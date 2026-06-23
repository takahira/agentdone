// Package cchooks provides Go types for Claude Code hook stdin payloads.
//
// The schema was reverse-engineered from the claude-code binary — first
// v2.1.156 (2026-06-07), re-verified and extended against the v2.1.168 zod
// definitions (2026-06-10) — and covers every hook event. Decode a payload and
// type-switch on the concrete event:
//
//	ev, err := cchooks.Parse(os.Stdin)
//	switch e := ev.(type) {
//	case *cchooks.Stop:         // e.BackgroundTasks, e.LastAssistantMessage, ...
//	case *cchooks.Notification: // e.NotificationType, e.Title, e.Message
//	}
//
// Field shapes mirror the CLI. A few fields whose exact shape was not confirmed
// from the binary are typed as json.RawMessage and noted as such.
package cchooks

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// VerifiedClaudeCodeVersion is the claude-code release this package's
// reverse-engineered schema was last verified against. The hook payload schema
// is not a published API: when notifications misbehave after a Claude Code
// update, compare against this version first (`agentdone doctor` prints it).
const VerifiedClaudeCodeVersion = "v2.1.168"

// Event is implemented by every hook event type via the embedded Common.
type Event interface {
	EventName() string
}

// Common holds the fields present on (essentially) every hook event.
type Common struct {
	HookEventName  string  `json:"hook_event_name"`
	SessionID      string  `json:"session_id"`
	TranscriptPath string  `json:"transcript_path"`
	Cwd            string  `json:"cwd"`
	PermissionMode string  `json:"permission_mode,omitempty"`
	AgentID        string  `json:"agent_id,omitempty"`
	AgentType      string  `json:"agent_type,omitempty"`
	Effort         *Effort `json:"effort,omitempty"` // present on most events (Stop, PreToolUse, …); absent on UserPromptSubmit

	// Raw is the exact JSON this event was decoded from, so consumers can recover
	// fields this library does not model — e.g. a future event type that decodes
	// into Common, or a new field on a known event. Populated by Decode/Parse;
	// never marshaled back out.
	Raw json.RawMessage `json:"-"`
}

// Effort is the thinking-effort setting, e.g. {"level":"xhigh"} (verified
// against a real PreToolUse/Stop payload, 2026-06-07).
type Effort struct {
	Level string `json:"level,omitempty"`
}

// EventName returns hook_event_name, satisfying Event.
func (c Common) EventName() string { return c.HookEventName }

// setRaw stores the original payload on the embedded Common. Decode calls it so
// every event — including an unknown one decoded into *Common — keeps its raw
// JSON for consumers to inspect.
func (c *Common) setRaw(b json.RawMessage) { c.Raw = b }

// BackgroundTask is one entry of the Stop/SubagentStop background_tasks array:
// in-flight background work registered in the session. The array is empty when
// nothing is in flight, which lets a hook tell "session is done" from "session
// is paused waiting for background work to wake it".
//
// The optional fields are populated by task type: command for shell tasks,
// agent_type for subagents, server/tool for MCP monitors, name for workflows.
// Note: the hook payload exposes no flag for whether a task was manually
// (Ctrl+B) or auto-backgrounded — that lives in a separate internal schema and
// is not available here.
type BackgroundTask struct {
	ID string `json:"id"`
	// Type is an OPEN set: shell | subagent | monitor | workflow are the common
	// values, but newer builds also emit e.g. MCP-task / teammate / dream /
	// cloud-session variants. Treat unknown values as just another task kind.
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`    // shell tasks
	AgentType   string `json:"agent_type,omitempty"` // subagent tasks
	Server      string `json:"server,omitempty"`     // MCP monitor
	Tool        string `json:"tool,omitempty"`       // MCP monitor
	Name        string `json:"name,omitempty"`       // workflow tasks
}

// Running reports whether the task is still in flight. The hook lists a task
// only while it is registered and exposes no enum of terminal states, so we
// treat any status as in-flight EXCEPT a known terminal one. A whitelist would
// mistake an unrecognized or new in-flight status (queued, starting, …) for
// "done" and leak the premature completion ping this tool exists to withhold.
func (t BackgroundTask) Running() bool {
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "completed", "complete", "done", "succeeded", "success", "finished",
		"failed", "failure", "error", "errored", "cancelled", "canceled",
		"stopped", "killed", "timeout", "timed_out", "aborted":
		return false
	}
	return true
}

// SessionCron is one entry of the Stop/SubagentStop session_crons array:
// a session-scoped scheduled task (CronCreate, ScheduleWakeup, /loop) that will
// wake the session later. The array is empty when none are scheduled.
// (Shape confirmed against the v2.1.168 binary schema.)
type SessionCron struct {
	ID        string `json:"id"`
	Schedule  string `json:"schedule"`  // cron expression, e.g. "0 9 * * 1-5"
	Recurring bool   `json:"recurring"` // false for one-shot wakeups
	Prompt    string `json:"prompt"`    // prompt submitted when it fires (capped at 1000 chars)
}

// ToolCall is one entry of the PostToolBatch tool_calls array. The field set is
// inferred from PostToolUse; unknown extras are ignored.
type ToolCall struct {
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
}

// Hook event name constants (all events emitted by claude-code as of
// VerifiedClaudeCodeVersion).
const (
	EventStop                = "Stop"
	EventStopFailure         = "StopFailure"
	EventSubagentStop        = "SubagentStop"
	EventSubagentStart       = "SubagentStart"
	EventNotification        = "Notification"
	EventPreToolUse          = "PreToolUse"
	EventPostToolUse         = "PostToolUse"
	EventPostToolUseFailure  = "PostToolUseFailure"
	EventPostToolBatch       = "PostToolBatch"
	EventUserPromptSubmit    = "UserPromptSubmit"
	EventUserPromptExpansion = "UserPromptExpansion"
	EventSessionStart        = "SessionStart"
	EventSessionEnd          = "SessionEnd"
	EventPreCompact          = "PreCompact"
	EventPostCompact         = "PostCompact"
	EventTaskCreated         = "TaskCreated"
	EventTaskCompleted       = "TaskCompleted"
	EventTeammateIdle        = "TeammateIdle"
	EventPermissionRequest   = "PermissionRequest"
	EventPermissionDenied    = "PermissionDenied"
	EventElicitation         = "Elicitation"
	EventElicitationResult   = "ElicitationResult"
	EventMessageDisplay      = "MessageDisplay"
	EventFileChanged         = "FileChanged"
	EventConfigChange        = "ConfigChange"
	EventCwdChanged          = "CwdChanged"
	EventInstructionsLoaded  = "InstructionsLoaded"
	EventSetup               = "Setup"
	EventWorktreeCreate      = "WorktreeCreate"
	EventWorktreeRemove      = "WorktreeRemove"
)

// maxPayloadBytes caps how much hook stdin Parse will buffer. Real payloads are
// kilobytes; this only guards a pathological or malicious input so the process
// (which must never hang Claude Code) can't be made to allocate without bound.
const maxPayloadBytes = 8 << 20 // 8 MiB

// Parse reads a hook payload from r (typically os.Stdin) and decodes it into its
// concrete event type.
func Parse(r io.Reader) (Event, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPayloadBytes {
		return nil, fmt.Errorf("cchooks: hook payload exceeds %d bytes", maxPayloadBytes)
	}
	return Decode(data)
}

// Decode decodes a hook payload into the concrete event type for its
// hook_event_name. Unknown events decode into *Common so base fields remain
// accessible.
func Decode(data []byte) (Event, error) {
	var probe struct {
		HookEventName string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	// An unknown event still names itself (decoded into *Common below); a payload
	// with no hook_event_name (e.g. null or {}) is malformed, not a future event.
	if probe.HookEventName == "" {
		return nil, fmt.Errorf("cchooks: payload has no hook_event_name")
	}
	ev := newEvent(probe.HookEventName)
	if err := json.Unmarshal(data, ev); err != nil {
		return nil, err
	}
	if rs, ok := ev.(interface{ setRaw(json.RawMessage) }); ok {
		rs.setRaw(append(json.RawMessage(nil), data...))
	}
	return ev, nil
}

func newEvent(name string) Event {
	switch name {
	case EventStop:
		return &Stop{}
	case EventStopFailure:
		return &StopFailure{}
	case EventSubagentStop:
		return &SubagentStop{}
	case EventSubagentStart:
		return &SubagentStart{}
	case EventNotification:
		return &Notification{}
	case EventPreToolUse:
		return &PreToolUse{}
	case EventPostToolUse:
		return &PostToolUse{}
	case EventPostToolUseFailure:
		return &PostToolUseFailure{}
	case EventPostToolBatch:
		return &PostToolBatch{}
	case EventUserPromptSubmit:
		return &UserPromptSubmit{}
	case EventUserPromptExpansion:
		return &UserPromptExpansion{}
	case EventSessionStart:
		return &SessionStart{}
	case EventSessionEnd:
		return &SessionEnd{}
	case EventPreCompact:
		return &PreCompact{}
	case EventPostCompact:
		return &PostCompact{}
	case EventTaskCreated:
		return &TaskCreated{}
	case EventTaskCompleted:
		return &TaskCompleted{}
	case EventTeammateIdle:
		return &TeammateIdle{}
	case EventPermissionRequest:
		return &PermissionRequest{}
	case EventPermissionDenied:
		return &PermissionDenied{}
	case EventElicitation:
		return &Elicitation{}
	case EventElicitationResult:
		return &ElicitationResult{}
	case EventMessageDisplay:
		return &MessageDisplay{}
	case EventFileChanged:
		return &FileChanged{}
	case EventConfigChange:
		return &ConfigChange{}
	case EventCwdChanged:
		return &CwdChanged{}
	case EventInstructionsLoaded:
		return &InstructionsLoaded{}
	case EventSetup:
		return &Setup{}
	case EventWorktreeCreate:
		return &WorktreeCreate{}
	case EventWorktreeRemove:
		return &WorktreeRemove{}
	default:
		return &Common{}
	}
}
