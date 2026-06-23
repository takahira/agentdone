package config

import "testing"

func TestResolveWebhook(t *testing.T) {
	cases := []struct {
		name    string
		val     string
		wantURL string
		wantErr bool
	}{
		{"valid slack https", "https://hooks.slack.com/services/T/B/x", "https://hooks.slack.com/services/T/B/x", false},
		{"explicit :443 accepted", "https://hooks.slack.com:443/services/T/B/x", "https://hooks.slack.com:443/services/T/B/x", false},
		{"uppercase host accepted", "https://HOOKS.SLACK.COM/services/T/B/x", "https://HOOKS.SLACK.COM/services/T/B/x", false},
		{"http rejected", "http://hooks.slack.com/services/T/B/x", "", true},
		{"other host rejected", "https://evil.example.com/x", "", true},
		{"metadata host rejected", "https://169.254.169.254/latest", "", true},
		{"garbage rejected", "://nope", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("SLACK_WEBHOOK_URL", c.val)
			got, err := ResolveWebhook()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if got != c.wantURL {
				t.Errorf("url = %q, want %q", got, c.wantURL)
			}
		})
	}
}

func TestResolveWebhookUnset(t *testing.T) {
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("HOME", t.TempDir())     // ensure ~/.claude/hooks/.webhook is absent
	t.Setenv("CLAUDE_CONFIG_DIR", "") // …and that an inherited config dir can't supply one
	got, err := ResolveWebhook()
	if err != nil || got != "" {
		t.Fatalf("ResolveWebhook unset = %q, %v; want \"\", nil", got, err)
	}
}
