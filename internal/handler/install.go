package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/takahira/agentdone/internal/claudedir"
	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
)

// hookCmd / hookMatcher describe the entries WE write. Other tools' entries are
// never round-tripped through these structs: the hooks subtree is held as raw
// JSON (see loadSettings), because a typed round-trip would silently drop every
// field the struct doesn't know — settings.json hooks legitimately carry
// timeout, args, statusMessage, once, asyncRewake, prompt, model, … and a
// "prompt"-type hook squeezed through this struct would lose its prompt body.
//
// Args is always present (an empty array, never null): its presence selects
// Claude Code's EXEC-FORM, which spawns the command directly without a shell.
// Shell-form would word-split a path containing spaces (standard on Windows:
// C:\Users\First Last\…) and let Git Bash's quote-removal eat backslashes —
// either way every hook dies with exit 127. With no shell in the path, neither
// can happen, on any OS. (agentdone is a real single executable, so exec-form's
// Windows restriction — no .cmd/.bat shims — does not apply.)
type hookCmd struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Async   bool     `json:"async,omitempty"`
}

type hookMatcher struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []hookCmd `json:"hooks"`
}

// wiring describes one settings.json hook entry for this tool.
type wiring struct {
	event   string
	matcher string
	async   bool
}

func wirings() []wiring {
	return []wiring{
		{"UserPromptSubmit", "", false},
		{"Stop", "", true},
		{"StopFailure", "", true},
		{"Notification", "", true},
		{"PreToolUse", "AskUserQuestion|ExitPlanMode", true},
	}
}

func settingsPath() (string, error) {
	base, err := claudedir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "settings.json"), nil
}

// Init wires the hooks into ~/.claude/settings.json (idempotently, preserving
// existing settings and hooks) and sends a test notification.
func Init() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Forward slashes only: Claude Code runs hook commands through Git Bash on
	// Windows, whose quote removal eats backslashes — C:\Users\...\agentdone.exe
	// would resolve to C:Users... and exit 127 on every event. C:/... works in
	// Git Bash and PowerShell alike; on Unix this is a no-op.
	exe = filepath.ToSlash(exe)
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := wireInto(path, exe); err != nil {
		return err
	}
	fmt.Printf("Wired agentdone into %s\n", path)

	switch url, werr := config.ResolveWebhook(); {
	case werr != nil:
		fmt.Printf("Webhook is set but invalid: %v\n", werr)
	case url != "":
		ping := ":white_check_mark: agentdone: setup complete (test notification)"
		if wantJapanese() {
			ping = ":white_check_mark: agentdone: セットアップ完了（テスト通知）"
		}
		if err := slack.Post(url, ping); err != nil {
			fmt.Printf("Webhook test failed: %v\n", err)
		} else {
			fmt.Println("Sent a test notification to Slack.")
		}
	default:
		fmt.Println("No webhook configured. Set SLACK_WEBHOOK_URL or write ~/.claude/hooks/.webhook (chmod 600).")
	}
	return nil
}

// Uninstall removes this tool's hook entries from settings.json, leaving any
// other hooks intact.
func Uninstall() error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	removed, err := unwireFrom(path)
	if err != nil {
		return err
	}
	state.Clear()
	// Show exactly which hook commands were removed: isOurs matches by binary
	// name, so if it ever removed something unexpected the user sees it here.
	switch {
	case len(removed) > 0:
		fmt.Printf("Removed agentdone from %s:\n", path)
		for _, c := range removed {
			fmt.Printf("  - %s\n", c)
		}
	default:
		fmt.Printf("Nothing of agentdone wired in %s; nothing to remove.\n", path)
	}
	return nil
}

// wireInto wires our hooks into the settings file at path (creating it if
// absent), recording exe as the command to invoke. Idempotent; preserves any
// unrelated settings and other tools' hooks.
func wireInto(path, exe string) error {
	root, hooks, err := loadSettings(path)
	if err != nil {
		return err
	}
	for _, w := range wirings() {
		hooks[w.event] = ensureWired(hooks[w.event], exe, w)
	}
	return saveSettings(path, root, hooks)
}

// unwireFrom removes this tool's hook entries from the settings file at path,
// dropping now-empty events and leaving everything else intact. It returns the
// commands of the entries it removed. A missing settings file — or one with
// nothing of ours wired — is left exactly as it is: uninstall must not create
// or rewrite a file it has nothing to remove from.
func unwireFrom(path string) ([]string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	root, hooks, err := loadSettings(path)
	if err != nil {
		return nil, err
	}
	var removed []string
	for event, matchers := range hooks {
		kept, rm := removeWired(matchers)
		removed = append(removed, rm...)
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	return removed, saveSettings(path, root, hooks)
}

// loadSettings reads the settings file. The hooks subtree is decoded only one
// level deep — each matcher entry stays a raw JSON value — so other tools'
// entries round-trip losslessly, unknown fields and all.
func loadSettings(path string) (map[string]json.RawMessage, map[string][]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case !os.IsNotExist(err):
		return nil, nil, err
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := root["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, fmt.Errorf("parse hooks in %s: %w", path, err)
		}
	}
	// A literal `null` (whole file or "hooks" value) unmarshals as a no-op /
	// nil map; make sure both maps are non-nil so the callers' writes can't
	// panic on a degenerate settings file.
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	if hooks == nil {
		hooks = map[string][]json.RawMessage{}
	}
	return root, hooks, nil
}

func saveSettings(path string, root map[string]json.RawMessage, hooks map[string][]json.RawMessage) error {
	hb, err := json.Marshal(hooks)
	if err != nil {
		return err
	}
	root["hooks"] = hb
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	// If settings.json is a symlink (dotfiles/stow setups), write through to the
	// target: renaming over the link would replace the LINK with a regular file
	// and orphan the managed original.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Preserve the file's permissions: a user who keeps settings.json 0600
	// (e.g. API keys in `env`) must not have init/uninstall silently widen it —
	// or its backup — to group/other-readable. New files get 0644.
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	// Back up an existing settings file before overwriting it. WriteFile only
	// applies the mode on creation, so chmod too: an earlier wider .bak must
	// follow the settings file's (possibly tightened) permissions. Make the .bak
	// writable FIRST: if settings.json is kept read-only (0400/0444), the .bak
	// inherited that mode, and WriteFile's O_TRUNC would fail with EACCES — every
	// run after the first would silently keep a stale backup.
	if cur, err := os.ReadFile(path); err == nil {
		bak := path + ".bak"
		_ = os.Chmod(bak, 0o600) // no-op (error ignored) when .bak doesn't exist yet
		if werr := os.WriteFile(bak, cur, mode); werr == nil {
			_ = os.Chmod(bak, mode)
		}
	}
	// Atomic write: temp in the same dir + rename, so a crash mid-write can't
	// truncate the user's settings.json.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentdone-settings-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// binaryName is this tool's executable name. Hook entries are identified by it
// (the command's base name), not the full install path, so that re-running init
// from a different location — e.g. the curl installer's ~/.claude/bin/agentdone
// vs `go install`'s ~/go/bin/agentdone — updates the wiring in place instead of
// leaving a stale duplicate that fires a second notification.
const binaryName = "agentdone"

// isOurs reports whether a hook command is one of ours, by the base name of
// its executable. Besides the bare path init writes, this recognises a
// manually wired variant — `agentdone --flag`, or a quoted "/path/agentdone"
// — so re-running init updates it in place instead of stacking a duplicate
// that would fire a second notification. The whole-string base is also kept:
// an unquoted path with spaces in a DIRECTORY ("/Users/First Last/bin/agentdone")
// has no meaningful first token but still ends in our binary name.
func isOurs(command string) bool {
	if isBinary(filepath.Base(command)) {
		return true
	}
	tok := strings.TrimSpace(command)
	if strings.HasPrefix(tok, `"`) {
		if end := strings.Index(tok[1:], `"`); end >= 0 {
			return isBinary(filepath.Base(tok[1 : 1+end]))
		}
		return false
	}
	if f := strings.Fields(tok); len(f) > 0 {
		return isBinary(filepath.Base(f[0]))
	}
	return false
}

func isBinary(base string) bool {
	return base == binaryName || base == binaryName+".exe"
}

// executableOf extracts the executable part of a hook command, mirroring the
// shapes isOurs recognises: the whole string (an unquoted path, possibly with
// spaces in a directory), a quoted first token, or the first field. Returns ""
// when no executable can be identified.
func executableOf(command string) string {
	if isBinary(filepath.Base(command)) {
		return command
	}
	tok := strings.TrimSpace(command)
	if strings.HasPrefix(tok, `"`) {
		if end := strings.Index(tok[1:], `"`); end >= 0 {
			return tok[1 : 1+end]
		}
		return ""
	}
	if f := strings.Fields(tok); len(f) > 0 {
		return f[0]
	}
	return ""
}

// ensureWired makes the event's matchers hold exactly one entry of ours: it
// drops any previous entry (so a changed install path is updated, not
// duplicated) and appends a fresh one pointing at exe. Idempotent.
func ensureWired(matchers []json.RawMessage, exe string, w wiring) []json.RawMessage {
	matchers, _ = removeWired(matchers)
	entry, err := json.Marshal(hookMatcher{
		Matcher: w.matcher,
		Hooks:   []hookCmd{{Type: "command", Command: exe, Args: []string{}, Async: w.async}},
	})
	if err != nil {
		return matchers // can't happen for this literal struct
	}
	return append(matchers, entry)
}

// removeWired drops every hook of ours from the matchers and discards any
// matcher left with no hooks, returning the kept matchers and the commands of
// the removed hooks. A matcher holding none of our hooks — or one this tool
// cannot parse — keeps its exact raw bytes; only a matcher we actually edit is
// re-encoded, and even then its values (including fields we don't model) are
// carried over verbatim.
func removeWired(matchers []json.RawMessage) (kept []json.RawMessage, removed []string) {
	for _, raw := range matchers {
		m := map[string]json.RawMessage{}
		var hooks []json.RawMessage
		if json.Unmarshal(raw, &m) != nil || json.Unmarshal(m["hooks"], &hooks) != nil {
			kept = append(kept, raw) // not a shape we understand: leave untouched
			continue
		}
		var keptHooks []json.RawMessage
		var rm []string
		for _, h := range hooks {
			var cmd struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(h, &cmd) == nil && isOurs(cmd.Command) {
				rm = append(rm, cmd.Command)
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		switch {
		case len(rm) == 0:
			kept = append(kept, raw)
			continue
		case len(keptHooks) > 0:
			hb, err := json.Marshal(keptHooks)
			if err != nil {
				kept = append(kept, raw)
				continue
			}
			m["hooks"] = hb
			mb, err := json.Marshal(m)
			if err != nil {
				kept = append(kept, raw)
				continue
			}
			kept = append(kept, mb)
		}
		// a matcher whose hooks were all ours is dropped entirely
		removed = append(removed, rm...)
	}
	return kept, removed
}
