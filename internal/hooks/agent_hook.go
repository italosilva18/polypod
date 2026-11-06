package hooks

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Agent lifecycle event type constants.
const (
	AgentEventStart      = "start"
	AgentEventFinish     = "finish"
	AgentEventError      = "error"
	AgentEventToolStart  = "tool_start"
	AgentEventToolFinish = "tool_finish"
)

// AgentEvent carries data about a single agent lifecycle occurrence.
type AgentEvent struct {
	// Type is one of the AgentEvent* constants: "start", "finish", "error",
	// "tool_start", or "tool_finish".
	Type string

	// AgentName is the name of the agent that raised the event.
	AgentName string

	// TaskID uniquely identifies the task/run this event belongs to.
	TaskID string

	// ToolName is set for tool_start and tool_finish events.
	ToolName string

	// Error is set for error events.
	Error error

	// Timestamp records when the event was created.
	Timestamp time.Time

	// DurationMs records elapsed time for finish events (agent or tool).
	DurationMs int64
}

// AgentHookHandler is implemented by any type that wants to observe agent
// lifecycle events.
type AgentHookHandler interface {
	OnAgentEvent(ctx context.Context, event AgentEvent)
}

// agentNamedHandler pairs a handler with the name it was registered under, so
// that panic recovery messages can include a useful identifier.
type agentNamedHandler struct {
	name    string
	handler AgentHookHandler
}

// AgentHookRegistry is a thread-safe list of AgentHookHandlers.
type AgentHookRegistry struct {
	mu       sync.RWMutex
	handlers []agentNamedHandler
}

// NewAgentHookRegistry creates an empty AgentHookRegistry.
func NewAgentHookRegistry() *AgentHookRegistry {
	return &AgentHookRegistry{}
}

// Register appends h to the registry under the given name. name is used only
// for logging; it does not need to be unique.
func (r *AgentHookRegistry) Register(name string, h AgentHookHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, agentNamedHandler{name: name, handler: h})
}

// Fire calls every registered handler synchronously in registration order.
// Panics inside a handler are recovered and logged via slog so that one bad
// handler cannot prevent the remaining handlers from running.
func (r *AgentHookRegistry) Fire(ctx context.Context, event AgentEvent) {
	r.mu.RLock()
	// Snapshot the slice under the read-lock so that handlers can call
	// Register without deadlocking.
	snapshot := make([]agentNamedHandler, len(r.handlers))
	copy(snapshot, r.handlers)
	r.mu.RUnlock()

	for _, nh := range snapshot {
		func(nh agentNamedHandler) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("agent hook handler panicked",
						"handler", nh.name,
						"event_type", event.Type,
						"agent", event.AgentName,
						"task_id", event.TaskID,
						"panic", fmt.Sprintf("%v", rec),
					)
				}
			}()
			nh.handler.OnAgentEvent(ctx, event)
		}(nh)
	}
}

// ---------------------------------------------------------------------------
// Convenience constructors
// ---------------------------------------------------------------------------

// StartEvent returns an AgentEvent with Type "start".
func StartEvent(agent, task string) AgentEvent {
	return AgentEvent{
		Type:      AgentEventStart,
		AgentName: agent,
		TaskID:    task,
		Timestamp: time.Now(),
	}
}

// FinishEvent returns an AgentEvent with Type "finish" and the given duration.
func FinishEvent(agent, task string, durationMs int64) AgentEvent {
	return AgentEvent{
		Type:       AgentEventFinish,
		AgentName:  agent,
		TaskID:     task,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
	}
}

// ErrorEvent returns an AgentEvent with Type "error" and the given error.
func ErrorEvent(agent, task string, err error) AgentEvent {
	return AgentEvent{
		Type:      AgentEventError,
		AgentName: agent,
		TaskID:    task,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ToolStartEvent returns an AgentEvent with Type "tool_start".
func ToolStartEvent(agent, task, tool string) AgentEvent {
	return AgentEvent{
		Type:      AgentEventToolStart,
		AgentName: agent,
		TaskID:    task,
		ToolName:  tool,
		Timestamp: time.Now(),
	}
}

// ToolFinishEvent returns an AgentEvent with Type "tool_finish" and the given
// duration.
func ToolFinishEvent(agent, task, tool string, durationMs int64) AgentEvent {
	return AgentEvent{
		Type:       AgentEventToolFinish,
		AgentName:  agent,
		TaskID:     task,
		ToolName:   tool,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Global registry
// ---------------------------------------------------------------------------

var globalAgentHookRegistry = NewAgentHookRegistry()

// GetAgentHookRegistry returns the process-wide AgentHookRegistry.
func GetAgentHookRegistry() *AgentHookRegistry {
	return globalAgentHookRegistry
}
