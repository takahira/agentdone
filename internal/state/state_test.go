package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSavePeekDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // keep test state out of the real ~/.claude
	sid := "unit-save-peek-delete"
	defer Delete(sid)
	want := Turn{StartEpoch: 1234567890, Prompt: "やって", SessionTitle: "タイトル"}
	if err := Save(sid, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := Peek(sid)
	if !ok || got != want {
		t.Fatalf("Peek = %+v, %v; want %+v, true", got, ok, want)
	}
	if _, ok := Peek(sid); !ok { // Peek must not consume
		t.Fatal("Peek consumed the state")
	}
	Delete(sid)
	if _, ok := Peek(sid); ok {
		t.Fatal("Delete did not remove the state")
	}
}

func TestPeekMissing(t *testing.T) {
	if _, ok := Peek("unit-definitely-missing"); ok {
		t.Fatal("Peek of missing state returned ok=true")
	}
}

func TestSafeID(t *testing.T) {
	cases := map[string]string{
		"abc-123_DEF":           "abc-123_DEF",
		"../../x":               "x",
		"a/b/c":                 "abc",
		"":                      "none",
		"!@#$":                  "none",
		"3f9c1d2e-0000-uuidish": "3f9c1d2e-0000-uuidish",
	}
	for in, want := range cases {
		if got := safeID(in); got != want {
			t.Errorf("safeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathStaysInOurDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // assert the ~/.claude fallback, not an inherited override
	base := dir()
	// state lives under ~/.claude/hooks (macOS purges $TMPDIR after 3 days
	// of no access, which is shorter than a background task can pause a turn).
	if want := filepath.Join(home, ".claude", "hooks", "state"); base != want {
		t.Errorf("state dir = %q, want %q", base, want)
	}
	for _, id := range []string{"../../etc/passwd", "a/b", "....//", "/abs/path"} {
		if got := filepath.Dir(path(id)); got != base {
			t.Errorf("path(%q) escaped the state dir: dir=%q want=%q", id, got, base)
		}
	}
}

// a slow async Stop must not delete state a NEWER turn has saved since.
func TestDeleteIf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sid := "unit-delete-if"
	older := Turn{StartEpoch: 1, Prompt: "old"}
	newer := Turn{StartEpoch: 2, Prompt: "new"}
	if err := Save(sid, newer); err != nil {
		t.Fatal(err)
	}
	DeleteIf(sid, older) // the stale turn's delete must be a no-op
	if got, ok := Peek(sid); !ok || got != newer {
		t.Fatalf("DeleteIf removed a newer turn's state (got %+v, %v)", got, ok)
	}
	DeleteIf(sid, newer)
	if _, ok := Peek(sid); ok {
		t.Fatal("DeleteIf with the matching turn did not delete")
	}
}

// a symlink squatted where the state dir lives must be refused, not
// followed — following it would let another principal read session ids or
// feed forged turn state.
func TestSaveRejectsSymlinkStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // the squat is planted under the HOME-based path
	if err := os.MkdirAll(filepath.Join(home, ".claude", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(home, ".claude", "hooks", "state")); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}
	if err := Save("squat", Turn{StartEpoch: 1}); err == nil {
		t.Fatal("Save through a symlinked state dir succeeded, want refusal")
	}
}

func TestClearRemovesStateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save("clear-me", Turn{StartEpoch: 1}); err != nil {
		t.Fatal(err)
	}
	Clear()
	if _, err := os.Stat(dir()); !os.IsNotExist(err) {
		t.Errorf("Clear left the state dir behind (err=%v)", err)
	}
}

func TestSweepStale(t *testing.T) {
	// Isolate HOME so the sweep can't touch a real session's state.
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		t.Fatal(err)
	}

	oldJSON := filepath.Join(dir(), "oldsession.json")
	oldTmp := filepath.Join(dir(), "turn-orphan.tmp") // orphaned temp must be reclaimed too
	fresh := filepath.Join(dir(), "freshsession.json")
	for _, p := range []string{oldJSON, oldTmp, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Age the old files past staleAfter.
	stale := time.Now().Add(-staleAfter - time.Hour)
	for _, p := range []string{oldJSON, oldTmp} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	sweepStale()

	for _, p := range []string{oldJSON, oldTmp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale file not swept: %s (err=%v)", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh state file was swept away: %v", err)
	}
}

// A home path with a glob metacharacter ([ * ?) used to silently disable the
// sweep (filepath.Glob treated it as a pattern); os.ReadDir is literal.
func TestSweepStaleGlobMetacharHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user[1]")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "") // exercise the HOME-based path with the metachar
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir(), "oldsession.json")
	if err := os.WriteFile(old, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-staleAfter - time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	sweepStale()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale file not swept under a glob-metachar home (err=%v)", err)
	}
}

// TestShouldRemoveStale is the real guard for the re-stat fix: removal must
// require a SUCCESSFUL re-stat that still shows stale. A re-stat error (file
// gone, transient ESTALE on a networked home) must NOT delete — reverting the
// fix to `statErr != nil || stale` makes the error case below fail. (The
// concurrent test below cannot exercise this branch on a local FS, so the logic
// is unit-tested directly here.)
func TestShouldRemoveStale(t *testing.T) {
	d := t.TempDir()
	cutoff := time.Now().Add(-staleAfter)
	stat := func(name string, age time.Duration) os.FileInfo {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(age)
		_ = os.Chtimes(p, ts, ts)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	staleFI := stat("stale.json", -staleAfter-time.Hour)
	freshFI := stat("fresh.json", -time.Minute)
	_, statErr := os.Stat(filepath.Join(d, "gone.json"))
	if statErr == nil {
		t.Fatal("expected a stat error for a missing file")
	}
	cases := []struct {
		name string
		fi   os.FileInfo
		err  error
		want bool
	}{
		{"genuinely stale, re-stat ok -> remove", staleFI, nil, true},
		{"rename-replaced fresh, re-stat ok -> keep", freshFI, nil, false},
		{"re-stat error -> keep", nil, statErr, false},
	}
	for _, c := range cases {
		if got := shouldRemoveStale(c.fi, c.err, cutoff); got != c.want {
			t.Errorf("%s: shouldRemoveStale = %v, want %v", c.name, got, c.want)
		}
	}
}

// A concurrency SMOKE test (run under -race): a stale decoy plus a continuously
// re-saved session must not race, panic, or lose a just-saved turn. NOTE: this
// does not by itself exercise the re-stat error branch — the monitored file is
// always fresh so it never reaches the re-stat, and the decoy is consumed on the
// first sweep; that branch is covered by TestShouldRemoveStale above.
func TestSweepStaleKeepsFreshRewrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	// A permanently-stale decoy so sweepStale always has a removal candidate to
	// race against (Save() runs sweepStale internally).
	decoy := filepath.Join(dir(), "decoy.json")
	if err := os.WriteFile(decoy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleAfter - time.Hour)
	_ = os.Chtimes(decoy, old, old)

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				sweepStale()
			}
		}
	}()

	const sid = "race-keep-fresh"
	for i := 0; i < 2000; i++ {
		want := Turn{StartEpoch: int64(i + 1), Prompt: "p"}
		if err := Save(sid, want); err != nil {
			close(done)
			t.Fatalf("Save #%d: %v", i, err)
		}
		if got, ok := Peek(sid); !ok || got != want {
			close(done)
			t.Fatalf("fresh state lost to sweep at #%d: got %+v ok=%v", i, got, ok)
		}
	}
	close(done)
}

func TestSaveOverwriteParses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sid := "unit-overwrite"
	defer Delete(sid)
	if err := Save(sid, Turn{StartEpoch: 1, Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(sid, Turn{StartEpoch: 2, Prompt: "q"}); err != nil {
		t.Fatal(err)
	}
	got, ok := Peek(sid)
	if !ok || got.StartEpoch != 2 || got.Prompt != "q" {
		t.Fatalf("Peek after overwrite = %+v, %v", got, ok)
	}
}
