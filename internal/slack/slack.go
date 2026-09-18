// Package slack posts messages to a Slack incoming webhook.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxPostAttempts     = 3
	postDeliveryTimeout = 5 * time.Second
	initialRetryBackoff = 100 * time.Millisecond
	// maxRetryAfter caps a parsed Retry-After; anything above the delivery budget is
	// equivalent (the budget check gives up) and this avoids int64 overflow.
	maxRetryAfter = time.Hour
)

// sleepForRetry is replaceable by tests so retry behavior is verified without
// making the test suite wait for backoff timers.
var sleepForRetry = time.Sleep

// escape neutralises Slack's three control characters. Much of a notification
// is transcript-derived (prompt, summary, question, error), so an unescaped
// "<!channel>" in assistant output would mention the whole channel and
// "<url|label>" would render a disguised link; "Vec<T>" would just disappear.
// Slack asks that exactly &, <, > be encoded — emoji codes and mrkdwn survive.
func escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// Post sends text to the given webhook URL. A hook must never block Claude
// Code, so callers generally ignore the returned error; an empty URL is a no-op.
//
// When AGENTDONE_STDOUT=1 the message is printed to stdout instead of being
// posted — useful for testing, demos, and seeing exactly what would be sent.
// When AGENTDONE_DEBUG is set, a delivery failure is logged to stderr (which
// Claude Code captures) so "my pings silently stopped" is diagnosable without
// breaking the non-blocking contract.
func Post(webhookURL, text string) (err error) {
	defer func() {
		if err != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: notification not delivered: %v\n", redact(err))
		}
	}()
	// Mask inline credentials BEFORE escaping: the redaction patterns match on the
	// raw characters, and escape() would turn a quote or angle bracket inside a
	// value into an entity that the patterns no longer recognise. This is the single
	// egress point, so every caller is covered (see redact.go). The handler also
	// redacts before truncating a field — this pass is the backstop for callers
	// that don't truncate.
	text = RedactSecrets(text)
	text = escape(text)
	if os.Getenv("AGENTDONE_STDOUT") == "1" {
		fmt.Println(text)
		return nil
	}
	if webhookURL == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	// Notifications are emitted from hook handlers on the agent's completion
	// path. Five seconds gives transient network failures quick retries while
	// keeping a finished agent from making its user wait for tens of seconds.
	// The context is the ONLY cap, across requests and backoff sleeps: there is
	// deliberately no shorter per-attempt timeout. A slow Slack that answers in,
	// say, 3s must still succeed on the first POST as it did before retries
	// existed; cutting that attempt short would re-send a webhook Slack may have
	// already accepted (a duplicate) and could exhaust the budget on timeouts.
	ctx, cancel := context.WithTimeout(context.Background(), postDeliveryTimeout)
	defer cancel()
	client := &http.Client{}

	var lastErr error
	for attempt := 1; attempt <= maxPostAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
		if err != nil {
			return redact(err)
		}
		req.Header.Set("Content-Type", "application/json")
		// Track whether the request reached Slack in full. A transport error
		// BEFORE that (DNS, refused, TLS handshake, a reset while sending) means
		// Slack never saw the message, so a retry cannot duplicate it. An error
		// AFTER it (EOF or the deadline while waiting for the response) is
		// ambiguous: Slack may already have posted it, and an incoming webhook
		// has no idempotency key, so re-sending would duplicate the ping.
		var wrote atomic.Bool
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					wrote.Store(true)
				}
			},
		}))

		resp, requestErr := client.Do(req)
		retryable := false
		retryAfter := time.Duration(0)
		hasRetryAfter := false
		if requestErr != nil {
			// A transport error is a *url.Error whose message embeds the full webhook
			// URL — and the webhook URL IS the secret. Redact before returning so a
			// caller that prints it (e.g. `init`'s "Webhook test failed: %v") can't
			// leak the token to stdout / CI logs / a shared screen.
			lastErr = redact(requestErr)
			if wrote.Load() {
				return postDeliveryError(attempt, fmt.Errorf(
					"sent, but no response (not re-sent, to avoid a duplicate): %w", lastErr))
			}
			retryable = true
		} else {
			// Drain the body so the connection can be reused — bounded by a
			// LimitReader so a misbehaving intermediary cannot stream an oversized
			// body into the discard (Slack's real body is 2 bytes).
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("slack webhook returned %s", resp.Status)
			retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
			// Honor Retry-After on any retryable status, not just 429: a 503 that says
			// "retry in 3s" would otherwise burn every attempt within ~300ms and drop
			// a notification the delivery budget could have saved.
			if retryable {
				retryAfter, hasRetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			}
		}

		if !retryable || attempt == maxPostAttempts {
			return postDeliveryError(attempt, lastErr)
		}
		if err := ctx.Err(); err != nil {
			return postDeliveryError(attempt, fmt.Errorf("delivery deadline reached: %w", err))
		}

		delay := initialRetryBackoff << (attempt - 1)
		if hasRetryAfter {
			delay = retryAfter
		}
		// Do not begin a backoff that would exceed the total notification budget.
		if deadline, ok := ctx.Deadline(); ok && time.Now().Add(delay).After(deadline) {
			return postDeliveryError(attempt, fmt.Errorf("delivery deadline would expire before retrying: %w", lastErr))
		}
		sleepForRetry(delay)
	}
	return postDeliveryError(maxPostAttempts, lastErr)
}

func postDeliveryError(attempts int, err error) error {
	return fmt.Errorf("slack webhook delivery failed after %d attempt(s): %w", attempts, err)
}

// parseRetryAfter accepts both forms permitted by RFC 9110: seconds and an
// HTTP date. A past date means Slack permits an immediate retry.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	// All digits but too large for int64 (RFC 9110 delta-seconds has no upper
	// bound): treat as "wait at least the cap", not as an unparseable header.
	if isAllDigits(value) && len(strings.TrimLeft(value, "0")) > 18 {
		return maxRetryAfter, true
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// time.Duration is int64 nanoseconds: multiplying a huge second count
		// overflows to a negative value, which would slip past the delivery
		// budget check and retry immediately.
		if seconds > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return max(retryAt.Sub(now), 0), true
	}
	return 0, false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// redact strips the webhook URL from a transport error before it is logged: a
// *url.Error's message embeds the full URL, and a Slack webhook URL IS the
// secret. Only the scheme://host is kept for diagnosis.
func redact(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	host := "webhook"
	if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
		host = u.Scheme + "://" + u.Host + "/…"
	}
	return fmt.Errorf("%s %s: %w", ue.Op, host, ue.Err)
}
