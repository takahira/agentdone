package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/slack"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// PreToolUse handles AskUserQuestion / ExitPlanMode, which are confirmation
// waits that surface on neither Stop nor Notification. Other tools are ignored
// (settings.json also scopes this via a matcher, but we double-check).
func PreToolUse(in *cchooks.PreToolUse) {
	if in.ToolName != "AskUserQuestion" && in.ToolName != "ExitPlanMode" {
		return
	}
	turn, _ := state.Peek(in.SessionID)
	if turn.SessionTitle == "" {
		turn.SessionTitle = transcript.LatestTitle(in.TranscriptPath)
	}

	m := activeMessages()
	var b strings.Builder
	b.WriteString(strings.Replace(m.waitingChoice, "{subject}", askSubject(in.ToolName), 1) + "\n")
	appendContext(&b, m, turn, in.Cwd)
	if asked := extractAsked(in.ToolName, in.ToolInput); asked != "" {
		fmt.Fprintf(&b, "%s", m.line(m.asked, truncate(oneLine(asked), maxFieldRunes)))
	}
	_ = slack.Post(config.WebhookURL(), strings.TrimRight(b.String(), "\n"))
}

func askSubject(toolName string) string {
	m := activeMessages()
	if toolName == "ExitPlanMode" {
		return m.planApproval
	}
	return m.choiceQ
}

func extractAsked(toolName string, input json.RawMessage) string {
	switch toolName {
	case "ExitPlanMode":
		var p struct {
			Plan string `json:"plan"`
		}
		debugUnmarshal(toolName, json.Unmarshal(input, &p))
		return p.Plan
	case "AskUserQuestion":
		var q struct {
			Questions []struct {
				Question string `json:"question"`
			} `json:"questions"`
		}
		debugUnmarshal(toolName, json.Unmarshal(input, &q))
		var parts []string
		for _, x := range q.Questions {
			if x.Question != "" {
				parts = append(parts, x.Question)
			}
		}
		return strings.Join(parts, " / ")
	}
	return ""
}

// debugUnmarshal surfaces a tool-input parse failure under AGENTDONE_DEBUG. The
// ping still fires (with an empty question/plan field); this just makes the
// "why is the question text blank?" case diagnosable if a future Claude Code
// version changes the ExitPlanMode / AskUserQuestion input schema.
func debugUnmarshal(toolName string, err error) {
	if err != nil && os.Getenv("AGENTDONE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentdone: %s tool input unparseable: %v\n", toolName, err)
	}
}
