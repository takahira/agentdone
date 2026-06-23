package cchooks

import "encoding/json"

// --- completion / stop ---

// Stop fires when the main agent finishes a turn.
type Stop struct {
	Common
	StopHookActive       bool             `json:"stop_hook_active,omitempty"`
	LastAssistantMessage string           `json:"last_assistant_message,omitempty"`
	BackgroundTasks      []BackgroundTask `json:"background_tasks,omitempty"`
	SessionCrons         []SessionCron    `json:"session_crons,omitempty"`
}

// StopFailure fires when a turn ends with an API error (rate limit, overload,
// auth failure, …) rather than completing. It is a separate hook registration
// from Stop: registering only Stop does not surface error-end turns.
//
// The v2.1.168 binary schema is error (a required enum string:
// authentication_failed | oauth_org_not_allowed | billing_error | rate_limit |
// overloaded | invalid_request | model_not_found | server_error | unknown |
// max_output_tokens) plus optional error_details and last_assistant_message.
// error_type/error_message exist only in the docs (2026-06-05), not in the
// binary; both shapes are carried so ErrorText works against either. Error
// stays a RawMessage on purpose: typing it as string would make the whole
// event fail to decode if the shape ever shifts again.
type StopFailure struct {
	Common
	ErrorType            string          `json:"error_type,omitempty"`    // docs-only shape
	ErrorMessage         string          `json:"error_message,omitempty"` // docs-only shape
	Error                json.RawMessage `json:"error,omitempty"`         // binary shape: enum string (see above)
	ErrorDetails         string          `json:"error_details,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
}

// ErrorText returns the most descriptive human-readable error available,
// tolerating either payload shape (see the type comment). Returns "" if none.
func (s StopFailure) ErrorText() string {
	switch {
	case s.ErrorMessage != "" && s.ErrorType != "":
		return s.ErrorType + ": " + s.ErrorMessage
	case s.ErrorMessage != "":
		return s.ErrorMessage
	}
	// The binary-inspection shape: error is an enum-ish JSON string
	// ("rate_limit"), error_details the human text. Unquote the former rather
	// than leak raw JSON (`"rate_limit"`) into a notification, and join both
	// when present.
	errStr := ""
	if len(s.Error) > 0 {
		if json.Unmarshal(s.Error, &errStr) != nil {
			errStr = string(s.Error) // not a string: show the raw value as-is
		}
	}
	switch {
	case errStr != "" && s.ErrorDetails != "":
		return errStr + ": " + s.ErrorDetails
	case s.ErrorDetails != "":
		return s.ErrorDetails
	case errStr != "":
		return errStr
	case s.ErrorType != "":
		return s.ErrorType
	}
	return ""
}

// SubagentStop fires when a subagent finishes. Like Stop it carries the
// session's in-flight background tasks and scheduled crons (v2.1.168).
type SubagentStop struct {
	Common
	StopHookActive       bool             `json:"stop_hook_active,omitempty"`
	AgentTranscriptPath  string           `json:"agent_transcript_path,omitempty"`
	LastAssistantMessage string           `json:"last_assistant_message,omitempty"`
	BackgroundTasks      []BackgroundTask `json:"background_tasks,omitempty"`
	SessionCrons         []SessionCron    `json:"session_crons,omitempty"`
}

// SubagentStart fires when a subagent starts (fields beyond Common: agent_id /
// agent_type, both on Common).
type SubagentStart struct {
	Common
}

// --- attention / input wait ---

// Notification fires for permission prompts, idle prompts, auth success, and
// MCP elicitation dialogs.
//
// Does NOT fire in the VS Code extension (anthropics/claude-code#8985); it fires
// in a terminal session. A terminal idle_prompt payload (verified 2026-06-07) is
// lean: message + notification_type only — title, permission_mode and effort are
// absent (message = "Claude is waiting for your input").
type Notification struct {
	Common
	Message          string `json:"message,omitempty"`
	Title            string `json:"title,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
}

// PermissionRequest fires before an interactive permission prompt.
type PermissionRequest struct {
	Common
	ToolName              string          `json:"tool_name,omitempty"`
	ToolInput             json.RawMessage `json:"tool_input,omitempty"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
}

// PermissionDenied fires when a tool call is auto-denied (no interactive prompt).
type PermissionDenied struct {
	Common
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// Elicitation fires when an MCP server requests structured input.
type Elicitation struct {
	Common
	MCPServerName   string          `json:"mcp_server_name,omitempty"`
	Message         string          `json:"message,omitempty"`
	Mode            string          `json:"mode,omitempty"` // form | url
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitation_id,omitempty"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
}

// ElicitationResult fires when an MCP elicitation is resolved.
type ElicitationResult struct {
	Common
	MCPServerName string          `json:"mcp_server_name,omitempty"`
	ElicitationID string          `json:"elicitation_id,omitempty"`
	Mode          string          `json:"mode,omitempty"`
	Action        string          `json:"action,omitempty"` // accept | decline | cancel
	Content       json.RawMessage `json:"content,omitempty"`
}

// --- tools ---

// PreToolUse fires before a tool call.
type PreToolUse struct {
	Common
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
}

// PostToolUse fires after a tool call resolves.
type PostToolUse struct {
	Common
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	DurationMS   int64           `json:"duration_ms,omitempty"`
}

// PostToolUseFailure fires when a tool call fails.
type PostToolUseFailure struct {
	Common
	ToolName    string          `json:"tool_name,omitempty"`
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID   string          `json:"tool_use_id,omitempty"`
	Error       string          `json:"error,omitempty"`
	IsInterrupt bool            `json:"is_interrupt,omitempty"`
	DurationMS  int64           `json:"duration_ms,omitempty"`
}

// PostToolBatch fires once after every tool call in a batch resolves.
type PostToolBatch struct {
	Common
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// --- prompts / session ---

// UserPromptSubmit fires when the user submits a prompt.
//
// Note: session_title is the *manual* session title (currentSessionTitle),
// usually unset and therefore omitted from the JSON. It is distinct from the
// AI-generated title shown in the UI (currentSessionAiTitle), which no hook
// field carries — read that from the transcript's ai-title line instead.
// (Verified against the binary and a real payload, 2026-06-07.)
type UserPromptSubmit struct {
	Common
	Prompt       string `json:"prompt,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
}

// UserPromptExpansion fires when a prompt expansion (slash command, MCP, …) runs.
type UserPromptExpansion struct {
	Common
	ExpansionType string `json:"expansion_type,omitempty"` // slash_command | mcp_prompt
	CommandName   string `json:"command_name,omitempty"`
	CommandArgs   string `json:"command_args,omitempty"`
	CommandSource string `json:"command_source,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
}

// SessionStart fires when a session starts.
type SessionStart struct {
	Common
	Source       string `json:"source,omitempty"` // startup | resume | clear | compact
	Model        string `json:"model,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
}

// SessionEnd fires when a session ends.
type SessionEnd struct {
	Common
	Reason string `json:"reason,omitempty"`
}

// --- tasks / teammates ---

// TaskCreated fires when a background task / teammate task is created.
type TaskCreated struct {
	Common
	TaskID          string `json:"task_id,omitempty"`
	TaskSubject     string `json:"task_subject,omitempty"`
	TaskDescription string `json:"task_description,omitempty"`
	TeammateName    string `json:"teammate_name,omitempty"`
	TeamName        string `json:"team_name,omitempty"`
}

// TaskCompleted fires when a background task / teammate task completes.
type TaskCompleted struct {
	Common
	TaskID          string `json:"task_id,omitempty"`
	TaskSubject     string `json:"task_subject,omitempty"`
	TaskDescription string `json:"task_description,omitempty"`
	TeammateName    string `json:"teammate_name,omitempty"`
	TeamName        string `json:"team_name,omitempty"`
}

// TeammateIdle fires when a teammate agent goes idle.
type TeammateIdle struct {
	Common
	TeammateName string `json:"teammate_name,omitempty"`
	TeamName     string `json:"team_name,omitempty"`
}

// --- compaction ---

// PreCompact fires before transcript compaction.
type PreCompact struct {
	Common
	Trigger            string  `json:"trigger,omitempty"` // manual | auto
	CustomInstructions *string `json:"custom_instructions,omitempty"`
}

// PostCompact fires after transcript compaction.
type PostCompact struct {
	Common
	Trigger        string `json:"trigger,omitempty"` // manual | auto
	CompactSummary string `json:"compact_summary,omitempty"`
}

// --- environment / infra ---

// MessageDisplay fires when a message is displayed (streamed in deltas: final
// marks the last delta of a message).
type MessageDisplay struct {
	Common
	TurnID    string `json:"turn_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Index     int    `json:"index,omitempty"`
	Final     bool   `json:"final,omitempty"`
	Delta     string `json:"delta,omitempty"`
}

// FileChanged fires when a watched file changes.
type FileChanged struct {
	Common
	FilePath string `json:"file_path,omitempty"`
	Event    string `json:"event,omitempty"` // change | add | unlink
}

// ConfigChange fires when configuration changes.
type ConfigChange struct {
	Common
	Source   string `json:"source,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

// CwdChanged fires when the working directory changes. The old and new
// directories are explicit fields (v2.1.168) — they are NOT carried on Common.
type CwdChanged struct {
	Common
	OldCwd string `json:"old_cwd,omitempty"`
	NewCwd string `json:"new_cwd,omitempty"`
}

// InstructionsLoaded fires when an instructions/memory file is loaded.
type InstructionsLoaded struct {
	Common
	FilePath        string   `json:"file_path,omitempty"`
	MemoryType      string   `json:"memory_type,omitempty"`
	LoadReason      string   `json:"load_reason,omitempty"`
	Globs           []string `json:"globs,omitempty"`
	TriggerFilePath string   `json:"trigger_file_path,omitempty"`
	ParentFilePath  string   `json:"parent_file_path,omitempty"`
}

// Setup fires on init / maintenance.
type Setup struct {
	Common
	Trigger string `json:"trigger,omitempty"` // init | maintenance
}

// WorktreeCreate fires when a worktree is created.
type WorktreeCreate struct {
	Common
	Name string `json:"name,omitempty"`
}

// WorktreeRemove fires when a worktree is removed.
type WorktreeRemove struct {
	Common
	WorktreePath string `json:"worktree_path,omitempty"`
}
