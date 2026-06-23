package handler

import (
	"fmt"
	"os"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// UserPromptSubmit records the turn start, the prompt, and the session title so
// the Stop hook can compute elapsed time and label the notification without
// re-parsing the transcript.
func UserPromptSubmit(in *cchooks.UserPromptSubmit) {
	// A failed Save means this turn loses its prompt/start and a threshold-gated
	// Stop later goes silent. That is the project's "pings silently stopped"
	// failure mode, so it must be diagnosable: log under AGENTDONE_DEBUG like the
	// other failure paths (panic, delivery, invalid webhook). state.ensureDir
	// rejects a squatted/broken state dir, which is exactly what surfaces here.
	if err := state.Save(in.SessionID, state.Turn{
		StartEpoch:   state.Now(),
		Prompt:       in.Prompt,
		SessionTitle: in.SessionTitle,
	}); err != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: turn state not saved: %v\n", err)
	}
}
