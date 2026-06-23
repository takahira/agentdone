// Package config resolves runtime configuration (the Slack webhook URL).
package config

import (
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

// rawWebhook returns the configured webhook string — the SLACK_WEBHOOK_URL
// environment variable, or failing that ~/.claude/hooks/.webhook — or "" if
// neither is set. The value is never compiled into the binary or committed.
// A .webhook that EXISTS but cannot be read (permissions, I/O) is an error,
// not "unset": reporting it as unset made a broken setup undiagnosable even
// with AGENTDONE_DEBUG.
func rawWebhook() (string, error) {
	if v := strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL")); v != "" {
		return v, nil
	}
	base, err := claudedir.Dir()
	if err != nil {
		return "", nil
	}
	p := filepath.Join(base, "hooks", ".webhook")
	b, err := os.ReadFile(p)
	switch {
	case os.IsNotExist(err):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return strings.TrimSpace(string(b)), nil
}

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
		return "", fmt.Errorf("invalid Slack webhook URL: %w", err)
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
