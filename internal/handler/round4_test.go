package handler

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// isolate points HOME (and the state dir with it) at a fresh temp directory and
// clears every env var that could leak a real configuration into the test.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("AGENTDONE_STDOUT", "")
	t.Setenv("AGENTDONE_DEBUG", "")
	return home
}

func failure(session, errText string) *cchooks.StopFailure {
	return &cchooks.StopFailure{
		Common:       cchooks.Common{SessionID: session},
		ErrorType:    "api_error",
		ErrorMessage: errText,
	}
}

// TestNoWebhookDoesNotStartTheCooldown is the "silence" bug: slack.Post returns
// nil for an empty URL, so a failure with nothing configured recorded a note.
// The user then configures .webhook and the SAME error is muted for the next 30
// minutes -- the exact error they set the webhook up for.
func TestNoWebhookDoesNotStartTheCooldown(t *testing.T) {
	isolate(t)
	in := failure("sess-nocfg", "rate limit")

	StopFailure(in) // nothing configured: must not leave a note

	if _, ok := state.PeekFailure("sess-nocfg"); ok {
		t.Fatal("a failure with no webhook configured recorded a cooldown note; " +
			"the next 30 minutes of that error would be silently muted")
	}
}

// TestStdoutModeDoesStartTheCooldown guards the other side: under
// AGENTDONE_STDOUT=1 stdout IS the delivery, so the cooldown must still engage.
func TestStdoutModeDoesStartTheCooldown(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTDONE_STDOUT", "1")

	StopFailure(failure("sess-stdout", "rate limit"))

	if _, ok := state.PeekFailure("sess-stdout"); !ok {
		t.Fatal("stdout delivery did not start the cooldown; a wakeup storm " +
			"would print one line per queued task")
	}
}

// TestCooldownIsAtomicAcrossConcurrentFailures: two async StopFailure processes
// racing on the same session both read "no recent note" before either wrote one,
// so both notified -- in precisely the queued wakeup storm the cooldown exists
// to collapse. Goroutines stand in for processes; the lock is cross-process.
func TestCooldownIsAtomicAcrossConcurrentFailures(t *testing.T) {
	isolate(t)
	t.Setenv("AGENTDONE_STDOUT", "1") // deliver without a network call

	// Count deliveries by watching stdout.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 1<<16)
		n, _ := r.Read(buf)
		done <- string(buf[:n])
	}()

	const racers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			StopFailure(failure("sess-race", "overloaded_error"))
		}()
	}
	close(start)
	wg.Wait()

	os.Stdout = orig
	_ = w.Close()
	out := <-done

	got := strings.Count(out, "overloaded_error")
	if got != 1 {
		t.Fatalf("%d racing failures produced %d notifications, want exactly 1 "+
			"(the cooldown must collapse a wakeup storm)", racers, got)
	}
}

// TestWebhookHintFollowsClaudeConfigDir: install/doctor hardcoded
// ~/.claude/hooks/.webhook, so under CLAUDE_CONFIG_DIR the user was told to
// create the file somewhere nothing reads -- and doctor repeated the same wrong
// remediation while every notification stayed silent.
func TestWebhookHintFollowsClaudeConfigDir(t *testing.T) {
	isolate(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)

	hint := webhookHint()
	if !strings.HasPrefix(hint, custom) {
		t.Fatalf("setup guidance points at %q, outside CLAUDE_CONFIG_DIR %q", hint, custom)
	}
	// The guidance must name the file the config package actually reads.
	if hint != config.WebhookFilePath() {
		t.Fatalf("guidance %q != the file actually read %q", hint, config.WebhookFilePath())
	}
	// And that file must genuinely be the one picked up.
	if err := os.MkdirAll(filepath.Dir(hint), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hint, []byte("https://hooks.slack.com/services/T0/B0/xxx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.WebhookURL(); got == "" {
		t.Fatal("writing the advertised path did not configure a webhook")
	}
}
