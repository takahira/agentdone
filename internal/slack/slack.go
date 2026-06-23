// Package slack posts messages to a Slack incoming webhook.
package slack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

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
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		// A transport error is a *url.Error whose message embeds the full webhook
		// URL — and the webhook URL IS the secret. Redact before returning so a
		// caller that prints it (e.g. `init`'s "Webhook test failed: %v") can't
		// leak the token to stdout / CI logs / a shared screen.
		return redact(err)
	}
	defer resp.Body.Close()
	// Slack returns 200 "ok" on success; a 4xx/5xx (e.g. invalid webhook) must be
	// surfaced so `init`'s test ping doesn't falsely report success. Drain the
	// body first so the connection can be reused — bounded by a LimitReader so a
	// misbehaving intermediary can't stream an oversized body into the discard
	// (Slack's real body is 2 bytes; the 5s client Timeout already caps wall time).
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook returned %s", resp.Status)
	}
	return nil
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
