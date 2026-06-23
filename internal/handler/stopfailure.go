package handler

import (
	"fmt"
	"strings"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// StopFailure handles a turn that ended on an API error (rate limit, overload,
// auth, …). Unlike Stop it always notifies — an error matters regardless of how
// long the turn ran — and is never withheld for background work. The turn is
// over, so the saved state is consumed.
func StopFailure(in *cchooks.StopFailure) {
	turn, _ := state.Peek(in.SessionID)
	state.DeleteIf(in.SessionID, turn) // async-safe: never delete a NEWER turn's state

	if turn.SessionTitle == "" {
		// stdin session_title is unreliable; fall back to ai-title. Only the title
		// is needed here, so skip the full Aggregate (token/start work).
		turn.SessionTitle = transcript.LatestTitle(in.TranscriptPath)
	}

	m := activeMessages()
	var b strings.Builder
	b.WriteString(m.errorEnd + "\n")
	appendContext(&b, m, turn, in.Cwd)
	if e := in.ErrorText(); e != "" {
		fmt.Fprintf(&b, "%s", m.line(m.errLabel, truncate(oneLine(e), maxFieldRunes)))
	}
	_ = slack.Post(config.WebhookURL(), strings.TrimRight(b.String(), "\n"))
}
