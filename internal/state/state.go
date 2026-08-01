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

// path names the turn-state file. Turn state is PER TURN, so it is keyed by
// session AND prompt: session_id alone cannot tell two overlapping turns apart,
// and the Stop hook runs async. A slow Stop for turn A would otherwise read the
// state turn B's UserPromptSubmit had just written, compute a near-zero elapsed
// time, and delete B's state on the way out -- silencing BOTH turns.
//
// Verified on real payloads (2026-08-01): the Stop for a turn carries the same
// prompt_id as the UserPromptSubmit that started it, which is what makes this
// key work.
//
// The separator is '.', which safeID strips from its input, so neither part can
// contain it and no crafted pair of ids can collide with another pair. The
// ".turn" tag keeps these clear of failurePath's ".stopfail.json".
func path(sessionID, promptID string) string {
	if promptID == "" {
		// No prompt_id (an older Claude Code, or an event that omits it): fall
		// back to the historical session-scoped name so behaviour is unchanged
		// rather than broken.
		return legacyPath(sessionID)
	}
	return filepath.Join(dir(), safeID(sessionID)+"."+safeID(promptID)+".turn.json")
}

// legacyPath is the pre-prompt_id session-scoped name, still read by Peek so an
// upgrade mid-session does not strand state written by the previous binary.
func legacyPath(sessionID string) string {
	return filepath.Join(dir(), safeID(sessionID)+".json")
}

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
func Save(sessionID, promptID string, t Turn) error {
	if err := ensureDir(); err != nil {
		return err
	}
	sweepStale()
	return writeJSON(path(sessionID, promptID), t)
}

// writeJSON marshals v and atomically replaces p with it: a concurrent reader
// sees either the old file or the new one, never a torn one. The temp lives in
// the same dir so Rename stays on one fs.
func writeJSON(p string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
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
// stored (e.g. a task-woken turn, whose synthetic wake prompt
// handler.UserPromptSubmit deliberately does not save). Reading without
// consuming matters for both mid-turn hooks (Notification, PreToolUse) and a
// withheld Stop: a Stop suppressed because background work is still running must
// leave the state for the later completion (the woken turn) to label itself.
func Peek(sessionID, promptID string) (t Turn, ok bool) {
	p := path(sessionID, promptID)
	b, err := os.ReadFile(p)
	if err != nil && os.IsNotExist(err) && promptID != "" {
		// Upgrade window only: state written by a pre-prompt_id binary earlier in
		// this same session still lives under the session-scoped name. Reading it
		// is strictly better than losing the turn; new writes always use the
		// prompt-scoped name, so this path dies out on its own.
		p = legacyPath(sessionID)
		b, err = os.ReadFile(p)
	}
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
func Delete(sessionID, promptID string) {
	_ = os.Remove(path(sessionID, promptID))
	if promptID != "" {
		// Also clear any legacy session-scoped state Peek may have just used, so
		// the upgrade-window fallback cannot resurrect a consumed turn.
		_ = os.Remove(legacyPath(sessionID))
	}
}

// DeleteIf removes the stored turn state only while it still holds expect — a
// compare-and-delete. The Stop hook runs async, so by the time a slow Stop gets
// to its delete the next turn's UserPromptSubmit may have saved NEW state;
// deleting blindly would strip that turn of its prompt/start and silence its
// completion. (Read-compare-remove, not atomic: the remaining window is the
// few µs between the read and the remove, vs. the hook's whole runtime.)
func DeleteIf(sessionID, promptID string, expect Turn) {
	cur, ok := Peek(sessionID, promptID)
	if ok && cur == expect {
		Delete(sessionID, promptID)
	}
}

// FailureNote records the last StopFailure notification actually sent for a
// session: when it went out (Now() milliseconds) and the error text it carried.
// It lives in its own <session>.stopfail.json rather than inside Turn because
// the two have different lifetimes: a failure note must survive the turn (the
// next failing turn is what reads it), while turn state is per-turn. The same
// stale sweep and uninstall Clear reclaim it.
type FailureNote struct {
	Epoch int64  `json:"epoch"`
	Error string `json:"error"`
}

// failurePath is collision-free against path(): safeID strips '.', so no
// crafted session id can name another session's ".stopfail.json" file.
func failurePath(sessionID string) string {
	return filepath.Join(dir(), safeID(sessionID)+".stopfail.json")
}

// SaveFailure writes the failure note for a session (atomic, like Save).
func SaveFailure(sessionID string, n FailureNote) error {
	if err := ensureDir(); err != nil {
		return err
	}
	return writeJSON(failurePath(sessionID), n)
}

// PeekFailure reads the failure note without removing it. ok is false when no
// note is stored or it is unreadable/corrupt. Unlike Peek there is no debug
// logging: losing a note fails OPEN to one extra error notification, not to
// the silently dropped ping Peek's logging exists to diagnose.
func PeekFailure(sessionID string) (n FailureNote, ok bool) {
	b, err := os.ReadFile(failurePath(sessionID))
	if err != nil || json.Unmarshal(b, &n) != nil {
		return FailureNote{}, false
	}
	return n, true
}

// ClearFailure removes the failure note, if any. Called when a turn truly
// completes: the failure episode is over, and the next error — identical or
// not — is news again.
func ClearFailure(sessionID string) { _ = os.Remove(failurePath(sessionID)) }

// Now returns the current unix time in milliseconds. Handlers share this clock.
// Milliseconds (not whole seconds) keep turn-boundary token accounting precise
// (see transcript.epoch).
func Now() int64 { return time.Now().UnixMilli() }
