package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Doctor reports missing wiring/webhook as a non-nil error and, once init has
// run with a webhook configured, a clean bill of health. It must never POST.
func TestDoctor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SLACK_WEBHOOK_URL", "")

	var out strings.Builder
	if err := Doctor(&out); err == nil {
		t.Errorf("Doctor on a bare HOME = nil, want an error; output:\n%s", out.String())
	}
	for _, want := range []string{"not wired", "not configured"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Doctor output missing %q:\n%s", want, out.String())
		}
	}

	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/T/B/X")
	// Wire directly with the real binary name: Init would record the TEST
	// binary's path (handler.test), which isOurs rightly does not recognise.
	// The wired command must point at a real file — Doctor stats it.
	bin := filepath.Join(t.TempDir(), "agentdone")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, bin); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	out.Reset()
	if err := Doctor(&out); err != nil {
		t.Errorf("Doctor after init = %v; output:\n%s", err, out.String())
	}
	for _, want := range []string{"Stop wired", "webhook: ✓", "state:   ✓"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Doctor output missing %q:\n%s", want, out.String())
		}
	}

	// Settings can outlive the binary they point at (a cleaned ~/.claude/bin, a
	// removed `go install`): the wiring is present but every hook exits 127, so
	// Doctor must flag the dangling command instead of a clean bill of health.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Doctor(&out); err == nil {
		t.Errorf("Doctor with a dangling wired command = nil, want an error; output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "wired command not found") {
		t.Errorf("Doctor output missing the dangling-command report:\n%s", out.String())
	}
}
