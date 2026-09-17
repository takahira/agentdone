package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func healthyDoctorSettings(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/T/B/X")

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
	return path
}

// Doctor reports missing wiring/webhook as a non-nil error and, once init has
// run with a webhook configured, a clean bill of health. It must never POST.
func TestDoctor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
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

// Claude Code does not run user hooks while either of these root settings is
// true. The hook entries can all be perfectly wired, so Doctor must inspect the
// settings root as well as the hooks subtree instead of reporting healthy.
func TestDoctorReportsHookRestrictionSettings(t *testing.T) {
	for _, setting := range []string{"disableAllHooks", "allowManagedHooksOnly"} {
		t.Run(setting, func(t *testing.T) {
			path := healthyDoctorSettings(t)
			root, hooks, err := loadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			root[setting] = json.RawMessage("true")
			if err := saveSettings(path, root, hooks); err != nil {
				t.Fatal(err)
			}

			var out strings.Builder
			if err := Doctor(&out); err == nil {
				t.Errorf("Doctor with %s=true = nil, want an error; output:\n%s", setting, out.String())
			}
			if !strings.Contains(out.String(), setting+"=true") {
				t.Errorf("Doctor output missing %s restriction:\n%s", setting, out.String())
			}
			// Keep reporting the underlying wiring too: fixing the restriction
			// should not require another Doctor run to discover a second problem.
			if !strings.Contains(out.String(), "✓ PreToolUse wired") {
				t.Errorf("Doctor stopped reporting wiring after %s restriction:\n%s", setting, out.String())
			}

			// The settings are switches, not presence flags: an explicit false
			// must leave an otherwise healthy setup healthy.
			root[setting] = json.RawMessage("false")
			if err := saveSettings(path, root, hooks); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := Doctor(&out); err != nil {
				t.Errorf("Doctor with %s=false = %v, want nil; output:\n%s", setting, err, out.String())
			}
		})
	}
}

// Doctor must validate the effective scope/schema of each of our entries. A
// command path alone is insufficient: these mutations either run PreToolUse for
// the wrong tools or change the exec/async contract that init deliberately set.
func TestDoctorReportsMisScopedWiring(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"matcher", func(m, _ map[string]any) { m["matcher"] = "Bash" }},
		{"type", func(_, h map[string]any) { h["type"] = "prompt" }},
		{"args missing", func(_, h map[string]any) { delete(h, "args") }},
		{"args null", func(_, h map[string]any) { h["args"] = nil }},
		{"args non-empty", func(_, h map[string]any) { h["args"] = []any{"--unexpected"} }},
		{"async", func(_, h map[string]any) { h["async"] = false }},
		{"one of two entries", func(m, h map[string]any) {
			wrong := make(map[string]any, len(h))
			for key, value := range h {
				wrong[key] = value
			}
			wrong["async"] = false
			m["hooks"] = append(m["hooks"].([]any), wrong)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := healthyDoctorSettings(t)
			root, hooks, err := loadSettings(path)
			if err != nil {
				t.Fatal(err)
			}
			var matcher map[string]any
			if err := json.Unmarshal(hooks["PreToolUse"][0], &matcher); err != nil {
				t.Fatal(err)
			}
			hook := matcher["hooks"].([]any)[0].(map[string]any)
			tt.mutate(matcher, hook)
			hooks["PreToolUse"][0], err = json.Marshal(matcher)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveSettings(path, root, hooks); err != nil {
				t.Fatal(err)
			}

			var out strings.Builder
			if err := Doctor(&out); err == nil {
				t.Errorf("Doctor with invalid %s = nil, want an error; output:\n%s", tt.name, out.String())
			}
			if !strings.Contains(out.String(), "PreToolUse wired but mis-scoped (re-run init)") {
				t.Errorf("Doctor output missing mis-scoped report for %s:\n%s", tt.name, out.String())
			}
			if strings.Contains(out.String(), "✓ PreToolUse wired") {
				t.Errorf("Doctor also reported invalid %s as healthy:\n%s", tt.name, out.String())
			}
		})
	}
}
