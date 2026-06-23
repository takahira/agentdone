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

// Notification handles permission_prompt / idle_prompt and similar attention
// events. The waiting message goes last; session context comes from the turn
// state saved at UserPromptSubmit (peeked, not consumed).
func Notification(in *cchooks.Notification) {
	// auth_success is an informational event, not a "Claude is waiting for you"
	// alert — don't ping for it.
	if in.NotificationType == "auth_success" {
		return
	}
	turn, _ := state.Peek(in.SessionID)
	if turn.SessionTitle == "" {
		turn.SessionTitle = transcript.LatestTitle(in.TranscriptPath)
	}

	m := activeMessages()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", notifHeader(in.NotificationType))
	appendContext(&b, m, turn, in.Cwd)

	msg := in.Message
	switch {
	case in.Title != "" && msg != "":
		msg = in.Title + " — " + msg
	case in.Title != "":
		msg = in.Title
	}
	if msg != "" {
		fmt.Fprintf(&b, "%s", m.line(m.content, truncate(oneLine(msg), maxFieldRunes)))
	}
	_ = slack.Post(config.WebhookURL(), strings.TrimRight(b.String(), "\n"))
}

func notifHeader(notifType string) string {
	m := activeMessages()
	switch notifType {
	case "permission_prompt":
		return m.permission
	case "idle_prompt":
		return m.idle
	default:
		return m.attention
	}
}
