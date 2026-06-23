package handler

import (
	"os"
	"testing"

	"github.com/takahira/agentdone/internal/config"
)

// TestMain relaxes the Slack-host check for the whole handler test binary: these
// tests POST to a local httptest server, not the real hooks.slack.com. HOME is
// redirected for the whole binary so handler tests can never write turn state
// (or settings) into the developer's real ~/.claude; tests that need their own
// HOME still override it per-test with t.Setenv.
func TestMain(m *testing.M) {
	config.SetAllowAnyWebhookHostForTest(true) // whole test binary; process exits, no restore needed
	os.Setenv("AGENTDONE_LANG", "ja")          // existing handler tests assert Japanese output
	os.Unsetenv("CLAUDE_CONFIG_DIR")           // tests assume paths resolve under the redirected HOME
	home, err := os.MkdirTemp("", "agentdone-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
