package hooks

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// HookEvent enumerates all lifecycle hook event types.
// Mirrors Claude Code's HOOK_EVENTS from agentSdkTypes.
type HookEvent string

const (
	EventPreToolUse         HookEvent = "PreToolUse"
	EventPostToolUse        HookEvent = "PostToolUse"
	EventPostToolUseFailure HookEvent = "PostToolUseFailure"
	EventPermissionDenied   HookEvent = "PermissionDenied"
	EventNotification       HookEvent = "Notification"
	EventUserPromptSubmit   HookEvent = "UserPromptSubmit"
	EventSessionStart       HookEvent = "SessionStart"
	EventSessionEnd         HookEvent = "SessionEnd"
	EventStop               HookEvent = "Stop"
	EventStopFailure        HookEvent = "StopFailure"
	EventSubagentStart      HookEvent = "SubagentStart"
	EventSubagentStop       HookEvent = "SubagentStop"
	EventPreCompact         HookEvent = "PreCompact"
	EventPostCompact        HookEvent = "PostCompact"
	EventPermissionRequest  HookEvent = "PermissionRequest"
	EventSetup              HookEvent = "Setup"
	EventTeammateIdle       HookEvent = "TeammateIdle"
	EventTaskCreated        HookEvent = "TaskCreated"
	EventTaskCompleted      HookEvent = "TaskCompleted"
	EventElicitation        HookEvent = "Elicitation"
	EventElicitationResult  HookEvent = "ElicitationResult"
	EventConfigChange       HookEvent = "ConfigChange"
	EventInstructionsLoaded HookEvent = "InstructionsLoaded"
	EventWorktreeCreate     HookEvent = "WorktreeCreate"
	EventWorktreeRemove     HookEvent = "WorktreeRemove"
	EventCwdChanged         HookEvent = "CwdChanged"
	EventFileChanged        HookEvent = "FileChanged"
)

// AllHookEvents is the canonical list of all hook event types, matching HOOK_EVENTS.
var AllHookEvents = []HookEvent{
	EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
	EventPermissionDenied, EventNotification, EventUserPromptSubmit,
	EventSessionStart, EventSessionEnd, EventStop, EventStopFailure,
	EventSubagentStart, EventSubagentStop, EventPreCompact, EventPostCompact,
	EventPermissionRequest, EventSetup, EventTeammateIdle,
	EventTaskCreated, EventTaskCompleted, EventElicitation, EventElicitationResult,
	EventConfigChange, EventInstructionsLoaded,
	EventWorktreeCreate, EventWorktreeRemove, EventCwdChanged, EventFileChanged,
}

// AlwaysEmittedEvents are emitted regardless of the includeHookEvents option.
// These are low-noise lifecycle events that are backwards-compatible.
var AlwaysEmittedEvents = []HookEvent{EventSessionStart, EventSetup}

// IsValidHookEvent returns true if the given string is a recognized hook event.
func IsValidHookEvent(s string) bool {
	for _, e := range AllHookEvents {
		if string(e) == s {
			return true
		}
	}
	return false
}

// HookEventMetadata describes a hook event type for configuration UI.
type HookEventMetadata struct {
	Summary     string
	Description string
	Matcher     *MatcherMetadata
}

// MatcherMetadata describes the matcher field for a hook event.
type MatcherMetadata struct {
	FieldToMatch string
	Values       []string
}

// GetHookEventMetadata returns metadata for all hook events.
// toolNames provides the list of available tool names for matcher values.
func GetHookEventMetadata(toolNames []string) map[HookEvent]HookEventMetadata {
	return map[HookEvent]HookEventMetadata{
		EventPreToolUse: {
			Summary:     "Before tool execution",
			Description: "Input to command is JSON of tool call arguments.\nExit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to model and block tool call\nOther exit codes - show stderr to user only but continue with tool call",
			Matcher:     &MatcherMetadata{FieldToMatch: "tool_name", Values: toolNames},
		},
		EventPostToolUse: {
			Summary:     "After tool execution",
			Description: "Input to command is JSON with fields \"inputs\" (tool call arguments) and \"response\" (tool call response).\nExit code 0 - stdout shown in transcript mode\nExit code 2 - show stderr to model immediately\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "tool_name", Values: toolNames},
		},
		EventPostToolUseFailure: {
			Summary:     "After tool execution fails",
			Description: "Input to command is JSON with tool_name, tool_input, tool_use_id, error, error_type, is_interrupt, and is_timeout.\nExit code 0 - stdout shown in transcript mode\nExit code 2 - show stderr to model immediately\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "tool_name", Values: toolNames},
		},
		EventPermissionDenied: {
			Summary:     "After auto mode classifier denies a tool call",
			Description: "Input to command is JSON with tool_name, tool_input, tool_use_id, and reason.\nReturn {\"hookSpecificOutput\":{\"hookEventName\":\"PermissionDenied\",\"retry\":true}} to tell the model it may retry.\nExit code 0 - stdout shown in transcript mode\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "tool_name", Values: toolNames},
		},
		EventNotification: {
			Summary:     "When notifications are sent",
			Description: "Input to command is JSON with notification message and type.\nExit code 0 - stdout/stderr not shown\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "notification_type",
				Values:       []string{"permission_prompt", "idle_prompt", "auth_success", "elicitation_dialog", "elicitation_complete", "elicitation_response"},
			},
		},
		EventUserPromptSubmit: {
			Summary:     "When the user submits a prompt",
			Description: "Input to command is JSON with original user prompt text.\nExit code 0 - stdout shown to Claude\nExit code 2 - block processing, erase original prompt, and show stderr to user only\nOther exit codes - show stderr to user only",
		},
		EventSessionStart: {
			Summary:     "When a new session is started",
			Description: "Input to command is JSON with session start source.\nExit code 0 - stdout shown to Claude\nBlocking errors are ignored\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "source",
				Values:       []string{"startup", "resume", "clear", "compact"},
			},
		},
		EventSessionEnd: {
			Summary:     "When a session is ending",
			Description: "Input to command is JSON with session end reason.\nExit code 0 - command completes successfully\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "reason",
				Values:       []string{"clear", "logout", "prompt_input_exit", "other"},
			},
		},
		EventStop: {
			Summary:     "Right before Claude concludes its response",
			Description: "Exit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to model and continue conversation\nOther exit codes - show stderr to user only",
		},
		EventStopFailure: {
			Summary:     "When the turn ends due to an API error",
			Description: "Fires instead of Stop when an API error ended the turn. Fire-and-forget — hook output and exit codes are ignored.",
			Matcher: &MatcherMetadata{
				FieldToMatch: "error",
				Values:       []string{"rate_limit", "authentication_failed", "billing_error", "invalid_request", "server_error", "max_output_tokens", "unknown"},
			},
		},
		EventSubagentStart: {
			Summary:     "When a subagent (Agent tool call) is started",
			Description: "Input to command is JSON with agent_id and agent_type.\nExit code 0 - stdout shown to subagent\nBlocking errors are ignored\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "agent_type", Values: nil},
		},
		EventSubagentStop: {
			Summary:     "Right before a subagent concludes its response",
			Description: "Input to command is JSON with agent_id, agent_type, and agent_transcript_path.\nExit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to subagent and continue having it run\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "agent_type", Values: nil},
		},
		EventPreCompact: {
			Summary:     "Before conversation compaction",
			Description: "Input to command is JSON with compaction details.\nExit code 0 - stdout appended as custom compact instructions\nExit code 2 - block compaction\nOther exit codes - show stderr to user only but continue with compaction",
			Matcher: &MatcherMetadata{
				FieldToMatch: "trigger",
				Values:       []string{"manual", "auto"},
			},
		},
		EventPostCompact: {
			Summary:     "After conversation compaction",
			Description: "Input to command is JSON with compaction details and the summary.\nExit code 0 - stdout shown to user\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "trigger",
				Values:       []string{"manual", "auto"},
			},
		},
		EventPermissionRequest: {
			Summary:     "When a permission dialog is displayed",
			Description: "Input to command is JSON with tool_name, tool_input, and tool_use_id.\nOutput JSON with hookSpecificOutput containing decision to allow or deny.\nExit code 0 - use hook decision if provided\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "tool_name", Values: toolNames},
		},
		EventSetup: {
			Summary:     "Repo setup hooks for init and maintenance",
			Description: "Input to command is JSON with trigger (init or maintenance).\nExit code 0 - stdout shown to Claude\nBlocking errors are ignored\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "trigger",
				Values:       []string{"init", "maintenance"},
			},
		},
		EventTeammateIdle: {
			Summary:     "When a teammate is about to go idle",
			Description: "Input to command is JSON with teammate_name and team_name.\nExit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to teammate and prevent idle\nOther exit codes - show stderr to user only",
		},
		EventTaskCreated: {
			Summary:     "When a task is being created",
			Description: "Input to command is JSON with task_id, task_subject, task_description, teammate_name, and team_name.\nExit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to model and prevent task creation\nOther exit codes - show stderr to user only",
		},
		EventTaskCompleted: {
			Summary:     "When a task is being marked as completed",
			Description: "Input to command is JSON with task_id, task_subject, task_description, teammate_name, and team_name.\nExit code 0 - stdout/stderr not shown\nExit code 2 - show stderr to model and prevent task completion\nOther exit codes - show stderr to user only",
		},
		EventElicitation: {
			Summary:     "When an MCP server requests user input (elicitation)",
			Description: "Input to command is JSON with mcp_server_name, message, and requested_schema.\nOutput JSON with hookSpecificOutput containing action and optional content.\nExit code 0 - use hook response if provided\nExit code 2 - deny the elicitation\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "mcp_server_name", Values: nil},
		},
		EventElicitationResult: {
			Summary:     "After a user responds to an MCP elicitation",
			Description: "Input to command is JSON with mcp_server_name, action, content, mode, and elicitation_id.\nOutput JSON with hookSpecificOutput containing optional action and content to override the response.\nExit code 0 - use hook response if provided\nExit code 2 - block the response\nOther exit codes - show stderr to user only",
			Matcher:     &MatcherMetadata{FieldToMatch: "mcp_server_name", Values: nil},
		},
		EventConfigChange: {
			Summary:     "When configuration files change during a session",
			Description: "Input to command is JSON with source and file_path.\nExit code 0 - allow the change\nExit code 2 - block the change from being applied to the session\nOther exit codes - show stderr to user only",
			Matcher: &MatcherMetadata{
				FieldToMatch: "source",
				Values:       []string{"user_settings", "project_settings", "local_settings", "policy_settings", "skills"},
			},
		},
		EventInstructionsLoaded: {
			Summary:     "When an instruction file is loaded",
			Description: "Input to command is JSON with file_path, memory_type, load_reason, globs, trigger_file_path, and parent_file_path.\nExit code 0 - command completes successfully\nOther exit codes - show stderr to user only\nThis hook is observability-only and does not support blocking.",
			Matcher: &MatcherMetadata{
				FieldToMatch: "load_reason",
				Values:       []string{"session_start", "nested_traversal", "path_glob_match", "include", "compact"},
			},
		},
		EventWorktreeCreate: {
			Summary:     "Create an isolated worktree for VCS-agnostic isolation",
			Description: "Input to command is JSON with name.\nStdout should contain the absolute path to the created worktree directory.\nExit code 0 - worktree created successfully\nOther exit codes - worktree creation failed",
		},
		EventWorktreeRemove: {
			Summary:     "Remove a previously created worktree",
			Description: "Input to command is JSON with worktree_path.\nExit code 0 - worktree removed successfully\nOther exit codes - show stderr to user only",
		},
		EventCwdChanged: {
			Summary:     "After the working directory changes",
			Description: "Input to command is JSON with old_cwd and new_cwd.\nHook output can include hookSpecificOutput.watchPaths.\nExit code 0 - command completes successfully\nOther exit codes - show stderr to user only",
		},
		EventFileChanged: {
			Summary:     "When a watched file changes",
			Description: "Input to command is JSON with file_path and event (change, add, unlink).\nThe matcher field specifies filenames to watch.\nExit code 0 - command completes successfully\nOther exit codes - show stderr to user only",
		},
	}
}

// --- Hook Execution Event System ---
// Broadcasts hook execution events separately from the main message stream.

// HookExecutionEventType discriminates between started, progress, and response events.
type HookExecutionEventType string

const (
	HookEventStarted  HookExecutionEventType = "started"
	HookEventProgress HookExecutionEventType = "progress"
	HookEventResponse HookExecutionEventType = "response"
)

// HookOutcome describes how a hook completed.
type HookOutcome string

const (
	OutcomeSuccess   HookOutcome = "success"
	OutcomeError     HookOutcome = "error"
	OutcomeCancelled HookOutcome = "cancelled"
)

// HookExecutionEvent is emitted during hook execution for observability.
type HookExecutionEvent struct {
	Type      HookExecutionEventType `json:"type"`
	HookID    string                 `json:"hook_id"`
	HookName  string                 `json:"hook_name"`
	HookEvent string                 `json:"hook_event"`
	Stdout    string                 `json:"stdout,omitempty"`
	Stderr    string                 `json:"stderr,omitempty"`
	Output    string                 `json:"output,omitempty"`
	ExitCode  *int                   `json:"exit_code,omitempty"`
	Outcome   HookOutcome            `json:"outcome,omitempty"`
}

// HookEventHandler receives hook execution events.
type HookEventHandler func(HookExecutionEvent)

// HookEventEmitter manages hook event broadcasting with buffering.
type HookEventEmitter struct {
	mu                   sync.Mutex
	handler              HookEventHandler
	pendingEvents        []HookExecutionEvent
	allHookEventsEnabled bool
	logger               *slog.Logger
}

const maxPendingEvents = 100

// NewHookEventEmitter creates a new event emitter.
func NewHookEventEmitter(logger *slog.Logger) *HookEventEmitter {
	return &HookEventEmitter{
		logger: logger,
	}
}

// RegisterHandler sets the event handler. Any pending events are flushed.
func (e *HookEventEmitter) RegisterHandler(h HookEventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = h
	if h != nil && len(e.pendingEvents) > 0 {
		for _, ev := range e.pendingEvents {
			h(ev)
		}
		e.pendingEvents = e.pendingEvents[:0]
	}
}

// SetAllHookEventsEnabled enables emission of all hook event types beyond
// SessionStart and Setup.
func (e *HookEventEmitter) SetAllHookEventsEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allHookEventsEnabled = enabled
}

// Clear resets the emitter state.
func (e *HookEventEmitter) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = nil
	e.pendingEvents = nil
	e.allHookEventsEnabled = false
}

func (e *HookEventEmitter) shouldEmit(hookEvent string) bool {
	for _, ae := range AlwaysEmittedEvents {
		if string(ae) == hookEvent {
			return true
		}
	}
	return e.allHookEventsEnabled && IsValidHookEvent(hookEvent)
}

func (e *HookEventEmitter) emit(ev HookExecutionEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handler != nil {
		e.handler(ev)
	} else {
		e.pendingEvents = append(e.pendingEvents, ev)
		if len(e.pendingEvents) > maxPendingEvents {
			e.pendingEvents = e.pendingEvents[1:]
		}
	}
}

// EmitStarted emits a hook started event.
func (e *HookEventEmitter) EmitStarted(hookID, hookName, hookEvent string) {
	if !e.shouldEmit(hookEvent) {
		return
	}
	e.emit(HookExecutionEvent{
		Type:      HookEventStarted,
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
	})
}

// EmitProgress emits a hook progress event.
func (e *HookEventEmitter) EmitProgress(hookID, hookName, hookEvent, stdout, stderr, output string) {
	if !e.shouldEmit(hookEvent) {
		return
	}
	e.emit(HookExecutionEvent{
		Type:      HookEventProgress,
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
		Stdout:    stdout,
		Stderr:    stderr,
		Output:    output,
	})
}

// EmitResponse emits a hook response event.
func (e *HookEventEmitter) EmitResponse(hookID, hookName, hookEvent, stdout, stderr string, exitCode *int, outcome HookOutcome) {
	// Always log full hook output for debugging.
	outputToLog := stdout
	if outputToLog == "" {
		outputToLog = stderr
	}
	if outputToLog != "" {
		e.logger.Debug("hook execution result",
			"hook_name", hookName,
			"hook_event", hookEvent,
			"outcome", outcome,
			"output", outputToLog,
		)
	}
	if !e.shouldEmit(hookEvent) {
		return
	}
	e.emit(HookExecutionEvent{
		Type:      HookEventResponse,
		HookID:    hookID,
		HookName:  hookName,
		HookEvent: hookEvent,
		Stdout:    stdout,
		Stderr:    stderr,
		Output:    stdout + stderr,
		ExitCode:  exitCode,
		Outcome:   outcome,
	})
}

// StartProgressInterval periodically emits progress events for a running hook.
// Returns a stop function. The getOutput callback is called each interval to
// retrieve current stdout/stderr.
func (e *HookEventEmitter) StartProgressInterval(
	hookID, hookName, hookEvent string,
	getOutput func() (stdout, stderr string),
	interval time.Duration,
) (stop func()) {
	if !e.shouldEmit(hookEvent) {
		return func() {}
	}
	if interval == 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	lastOutput := ""
	go func() {
		for {
			select {
			case <-done:
				ticker.Stop()
				return
			case <-ticker.C:
				stdout, stderr := getOutput()
				output := stdout + stderr
				if output == lastOutput {
					continue
				}
				lastOutput = output
				e.EmitProgress(hookID, hookName, hookEvent, stdout, stderr, output)
			}
		}
	}()
	return func() { close(done) }
}

// SerializePayload serializes a hook event payload to JSON for webhook delivery.
func SerializePayload(event HookEvent, timestamp time.Time, data map[string]any) ([]byte, error) {
	payload := map[string]any{
		"event":     string(event),
		"timestamp": timestamp.Format(time.RFC3339Nano),
	}
	for k, v := range data {
		payload[k] = v
	}
	return json.Marshal(payload)
}
