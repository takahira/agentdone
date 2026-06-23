package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/takahira/agentdone/pkg/cchooks"
)

func collectServer(t *testing.T) chan string {
	t.Helper()
	bodies := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SLACK_WEBHOOK_URL", srv.URL)
	return bodies
}

func TestExtractAsked(t *testing.T) {
	if got := extractAsked("ExitPlanMode", json.RawMessage(`{"plan":"do the thing"}`)); got != "do the thing" {
		t.Errorf("ExitPlanMode plan = %q, want %q", got, "do the thing")
	}
	if got := extractAsked("AskUserQuestion", json.RawMessage(`{"questions":[{"question":"q1"},{"question":"q2"}]}`)); got != "q1 / q2" {
		t.Errorf("AskUserQuestion = %q, want %q", got, "q1 / q2")
	}
	if got := extractAsked("Bash", json.RawMessage(`{"command":"ls"}`)); got != "" {
		t.Errorf("non-ask tool = %q, want empty", got)
	}
	if got := extractAsked("AskUserQuestion", json.RawMessage(`not json`)); got != "" {
		t.Errorf("malformed input = %q, want empty", got)
	}
}

func TestPreToolUse(t *testing.T) {
	bodies := collectServer(t)

	PreToolUse(&cchooks.PreToolUse{
		Common:    cchooks.Common{HookEventName: cchooks.EventPreToolUse, SessionID: "pt1"},
		ToolName:  "AskUserQuestion",
		ToolInput: json.RawMessage(`{"questions":[{"question":"Pick A or B?"}]}`),
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "Pick A or B?") {
			t.Errorf("AskUserQuestion content missing in: %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a PreToolUse notification for AskUserQuestion")
	}

	PreToolUse(&cchooks.PreToolUse{
		Common:    cchooks.Common{HookEventName: cchooks.EventPreToolUse, SessionID: "pt2"},
		ToolName:  "ExitPlanMode",
		ToolInput: json.RawMessage(`{"plan":"Step one then step two."}`),
	})
	select {
	case b := <-bodies:
		if !strings.Contains(b, "Step one") {
			t.Errorf("ExitPlanMode plan missing in: %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a PreToolUse notification for ExitPlanMode")
	}

	// A non-confirmation tool is ignored (defends against an over-broad matcher).
	PreToolUse(&cchooks.PreToolUse{
		Common:    cchooks.Common{HookEventName: cchooks.EventPreToolUse, SessionID: "pt3"},
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
	})
	select {
	case b := <-bodies:
		t.Errorf("Bash PreToolUse should be ignored, got: %s", b)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestNotificationAuthSuccessSkipped(t *testing.T) {
	bodies := collectServer(t)

	// auth_success is informational, not a wait -> no ping.
	Notification(&cchooks.Notification{
		Common:           cchooks.Common{HookEventName: cchooks.EventNotification, SessionID: "n1"},
		NotificationType: "auth_success",
		Message:          "Authenticated",
	})
	select {
	case b := <-bodies:
		t.Errorf("auth_success should not ping, got: %s", b)
	case <-time.After(150 * time.Millisecond):
	}

	// A real wait still pings.
	Notification(&cchooks.Notification{
		Common:           cchooks.Common{HookEventName: cchooks.EventNotification, SessionID: "n2"},
		NotificationType: "permission_prompt",
		Message:          "needs permission",
	})
	select {
	case <-bodies:
	case <-time.After(time.Second):
		t.Fatal("permission_prompt should ping")
	}
}
