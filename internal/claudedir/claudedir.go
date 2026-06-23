// Package claudedir resolves Claude Code's config directory the way the Claude
// Code binary itself does: $CLAUDE_CONFIG_DIR when set, otherwise ~/.claude.
//
// settings.json, the webhook file and our per-turn state all live under this
// directory, so install / doctor / config / state must agree on it. When they
// don't — e.g. they hardcode ~/.claude while a user has CLAUDE_CONFIG_DIR set
// (containers, CI, multi-account, dotfile managers) — `init` wires hooks into a
// file Claude Code never reads, every hook stays silent, and `doctor` reads its
// own wiring and reports everything healthy. One resolver keeps all four honest.
package claudedir

import (
	"os"
	"path/filepath"
	"strings"
)

// Dir returns the Claude Code config directory: $CLAUDE_CONFIG_DIR if set to a
// non-empty value, otherwise <home>/.claude. It errors only when neither is
// available (no CLAUDE_CONFIG_DIR and an unresolvable home).
//
// This mirrors Claude Code's own resolution (verified against v2.1.168:
// `process.env.CLAUDE_CONFIG_DIR ?? path.join(os.homedir(), ".claude")`). A
// CLAUDE_CONFIG_DIR set to empty/whitespace is treated as unset — Claude Code's
// `??` would keep the empty string, but that yields a meaningless relative path,
// so falling back to ~/.claude is the only safe reading.
func Dir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}
