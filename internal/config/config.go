// Package config resolves runtime configuration (the Slack webhook URL).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/takahira/agentdone/internal/claudedir"
)

// allowAnyWebhookHost relaxes the hooks.slack.com host/scheme check in
// ResolveWebhook. It is false in every shipped binary; only the test helper
// below flips it. The package lives under internal/, so nothing outside this
// module can import it, and a Go var cannot be set at runtime regardless.
var allowAnyWebhookHost = false

// SetAllowAnyWebhookHostForTest relaxes the Slack-host guard so a test can POST
// to a local httptest server, returning a function that restores the prior
// value. It exists only for tests in other packages (config's own tests use the
// unexported var directly); production code has no reason to call it.
func SetAllowAnyWebhookHostForTest(allow bool) (restore func()) {
	prev := allowAnyWebhookHost
	allowAnyWebhookHost = allow
	return func() { allowAnyWebhookHost = prev }
}

// WebhookFilePath is the file rawWebhook actually reads, or "" if the Claude
// directory cannot be resolved. Setup guidance MUST print this rather than a
// hardcoded ~/.claude/hooks/.webhook: under CLAUDE_CONFIG_DIR the two differ, so
// `init` and `doctor` were telling users to create the file somewhere nothing
// reads it -- and then `doctor` repeated the same wrong remediation forever
// while every notification stayed silent.
func WebhookFilePath() string {
	base, err := claudedir.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "hooks", ".webhook")
}

// rawWebhook returns the configured webhook string — the SLACK_WEBHOOK_URL
// environment variable, or failing that the file at WebhookFilePath — or "" if
// neither is set. The value is never compiled into the binary or committed.
// A .webhook that EXISTS but cannot be read (permissions, I/O) is an error,
// not "unset": reporting it as unset made a broken setup undiagnosable even
// with AGENTDONE_DEBUG.
func rawWebhook() (string, error) {
	if v := strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL")); v != "" {
		return v, nil
	}
	p := WebhookFilePath()
	if p == "" {
		return "", nil
	}
	b, err := os.ReadFile(p)
	switch {
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// errInvalidWebhookURL is intentionally value-free: the only thing a caller may
// print about an unparseable webhook is that it was unparseable.
var errInvalidWebhookURL = errors.New("invalid Slack webhook URL")

// ResolveWebhook returns the configured Slack webhook URL after checking it is an
// https://hooks.slack.com/... endpoint. It returns ("", nil) when nothing is
// configured and ("", err) when a value is set but is not a valid Slack webhook.
// Validating here — not at the POST — keeps a misconfigured or attacker-supplied
// URL from ever being POSTed to (e.g. an internal http://169.254.169.254/ SSRF).
func ResolveWebhook() (string, error) {
	raw, err := rawWebhook()
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Deliberately NOT wrapped: a url.Error's message embeds the whole raw
		// URL, so wrapping it put the webhook token -- the secret itself -- into
		// an error that `agentdone init` and `doctor` print to a terminal or a CI
		// log. The parse failure tells the user nothing the generic message does
		// not; under AGENTDONE_DEBUG they can still see the position.
		if os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: webhook URL failed to parse (value withheld); check for spaces or control characters\n")
		}
		return "", errInvalidWebhookURL
	}
	// Compare the host case-insensitively and without any port: url.Host keeps
	// both ("HOOKS.SLACK.COM", "hooks.slack.com:443"), so an exact == would
	// reject those otherwise-valid Slack webhooks and silence all notifications.
	// (url.Parse already lower-cases the scheme.) The host is still pinned to
	// hooks.slack.com, so the SSRF guard holds.
	if !allowAnyWebhookHost && (u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "hooks.slack.com")) {
		return "", fmt.Errorf("webhook must be an https://hooks.slack.com/... URL")
	}
	return raw, nil
}

// WebhookURL returns the validated webhook URL, or "" when it is unset or
// invalid. Notification handlers use this and treat "" as "do not post". When
// AGENTDONE_DEBUG is set, a configured-but-invalid webhook is logged to stderr
// so a misconfiguration after `init` is diagnosable.
func WebhookURL() string {
	u, err := ResolveWebhook()
	if err != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: webhook unusable: %v\n", err)
	}
	return u
}
