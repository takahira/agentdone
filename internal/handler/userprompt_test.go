package handler

import (
	"testing"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// A background-task wake arrives as a synthetic UserPromptSubmit whose prompt
// is the "<task-notification>…" machinery text. Saving it would clobber the
// real turn's prompt/start that a withheld Stop deliberately preserved: the
// eventual completion would be labeled with the noise and timed from the wake.
func TestUserPromptSubmitIgnoresTaskNotificationWake(t *testing.T) {
	UserPromptSubmit(&cchooks.UserPromptSubmit{
		Common:       cchooks.Common{HookEventName: cchooks.EventUserPromptSubmit, SessionID: "wake1"},
		Prompt:       "build 100 games",
		SessionTitle: "T",
	})
	orig, ok := state.Peek("wake1", "")
	if !ok || orig.Prompt != "build 100 games" {
		t.Fatalf("real prompt was not saved: %+v ok=%v", orig, ok)
	}

	wake := "<task-notification>\n<task-id>w714rru7b</task-id>\n<tool-use-id>toolu_x</tool-use-id>\n<status>completed</status>"
	UserPromptSubmit(&cchooks.UserPromptSubmit{
		Common: cchooks.Common{HookEventName: cchooks.EventUserPromptSubmit, SessionID: "wake1"},
		Prompt: wake,
	})
	if got, ok := state.Peek("wake1", ""); !ok || got != orig {
		t.Errorf("synthetic wake overwrote the preserved turn state: got %+v ok=%v, want %+v", got, ok, orig)
	}

	// With no prior state a wake must not fabricate one either: a later Stop
	// would otherwise time the turn from the wake and label it with machinery.
	// Leading whitespace still counts as a wake.
	UserPromptSubmit(&cchooks.UserPromptSubmit{
		Common: cchooks.Common{HookEventName: cchooks.EventUserPromptSubmit, SessionID: "wake2"},
		Prompt: " <task-notification>\n<task-id>x</task-id>",
	})
	if got, ok := state.Peek("wake2", ""); ok {
		t.Errorf("synthetic wake fabricated turn state: %+v", got)
	}
}
