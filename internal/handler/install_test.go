package handler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// commandsIn returns every hook command across the (raw) matcher entries.
func commandsIn(matchers []json.RawMessage) []string {
	var cmds []string
	for _, raw := range matchers {
		var m struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, h := range m.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

// ourCommands returns the commands of every entry of ours across the matchers.
func ourCommands(matchers []json.RawMessage) []string {
	var cmds []string
	for _, c := range commandsIn(matchers) {
		if isOurs(c) {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func tempSettings(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".claude", "settings.json")
}

// wireInto wires every event exactly once into a fresh settings file.
func TestWireIntoCreatesAllEvents(t *testing.T) {
	path := tempSettings(t)
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	_, hooks, err := loadSettings(path)
	if err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	for _, w := range wirings() {
		cmds := ourCommands(hooks[w.event])
		if len(cmds) != 1 || cmds[0] != "/curl/bin/agentdone" {
			t.Errorf("event %s: got our commands %v, want exactly [/curl/bin/agentdone]", w.event, cmds)
		}
	}
}

// our entries are wired EXEC-FORM — args present as [] (never null) — so
// Claude Code spawns the binary without a shell and a path with spaces (or
// Windows backslashes) cannot be word-split or quote-eaten into exit 127.
func TestWireIntoUsesExecForm(t *testing.T) {
	path := tempSettings(t)
	if err := wireInto(path, "/Users/First Last/.claude/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	_, hooks, _ := loadSettings(path)
	for _, w := range wirings() {
		for _, raw := range hooks[w.event] {
			var m struct {
				Hooks []map[string]json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			for _, h := range m.Hooks {
				args, ok := h["args"]
				if !ok {
					t.Fatalf("event %s: our hook has no args key (shell-form): %s", w.event, raw)
				}
				if string(args) != "[]" {
					t.Errorf("event %s: args = %s, want [] (an absent/null args falls back to shell-form)", w.event, args)
				}
			}
		}
	}
}

// init/uninstall must not widen a 0600 settings.json (or its backup) to
// the 0644 default — the file can carry API keys via `env`.
func TestSaveSettingsPreservesMode(t *testing.T) {
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	for _, p := range []string{path, path + ".bak"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 600 (must not be widened)", p, perm)
		}
	}

	// A new file (no prior mode to preserve) still gets the 0644 default.
	fresh := tempSettings(t)
	if err := wireInto(fresh, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto fresh: %v", err)
	}
	if fi, err := os.Stat(fresh); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("fresh settings mode = %v (err=%v), want 644", fi.Mode().Perm(), err)
	}
}

// Re-running with the same exe must not duplicate entries.
func TestWireIntoIdempotent(t *testing.T) {
	path := tempSettings(t)
	for i := 0; i < 3; i++ {
		if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
			t.Fatalf("wireInto #%d: %v", i, err)
		}
	}
	_, hooks, _ := loadSettings(path)
	for _, w := range wirings() {
		if cmds := ourCommands(hooks[w.event]); len(cmds) != 1 {
			t.Errorf("event %s: got %d of our entries after repeated init, want 1 (%v)", w.event, len(cmds), cmds)
		}
	}
}

// switching install location updates the wiring in place instead of leaving
// a stale duplicate that would fire a second notification.
func TestWireIntoPathSwitchNoDuplicate(t *testing.T) {
	path := tempSettings(t)
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto curl: %v", err)
	}
	if err := wireInto(path, "/go/bin/agentdone"); err != nil {
		t.Fatalf("wireInto go: %v", err)
	}
	_, hooks, _ := loadSettings(path)
	for _, w := range wirings() {
		cmds := ourCommands(hooks[w.event])
		if len(cmds) != 1 || cmds[0] != "/go/bin/agentdone" {
			t.Errorf("event %s: after path switch got %v, want exactly [/go/bin/agentdone]", w.event, cmds)
		}
	}
}

// Unrelated settings and other tools' hooks must survive wiring.
func TestWireIntoPreservesOtherSettings(t *testing.T) {
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "model": "opus",
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "/other/tool"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	root, hooks, _ := loadSettings(path)
	if string(root["model"]) != `"opus"` {
		t.Errorf("non-hooks key dropped: model=%q", string(root["model"]))
	}
	// The unrelated Stop hook is still present...
	if !contains(commandsIn(hooks["Stop"]), "/other/tool") {
		t.Error("unrelated /other/tool hook was dropped from Stop")
	}
	// ...alongside exactly one of ours.
	if cmds := ourCommands(hooks["Stop"]); len(cmds) != 1 {
		t.Errorf("Stop: got %d of our entries, want 1 (%v)", len(cmds), cmds)
	}
}

// other tools' hook entries carry fields we don't model (timeout, args,
// statusMessage, once, asyncRewake, and whole "prompt"-type hooks). A typed
// round-trip would silently drop them all — init/uninstall must instead keep
// every untouched entry byte-for-byte (modulo JSON re-encoding), so the other
// matcher has to come out semantically identical to how it went in.
func TestWireIntoPreservesUnknownHookFields(t *testing.T) {
	otherMatcher := `{"matcher": "Bash", "hooks": [
      {"type": "command", "command": "/other/tool", "timeout": 30, "args": ["--flag", "x"], "statusMessage": "running other", "once": true, "asyncRewake": true},
      {"type": "prompt", "prompt": "Summarize what changed", "model": "claude-haiku-4-5"}
    ]}`
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"hooks": {"Stop": [` + otherMatcher + `]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	var want any
	if err := json.Unmarshal([]byte(otherMatcher), &want); err != nil {
		t.Fatal(err)
	}
	// The other matcher must survive wiring AND unwiring untouched.
	check := func(stage string) {
		t.Helper()
		_, hooks, err := loadSettings(path)
		if err != nil {
			t.Fatalf("%s: loadSettings: %v", stage, err)
		}
		for _, raw := range hooks["Stop"] {
			var got any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("%s: matcher unparseable: %v", stage, err)
			}
			if m, ok := got.(map[string]any); ok && m["matcher"] == "Bash" {
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: other tool's matcher was altered:\n got %s\nwant %s", stage, raw, otherMatcher)
				}
				return
			}
		}
		t.Errorf("%s: other tool's matcher disappeared", stage)
	}

	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	check("after init")
	if _, err := unwireFrom(path); err != nil {
		t.Fatalf("unwireFrom: %v", err)
	}
	check("after uninstall")
}

// a degenerate settings file (`null`, or `"hooks": null`) must not
// panic init/uninstall — both decode to nil maps that the writers index into.
func TestWireIntoNullSettings(t *testing.T) {
	for _, seed := range []string{`null`, `{"hooks": null}`} {
		path := tempSettings(t)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
			t.Errorf("wireInto on %q: %v", seed, err)
		}
		if _, err := unwireFrom(path); err != nil {
			t.Errorf("unwireFrom on %q: %v", seed, err)
		}
	}
}

// uninstall must not create a settings file that doesn't exist, and a
// no-op uninstall (nothing of ours wired) must not rewrite the file at all.
func TestUnwireFromMissingOrNoOp(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if removed, err := unwireFrom(missing); err != nil || removed != nil {
		t.Fatalf("unwireFrom(missing) = %v, %v; want nil, nil", removed, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("unwireFrom created a settings file out of thin air")
	}

	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"hooks": {"Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "/other/tool"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := unwireFrom(path); err != nil || len(removed) != 0 {
		t.Fatalf("no-op unwireFrom = %v, %v; want no removals", removed, err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != seed {
		t.Errorf("no-op uninstall rewrote the file:\n got %s\nwant %s", out, seed)
	}
}

// when settings.json is a symlink (dotfiles/stow), the atomic write must
// go through to the target — replacing the link with a regular file would
// orphan the managed original.
func TestSaveSettingsThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-settings.json")
	if err := os.WriteFile(target, []byte(`{"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}
	if err := wireInto(link, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto via symlink: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("settings.json symlink was replaced by a regular file (mode=%v err=%v)", fi.Mode(), err)
	}
	out, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || !contains(commandsInFile(t, target, "Stop"), "/curl/bin/agentdone") {
		t.Errorf("symlink target was not updated: %s", out)
	}
}

func commandsInFile(t *testing.T, path, event string) []string {
	t.Helper()
	_, hooks, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	return commandsIn(hooks[event])
}

// isOurs must catch manually wired variants (args, quoting) so init
// updates them instead of stacking a duplicate — without mistaking lookalikes.
func TestIsOurs(t *testing.T) {
	yes := []string{
		"/curl/bin/agentdone",
		"/go/bin/agentdone.exe",
		"agentdone --lang ja",
		`"/path with space/agentdone" --flag`,
		"/Users/First Last/.claude/bin/agentdone", // unquoted dir with a space: base name still ours
	}
	no := []string{
		"/x/agentdone-wrapper",
		"run agentdone",
		"/other/tool",
		"",
	}
	for _, c := range yes {
		if !isOurs(c) {
			t.Errorf("isOurs(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if isOurs(c) {
			t.Errorf("isOurs(%q) = true, want false", c)
		}
	}
}

// saveSettings backs up the prior file and writes valid JSON atomically.
func TestSaveSettingsBackupAndValidJSON(t *testing.T) {
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := `{"model":"sonnet"}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected a .bak backup: %v", err)
	}
	if string(bak) != prior {
		t.Errorf("backup = %q, want the pre-overwrite content %q", string(bak), prior)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Errorf("settings.json is not valid JSON after write: %s", out)
	}
}

// A missing settings file is treated as empty, not an error.
// A read-only settings.json (0400/0444) made the .bak inherit that mode; the
// next write's O_TRUNC then failed with EACCES and froze the backup a generation
// behind. The backup must keep tracking the current settings instead.
func TestSaveSettingsBackupRefreshesWhenReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file-mode semantics")
	}
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gen1 := `{"model":"opus","v":1}`
	if err := os.WriteFile(path, []byte(gen1), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil { // first wire: backs up gen1
		t.Fatalf("wireInto #1: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil { // keep it read-only for round 2
		t.Fatal(err)
	}
	if err := wireInto(path, "/other/bin/agentdone"); err != nil { // re-wire: must refresh .bak
		t.Fatalf("wireInto #2: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	// After the 2nd wire the .bak must hold the 1st wire's output (which contains
	// our hooks), NOT the frozen original gen1.
	if string(bak) == gen1 || !strings.Contains(string(bak), "agentdone") {
		t.Errorf(".bak was not refreshed on a read-only settings.json: %s", bak)
	}
}

func TestLoadSettingsMissingFile(t *testing.T) {
	root, hooks, err := loadSettings(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("loadSettings on missing file: %v", err)
	}
	if len(root) != 0 || len(hooks) != 0 {
		t.Errorf("missing file should yield empty maps, got root=%v hooks=%v", root, hooks)
	}
}

// A corrupt settings file aborts rather than clobbering the user's config.
func TestWireIntoCorruptAborts(t *testing.T) {
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	garbage := "{ this is not json"
	if err := os.WriteFile(path, []byte(garbage), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err == nil {
		t.Fatal("wireInto on corrupt settings returned nil, want an error")
	}
	out, _ := os.ReadFile(path)
	if string(out) != garbage {
		t.Errorf("corrupt settings was modified: %q", string(out))
	}
}

// unwireFrom removes only our entries and leaves other hooks intact.
func TestUnwireFromRemovesOnlyOurs(t *testing.T) {
	path := tempSettings(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "/other/tool"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wireInto(path, "/curl/bin/agentdone"); err != nil {
		t.Fatalf("wireInto: %v", err)
	}
	removed, err := unwireFrom(path)
	if err != nil {
		t.Fatalf("unwireFrom: %v", err)
	}
	if !contains(removed, "/curl/bin/agentdone") {
		t.Errorf("removed = %v, want it to report /curl/bin/agentdone", removed)
	}
	_, hooks, _ := loadSettings(path)
	for _, w := range wirings() {
		if cmds := ourCommands(hooks[w.event]); len(cmds) != 0 {
			t.Errorf("event %s: our entries survived uninstall: %v", w.event, cmds)
		}
	}
	if !contains(commandsIn(hooks["Stop"]), "/other/tool") {
		t.Error("unrelated /other/tool hook was removed by uninstall")
	}
}

// Init end-to-end (HOME redirected, no webhook) creates a valid settings file.
func TestInitSmoke(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SLACK_WEBHOOK_URL", "") // never POST to a real webhook from a test
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Init did not write settings.json: %v", err)
	}
	if !json.Valid(out) {
		t.Errorf("Init wrote invalid JSON: %s", out)
	}
	_, hooks, _ := loadSettings(path)
	for _, w := range wirings() {
		if len(hooks[w.event]) == 0 {
			t.Errorf("Init did not wire event %s", w.event)
		}
	}
}

// Uninstall end-to-end (HOME redirected) removes our entries and keeps others.
func TestUninstallSmoke(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [
      {"type": "command", "command": "/x/agentdone"},
      {"type": "command", "command": "/x/otherhook"}
    ]}]
  }
}`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	_, hooks, _ := loadSettings(path)
	if cmds := ourCommands(hooks["Stop"]); len(cmds) != 0 {
		t.Errorf("Uninstall left our entries: %v", cmds)
	}
	if !contains(commandsIn(hooks["Stop"]), "/x/otherhook") {
		t.Error("Uninstall removed the unrelated /x/otherhook hook")
	}
}
