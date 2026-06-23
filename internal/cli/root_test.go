package cli

import (
	"io"
	"os"
	"strings"
	"testing"
)

// safely must swallow a handler panic so the hook never crashes Claude Code.
func TestSafelyRecoversFromPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("safely let a panic escape: %v", r)
		}
	}()
	safely(func() { panic("boom") })
}

// Under AGENTDONE_DEBUG a recovered panic is logged to stderr — otherwise a
// panicking handler is indistinguishable from a turn with nothing to say.
func TestSafelyLogsPanicUnderDebug(t *testing.T) {
	t.Setenv("AGENTDONE_DEBUG", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	safely(func() { panic("boom") })
	os.Stderr = orig
	w.Close()
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "panic recovered: boom") {
		t.Fatalf("stderr missing the recovered panic under AGENTDONE_DEBUG: %q", out)
	}
}

// safely still runs fn on the happy path.
func TestSafelyRunsFn(t *testing.T) {
	ran := false
	safely(func() { ran = true })
	if !ran {
		t.Fatal("safely did not run fn")
	}
}

// dispatch swallows malformed input (Parse error) and never errors.
func TestDispatchSwallowsMalformedInput(t *testing.T) {
	if err := dispatch(strings.NewReader("not json")); err != nil {
		t.Fatalf("dispatch on malformed input returned %v, want nil", err)
	}
}
