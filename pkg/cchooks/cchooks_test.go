package cchooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// realStopPayload is the actual Stop hook stdin captured from claude-code
// v2.1.156 (2026-06-07), trimmed to the fields under test.
const realStopPayload = `{
  "hook_event_name": "Stop",
  "session_id": "abc",
  "transcript_path": "/tmp/t.jsonl",
  "cwd": "/tmp",
  "permission_mode": "auto",
  "stop_hook_active": false,
  "last_assistant_message": "仕込み完了です。",
  "background_tasks": [
    {"id": "b3e0e16vg", "type": "shell", "status": "running",
     "description": "Background task", "command": "sleep 30"}
  ],
  "session_crons": []
}`

func TestDecodeRealStop(t *testing.T) {
	ev, err := Decode([]byte(realStopPayload))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := ev.(*Stop)
	if !ok {
		t.Fatalf("decoded to %T, want *Stop", ev)
	}
	if s.SessionID != "abc" || s.PermissionMode != "auto" {
		t.Errorf("base fields not decoded: %+v", s.Common)
	}
	if !strings.Contains(s.LastAssistantMessage, "仕込み完了") {
		t.Errorf("last_assistant_message = %q", s.LastAssistantMessage)
	}
	if len(s.BackgroundTasks) != 1 {
		t.Fatalf("background_tasks len = %d, want 1", len(s.BackgroundTasks))
	}
	bt := s.BackgroundTasks[0]
	if bt.ID != "b3e0e16vg" || bt.Type != "shell" || bt.Status != "running" {
		t.Errorf("background task mismatch: %+v", bt)
	}
	if !bt.Running() {
		t.Error("Running() = false, want true for status=running")
	}
}

// TestStopFailureErrorText checks ErrorText tolerates either payload shape
// (docs: error_type/error_message; binary inspection: error/error_details).
func TestStopFailureErrorText(t *testing.T) {
	cases := []struct {
		name string
		in   StopFailure
		want string
	}{
		{"type+message", StopFailure{ErrorType: "rate_limit", ErrorMessage: "slow down"}, "rate_limit: slow down"},
		{"message only", StopFailure{ErrorMessage: "boom"}, "boom"},
		{"details only", StopFailure{ErrorDetails: "details"}, "details"},
		{"type only", StopFailure{ErrorType: "overloaded"}, "overloaded"},
		// a JSON-string error is unquoted — `"rate_limit"` with quotes
		// must never reach a notification — and joined with error_details.
		{"string error unquoted", StopFailure{Error: json.RawMessage(`"rate_limit"`)}, "rate_limit"},
		{"string error + details", StopFailure{Error: json.RawMessage(`"rate_limit"`), ErrorDetails: "too many requests"}, "rate_limit: too many requests"},
		{"non-string error raw", StopFailure{Error: json.RawMessage(`{"code":1}`)}, `{"code":1}`},
		{"empty", StopFailure{}, ""},
	}
	for _, c := range cases {
		if got := c.in.ErrorText(); got != c.want {
			t.Errorf("%s: ErrorText() = %q, want %q", c.name, got, c.want)
		}
	}
}

// fields confirmed against the v2.1.168 binary schema must decode —
// session_crons element shape, SubagentStop's task arrays, CwdChanged's
// explicit old/new, and the expansion/compaction details.
func TestDecodeV21168Fields(t *testing.T) {
	stop := `{"hook_event_name":"Stop","session_id":"s",
		"session_crons":[{"id":"c1","schedule":"0 9 * * 1-5","recurring":true,"prompt":"check CI"}]}`
	ev, err := Decode([]byte(stop))
	if err != nil {
		t.Fatal(err)
	}
	s := ev.(*Stop)
	if len(s.SessionCrons) != 1 || s.SessionCrons[0].Schedule != "0 9 * * 1-5" || !s.SessionCrons[0].Recurring {
		t.Errorf("SessionCrons = %+v", s.SessionCrons)
	}

	sub := `{"hook_event_name":"SubagentStop","session_id":"s",
		"background_tasks":[{"id":"t1","type":"shell","status":"running"}],
		"session_crons":[{"id":"c1","schedule":"* * * * *","recurring":false,"prompt":"p"}]}`
	ev, err = Decode([]byte(sub))
	if err != nil {
		t.Fatal(err)
	}
	ss := ev.(*SubagentStop)
	if len(ss.BackgroundTasks) != 1 || !ss.BackgroundTasks[0].Running() || len(ss.SessionCrons) != 1 {
		t.Errorf("SubagentStop arrays = %+v / %+v", ss.BackgroundTasks, ss.SessionCrons)
	}

	cwd := `{"hook_event_name":"CwdChanged","old_cwd":"/a","new_cwd":"/b"}`
	ev, err = Decode([]byte(cwd))
	if err != nil {
		t.Fatal(err)
	}
	if c := ev.(*CwdChanged); c.OldCwd != "/a" || c.NewCwd != "/b" {
		t.Errorf("CwdChanged = %+v", c)
	}

	exp := `{"hook_event_name":"UserPromptExpansion","expansion_type":"slash_command","command_name":"review","command_args":"--fix","prompt":"do it"}`
	ev, err = Decode([]byte(exp))
	if err != nil {
		t.Fatal(err)
	}
	if e := ev.(*UserPromptExpansion); e.CommandName != "review" || e.CommandArgs != "--fix" || e.Prompt != "do it" {
		t.Errorf("UserPromptExpansion = %+v", e)
	}
}

func TestDecodeDispatch(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`:     "*cchooks.Notification",
		`{"hook_event_name":"UserPromptSubmit","prompt":"hi","session_title":"t"}`: "*cchooks.UserPromptSubmit",
		`{"hook_event_name":"TaskCompleted","task_id":"x"}`:                        "*cchooks.TaskCompleted",
		`{"hook_event_name":"StopFailure","error_type":"rate_limit"}`:              "*cchooks.StopFailure",
		`{"hook_event_name":"SomethingNew"}`:                                       "*cchooks.Common",
	}
	for payload, want := range cases {
		ev, err := Decode([]byte(payload))
		if err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if got := typeName(ev); got != want {
			t.Errorf("%s: decoded to %s, want %s", payload, got, want)
		}
	}
}

// a payload with no hook_event_name (null, {}, missing field) is
// malformed and must error — distinct from a NAMED but unknown event, which
// decodes into *Common (covered above).
func TestDecodeRejectsNameless(t *testing.T) {
	for _, payload := range []string{`null`, `{}`, `{"session_id":"s1"}`} {
		if _, err := Decode([]byte(payload)); err == nil {
			t.Errorf("Decode(%q) = nil error, want an error for a payload with no hook_event_name", payload)
		}
	}
}

// TestEventConstantsHaveConcreteType guards the parity between the EventXxx
// constants and newEvent's switch: every known event name must decode into its
// own concrete type, never the *Common forward-compat fallback. Adding an
// EventXxx constant (e.g. when bumping VerifiedClaudeCodeVersion) without the
// matching newEvent case fails here. allEvents is the single source of truth —
// keep it in lockstep with the const block and the switch.
func TestEventConstantsHaveConcreteType(t *testing.T) {
	allEvents := []string{
		EventStop, EventStopFailure, EventSubagentStop, EventSubagentStart,
		EventNotification, EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
		EventPostToolBatch, EventUserPromptSubmit, EventUserPromptExpansion,
		EventSessionStart, EventSessionEnd, EventPreCompact, EventPostCompact,
		EventTaskCreated, EventTaskCompleted, EventTeammateIdle, EventPermissionRequest,
		EventPermissionDenied, EventElicitation, EventElicitationResult, EventMessageDisplay,
		EventFileChanged, EventConfigChange, EventCwdChanged, EventInstructionsLoaded,
		EventSetup, EventWorktreeCreate, EventWorktreeRemove,
	}
	// A tripwire so a new constant added without extending allEvents is noticed.
	if len(allEvents) != 30 {
		t.Fatalf("allEvents has %d entries, want 30 — update it in lockstep with the EventXxx const block", len(allEvents))
	}
	for _, name := range allEvents {
		if _, isCommon := newEvent(name).(*Common); isCommon {
			t.Errorf("newEvent(%q) returned *Common — the EventXxx constant has no matching case in newEvent's switch", name)
		}
	}
}

func typeName(ev Event) string {
	switch ev.(type) {
	case *Notification:
		return "*cchooks.Notification"
	case *UserPromptSubmit:
		return "*cchooks.UserPromptSubmit"
	case *TaskCompleted:
		return "*cchooks.TaskCompleted"
	case *StopFailure:
		return "*cchooks.StopFailure"
	case *Common:
		return "*cchooks.Common"
	default:
		return "?"
	}
}
