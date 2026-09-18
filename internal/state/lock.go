package state

import (
	"os"
	"time"
)

// failureLockStaleAfter bounds how long a lock file is trusted. A process killed
// between acquire and release would otherwise mute a session's error
// notifications forever, which is the one outcome this tool must never produce.
const failureLockStaleAfter = 60 * time.Second

// lockWait is the total time WithFailureLock will wait for a holder to finish.
// Slack's whole delivery budget is 5 seconds, so this outlasts one POST; the
// caller still proceeds unlocked after it.
const lockWait = 6 * time.Second

// WithFailureLock runs fn while holding a cross-process lock for one session's
// failure note, and reports whether the lock was actually held.
//
// The repeat-error cooldown reads the note, POSTs, then writes the note. Without
// a lock two concurrent async StopFailure processes both read "no recent note"
// before either has written one, so both notify -- exactly in the queued wakeup
// storm the cooldown exists to collapse.
//
// It FAILS OPEN: if the lock cannot be taken within lockWait, fn runs anyway and
// ok is false. Blocking indefinitely, or returning without running fn, would
// trade a duplicate ping for a silently dropped error report -- and a missed
// error report is the worse failure by a wide margin. The caller re-checks the
// cooldown in that case, so the loser of a race usually still collapses.
//
// O_CREAT|O_EXCL on a lock file, not flock: it needs no new dependency and
// behaves the same on every platform the binary ships for, including over the
// network filesystems a shared state dir might live on.
func WithFailureLock(sessionID string, fn func()) (ok bool) {
	if err := ensureDir(); err != nil {
		fn()
		return false
	}
	lockPath := failurePath(sessionID) + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(lockPath) }()
			fn()
			return true
		}
		// A lock left behind by a killed process must not mute this session
		// permanently: reclaim it once it is provably older than any real
		// critical section.
		if st, serr := os.Stat(lockPath); serr == nil &&
			time.Since(st.ModTime()) > failureLockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			fn()
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
