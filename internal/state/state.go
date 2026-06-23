// Package state persists per-turn data between the UserPromptSubmit and Stop
// hooks, which run as separate process invocations.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/takahira/agentdone/internal/claudedir"
)

// Turn is the data captured at UserPromptSubmit and consumed at Stop.
type Turn struct {
	StartEpoch   int64  `json:"start_epoch"`
	Prompt       string `json:"prompt"`
	SessionTitle string `json:"session_title"`
}

// staleAfter is how long an unconsumed turn-state file is kept before the next
// Save sweeps it. State is only useful for the lifetime of a single turn, so a
// generous TTL reclaims files from sessions that ended without a consuming Stop
// (SessionEnd, crash, StopFailure). Set well beyond any realistic turn — a
// background task can pause a turn for a long time — so the sweep never discards
// state a still-running turn will need when its task finally wakes it.
const staleAfter = 7 * 24 * time.Hour

// dir is the per-user state directory. It lives under the Claude Code config
// dir's hooks/ subtree (claudedir.Dir — $CLAUDE_CONFIG_DIR or ~/.claude), NOT
// the temp dir: macOS purges $TMPDIR entries after 3 days without access, and a
// background task can keep a turn paused longer than that with no hook touching
// the state file in between — the OS would delete it mid-turn and the eventual
// completion would lose its question/start (or go silent entirely). The home
// location is also inherently per-user (created 0700 all the same) and is
// removed wholesale by uninstall. Only with no resolvable home do we fall back
// to a per-user temp dir rather than failing the hook.
func dir() string {
	if base, err := claudedir.Dir(); err == nil && base != "" {
		return filepath.Join(base, "hooks", "state")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("agentdone-%d", os.Getuid()))
}

// Clear removes the state directory and everything in it. Called by uninstall
// so nothing of ours outlives the hook wiring.
func Clear() { _ = os.RemoveAll(dir()) }

// ensureDir creates the state directory (0700) and rejects a pre-squatted
// path: a symlink or a non-directory planted where our state lives would let
// another principal read session ids or feed us forged turn state, so we
// refuse to use it rather than follow it. An over-permissive existing dir is
// tightened back to 0700 (best-effort; failure to chmod someone else's dir
// also refuses).
func ensureDir() error {
	d := dir()
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(d)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("state dir %s is not a real directory", d)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func path(sessionID string) string { return filepath.Join(dir(), safeID(sessionID)+".json") }

// safeID strips everything but [A-Za-z0-9_-] from the session id before it is
// used in a state-file path. session_id is normally a UUID; this keeps a crafted
// value (e.g. "../../x") from escaping the state directory.
func safeID(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' || c == '_' ||
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			b = append(b, c)
		}
	}
	if len(b) == 0 {
		return "none"
	}
	return string(b)
}

// Save writes the turn state for a session and opportunistically sweeps stale
// state files left by sessions that never reached a consuming Stop.
func Save(sessionID string, t Turn) error {
	if err := ensureDir(); err != nil {
		return err
	}
	sweepStale()
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	p := path(sessionID)
	// Atomic write: a concurrent Peek sees either the old file or the new one,
	// never a torn one. The temp lives in the same dir so Rename stays on one fs.
	tmp, err := os.CreateTemp(filepath.Dir(p), "turn-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

// sweepStale removes state files (and any orphaned .tmp) older than staleAfter
// from our per-user dir. Best-effort: any error (read, stat, remove) is ignored
// so it never disrupts the hook.
//
// os.ReadDir, not filepath.Glob: a home path with a glob metacharacter
// ([ * ?) — legal in a directory name — would make the dir() pattern either
// match nothing or error out, silently disabling the sweep so stale files
// accumulate forever.
func sweepStale() {
	d := dir()
	ents, err := os.ReadDir(d)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleAfter)
	for _, ent := range ents {
		m := filepath.Join(d, ent.Name())
		fi, err := os.Stat(m)
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		// Re-stat right before removing: a Save in another process may have
		// rename-replaced this path with fresh state between the listing above and
		// here, and we must not delete that.
		again, err := os.Stat(m)
		if shouldRemoveStale(again, err, cutoff) {
			_ = os.Remove(m)
		}
	}
}

// shouldRemoveStale decides whether a file the listing already saw as stale
// should actually be removed, given a fresh re-stat (info, err). ONLY a
// successful re-stat that STILL shows stale removes: a re-stat error (the file
// already gone, or a transient ESTALE/EINTR on a networked home) or a now-fresh
// mtime keeps it — deleting on a re-stat error reintroduces the very
// fresh-state deletion the re-stat exists to prevent. Pulled out as a pure
// function so the rename-replace safety can be unit-tested without a filesystem
// race (reverting the err==nil guard must fail TestShouldRemoveStale).
func shouldRemoveStale(info os.FileInfo, statErr error, cutoff time.Time) bool {
	return statErr == nil && info.ModTime().Before(cutoff)
}

// Peek reads the turn state without removing it. ok is false when no state was
// stored (e.g. a turn with no preceding UserPromptSubmit). Reading without
// consuming matters for both mid-turn hooks (Notification, PreToolUse) and a
// withheld Stop: a Stop suppressed because background work is still running must
// leave the state for the later completion (the woken turn) to label itself.
func Peek(sessionID string) (t Turn, ok bool) {
	b, err := os.ReadFile(path(sessionID))
	if err != nil {
		// A missing file is the common, expected case (a turn with no preceding
		// UserPromptSubmit) and stays silent. Any OTHER read error (permission,
		// I/O on a flaky mount) silently drops the turn's start, which a
		// threshold-gated Stop then withholds — surface it under AGENTDONE_DEBUG.
		if !os.IsNotExist(err) && os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: turn state unreadable for %s: %v\n", sessionID, err)
		}
		return Turn{}, false
	}
	if err := json.Unmarshal(b, &t); err != nil {
		// The file exists but is corrupt/truncated — unexpected (Save uses an
		// atomic rename). This silently drops a completion ping (start unknown),
		// the project's worst failure mode, so make it diagnosable like the Save
		// path in userprompt.go rather than conflating it with "no state".
		if os.Getenv("AGENTDONE_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "agentdone: turn state corrupt for %s: %v\n", sessionID, err)
		}
		return Turn{}, false
	}
	return t, true
}

// Delete removes the stored turn state for a session, if any. Called once the
// turn is genuinely ending (a Stop that actually evaluates/sends), not on a Stop
// withheld for in-flight background work.
func Delete(sessionID string) { _ = os.Remove(path(sessionID)) }

// DeleteIf removes the stored turn state only while it still holds expect — a
// compare-and-delete. The Stop hook runs async, so by the time a slow Stop gets
// to its delete the next turn's UserPromptSubmit may have saved NEW state;
// deleting blindly would strip that turn of its prompt/start and silence its
// completion. (Read-compare-remove, not atomic: the remaining window is the
// few µs between the read and the remove, vs. the hook's whole runtime.)
func DeleteIf(sessionID string, expect Turn) {
	cur, ok := Peek(sessionID)
	if ok && cur == expect {
		Delete(sessionID)
	}
}

// Now returns the current unix time in milliseconds. Handlers share this clock.
// Milliseconds (not whole seconds) keep turn-boundary token accounting precise
// (see transcript.epoch).
func Now() int64 { return time.Now().UnixMilli() }
