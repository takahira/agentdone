// Package suppress decides when a "completed" notification is premature.
package suppress

import "github.com/takahira/agentdone/pkg/cchooks"

// WaitingOnBackground reports whether the turn ended with in-flight background
// work still registered. In that case the Stop is a pause — Claude will be
// re-invoked when the work completes — not a real completion, so the "done"
// notification should be withheld.
//
// The hook's background_tasks element carries no flag distinguishing work we are
// waiting on from a task the user pushed aside with Ctrl+B (that flag lives in a
// separate internal schema, absent from the hook payload). We therefore treat
// any running/pending task as "waiting", matching the official field semantic:
// "session is paused waiting for background work to wake it".
//
// Caveat: a long-running task the user backgrounded with Ctrl+B will keep
// suppressing completion pings until it ends. Distinguishing it would require
// the transcript-based "launched this turn" scoping.
func WaitingOnBackground(tasks []cchooks.BackgroundTask) bool {
	for _, t := range tasks {
		if t.Running() {
			return true
		}
	}
	return false
}
