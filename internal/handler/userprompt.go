package handler

import (
	"fmt"
	"os"
	"strings"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// taskNotificationPrefix opens the synthetic prompt Claude Code submits when a
// background task wakes the session ("<task-notification>\n<task-id>…"; shape
// observed in real v2.x transcripts). It is machinery, not the user speaking.
const taskNotificationPrefix = "<task-notification>"

// UserPromptSubmit records the turn start, the prompt, and the session title so
// the Stop hook can compute elapsed time and label the notification without
// re-parsing the transcript.
//
// A background-task wake ALSO arrives here, as a synthetic UserPromptSubmit.
// Saving that would clobber the real turn's prompt/start — state a withheld
// Stop deliberately preserved for the task-woken completion — so the eventual
// notification would show "<task-notification>…" noise and an elapsed time
// counted from the wake rather than the prompt (usually under the completion
// threshold, silencing it outright). Skip it: with preserved state the turn
// keeps its real label, and with none the transcript correctStart rescue in
// Stop still applies.
func UserPromptSubmit(in *cchooks.UserPromptSubmit) {
	if strings.HasPrefix(strings.TrimSpace(in.Prompt), taskNotificationPrefix) {
		return
	}
	// A failed Save means this turn loses its prompt/start and a threshold-gated
	// Stop later goes silent. That is the project's "pings silently stopped"
	// failure mode, so it must be diagnosable: log under AGENTDONE_DEBUG like the
	// other failure paths (panic, delivery, invalid webhook). state.ensureDir
	// rejects a squatted/broken state dir, which is exactly what surfaces here.
	if err := state.Save(in.SessionID, in.PromptID, state.Turn{
		StartEpoch:   state.Now(),
		Prompt:       in.Prompt,
		SessionTitle: in.SessionTitle,
	}); err != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: turn state not saved: %v\n", err)
	}
}
