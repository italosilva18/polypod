// Package hooks — integration layer tying StructuredHookExecutor,
// AgentHookRegistry, and PromptHookRegistry into the main Manager.
//
// All changes here are additive: no existing exports are removed or modified.
package hooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// ---------------------------------------------------------------------------
// Goal 1: StructuredHookExecutor as default executor on the Manager
// ---------------------------------------------------------------------------

// DefaultStructuredExecutor returns a *StructuredHookExecutor wrapping a
// ShellLegacyExecutor, using the manager's logger. This is the recommended
// default executor for new code paths.
//
// It is exposed as a method so callers can obtain it without reaching into
// Manager internals:
//
//	exec := mgr.DefaultStructuredExecutor()
//	result, err := exec.Execute(ctx, input)
func (m *Manager) DefaultStructuredExecutor() *StructuredHookExecutor {
	legacy := &ShellLegacyExecutor{Logger: m.logger}
	return NewStructuredHookExecutor(legacy).WithLogger(m.logger)
}

// ---------------------------------------------------------------------------
// Goal 2: New event type constants
// ---------------------------------------------------------------------------

const (
	// EventAgentLifecycle fires for agent lifecycle occurrences (start, finish,
	// error, tool_start, tool_finish). Delivered as structured payload.
	EventAgentLifecycle HookEvent = "agent_lifecycle"

	// EventPromptProcessing fires before and after the AI processes a prompt.
	EventPromptProcessing HookEvent = "prompt_processing"
)

// ---------------------------------------------------------------------------
// Goal 2: Helper functions bridging Manager with AgentHookRegistry /
// PromptHookRegistry
// ---------------------------------------------------------------------------

// agentEventPayload is the JSON shape posted to Manager.Fire when
// EventAgentLifecycle hooks are registered in the legacy system.
type agentEventPayload struct {
	EventType  string `json:"event_type"`
	AgentName  string `json:"agent_name"`
	TaskID     string `json:"task_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// FireAgentEvent fires an AgentEvent through two channels:
//  1. The global AgentHookRegistry (in-process handlers registered via
//     GetAgentHookRegistry().Register(...)).
//  2. The legacy Manager.Fire pipeline so that any shell/HTTP hooks registered
//     for EventAgentLifecycle also receive the event as a JSON payload.
//
// Both channels operate independently — an error from the Manager path is
// logged but does not prevent registry handlers from running.
func FireAgentEvent(ctx context.Context, mgr *Manager, event AgentEvent) {
	// 1. In-process registry (panic-safe, synchronous).
	GetAgentHookRegistry().Fire(ctx, event)

	// 2. Legacy Manager pipeline.
	if mgr == nil {
		return
	}

	p := agentEventPayload{
		EventType:  event.Type,
		AgentName:  event.AgentName,
		TaskID:     event.TaskID,
		ToolName:   event.ToolName,
		DurationMs: event.DurationMs,
	}
	if event.Error != nil {
		p.Error = event.Error.Error()
	}

	payload := LegacyPayload{
		Event:     EventAgentLifecycle,
		Timestamp: event.Timestamp.Format(time.RFC3339),
		Message:   event.Type,
	}
	// Embed the structured fields into ToolArgs so legacy hook scripts can
	// access them as JSON object fields.
	if b, err := json.Marshal(p); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			payload.ToolArgs = m
		}
	}

	if _, err := mgr.Fire(ctx, payload); err != nil {
		logger := mgr.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("FireAgentEvent: legacy manager fire failed",
			"event_type", event.Type,
			"agent", event.AgentName,
			"error", err,
		)
	}
}

// FirePromptPre runs all pre-prompt handlers in the global PromptHookRegistry.
// It also fires any EventPromptProcessing hooks registered in the Manager's
// legacy pipeline (fire-and-forget; errors are logged).
//
// Returns the (potentially modified) prompt text and whether the AI turn
// should be skipped entirely.
func FirePromptPre(ctx context.Context, mgr *Manager, prompt, sessionID string) (string, bool) {
	// Global registry — this is the authoritative pre-processing path.
	finalPrompt, skip := GetPromptHookRegistry().FirePre(ctx, prompt, sessionID)

	// Also notify legacy hooks (best-effort).
	if mgr != nil {
		payload := LegacyPayload{
			Event:     EventPromptProcessing,
			Timestamp: time.Now().Format(time.RFC3339),
			Message:   finalPrompt,
			SessionID: sessionID,
		}
		if _, err := mgr.Fire(ctx, payload); err != nil {
			logger := mgr.logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("FirePromptPre: legacy manager fire failed",
				"session_id", sessionID,
				"error", err,
			)
		}
	}

	return finalPrompt, skip
}

// FirePromptPost runs all post-prompt handlers in the global PromptHookRegistry.
// It also fires any EventPromptProcessing hooks registered in the Manager's
// legacy pipeline (fire-and-forget; errors are logged).
//
// This phase is side-effects only — return values from handlers are ignored.
func FirePromptPost(ctx context.Context, mgr *Manager, prompt, sessionID string) {
	// Global registry.
	GetPromptHookRegistry().FirePost(ctx, prompt, sessionID)

	// Also notify legacy hooks (best-effort).
	if mgr != nil {
		payload := LegacyPayload{
			Event:     EventPromptProcessing,
			Timestamp: time.Now().Format(time.RFC3339),
			Message:   prompt,
			SessionID: sessionID,
		}
		if _, err := mgr.Fire(ctx, payload); err != nil {
			logger := mgr.logger
			if logger == nil {
				logger = slog.Default()
			}
			logger.Warn("FirePromptPost: legacy manager fire failed",
				"session_id", sessionID,
				"error", err,
			)
		}
	}
}
