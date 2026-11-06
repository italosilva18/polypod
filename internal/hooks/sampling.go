package hooks

import (
	"context"
	"log/slog"
	"sync"
)

// --- Post-Sampling Hooks ---
// Internal hooks executed after model sampling completes.
// Not exposed in settings.json config; only used programmatically.

// PostSamplingContext holds the context for post-sampling hooks.
type PostSamplingContext struct {
	// Messages is the full message history including assistant responses.
	Messages []Message
	// SystemPrompt is the current system prompt.
	SystemPrompt string
	// UserContext holds user-defined context key/value pairs.
	UserContext map[string]string
	// SystemContext holds system-defined context key/value pairs.
	SystemContext map[string]string
	// QuerySource identifies where the query originated from.
	QuerySource string
	// SessionID is the current session ID.
	SessionID string
	// AgentID is the current agent ID (if in an agent context).
	AgentID string
}

// Message is a simplified message type for post-sampling hooks.
// The full message type depends on the outer system; this provides
// the minimal interface needed by hooks.
type Message struct {
	Role    string
	Content string
	// ToolUseID is set for tool-use messages.
	ToolUseID string
	// ToolName is set for tool-use messages.
	ToolName string
	// IsError indicates the message represents an error.
	IsError bool
}

// PostSamplingHook is a callback executed after model sampling completes.
type PostSamplingHook func(ctx context.Context, hookCtx *PostSamplingContext) error

// PostSamplingRegistry manages registered post-sampling hooks.
type PostSamplingRegistry struct {
	mu     sync.RWMutex
	hooks  []PostSamplingHook
	logger *slog.Logger
}

// NewPostSamplingRegistry creates a new post-sampling hook registry.
func NewPostSamplingRegistry(logger *slog.Logger) *PostSamplingRegistry {
	return &PostSamplingRegistry{
		logger: logger,
	}
}

// Register adds a post-sampling hook.
func (r *PostSamplingRegistry) Register(hook PostSamplingHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Clear removes all registered hooks.
func (r *PostSamplingRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = r.hooks[:0]
}

// Execute runs all registered post-sampling hooks sequentially.
// Errors are logged but do not stop execution of remaining hooks.
func (r *PostSamplingRegistry) Execute(ctx context.Context, hookCtx *PostSamplingContext) {
	r.mu.RLock()
	hooks := make([]PostSamplingHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	for i, hook := range hooks {
		if err := hook(ctx, hookCtx); err != nil {
			r.logger.Error("post-sampling hook error",
				"hook_index", i,
				"error", err,
			)
		}
	}
}

// Count returns the number of registered hooks.
func (r *PostSamplingRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.hooks)
}

// --- REPL Hook ---
// Hooks that integrate with the REPL (Read-Eval-Print Loop) lifecycle.

// REPLHookContext provides context for REPL-level hooks.
type REPLHookContext struct {
	Messages      []Message
	SystemPrompt  string
	UserContext   map[string]string
	SystemContext map[string]string
	QuerySource   string
}

// REPLHook is a callback for REPL lifecycle events.
type REPLHook func(ctx context.Context, hookCtx *REPLHookContext) error

// --- Stop Hooks ---
// Hooks executed when the model is about to stop (before final response).

// StopHookRegistry manages stop hooks.
type StopHookRegistry struct {
	mu     sync.RWMutex
	hooks  []StopHook
	logger *slog.Logger
}

// StopHook is a callback executed before the model stops.
type StopHook func(ctx context.Context, hookCtx *StopHookContext) error

// StopHookContext holds context for stop hooks.
type StopHookContext struct {
	Messages     []Message
	SystemPrompt string
	AgentID      string
	// StopReason explains why the model is stopping.
	StopReason string
}

// NewStopHookRegistry creates a new stop hook registry.
func NewStopHookRegistry(logger *slog.Logger) *StopHookRegistry {
	return &StopHookRegistry{
		logger: logger,
	}
}

// Register adds a stop hook.
func (r *StopHookRegistry) Register(hook StopHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Clear removes all registered hooks.
func (r *StopHookRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = r.hooks[:0]
}

// Execute runs all registered stop hooks.
func (r *StopHookRegistry) Execute(ctx context.Context, hookCtx *StopHookContext) {
	r.mu.RLock()
	hooks := make([]StopHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	for i, hook := range hooks {
		if err := hook(ctx, hookCtx); err != nil {
			r.logger.Error("stop hook error",
				"hook_index", i,
				"error", err,
			)
		}
	}
}

// --- Tool Use Summary Hook ---
// Captures and summarizes tool usage during a session.

// ToolUseSummary collects information about tool usage for reporting.
type ToolUseSummary struct {
	ToolName string
	Input    map[string]interface{}
	Output   string
	Duration int64 // milliseconds
	Success  bool
	Error    string
}

// ToolUseSummaryHook processes tool use summaries after each tool call.
type ToolUseSummaryHook func(ctx context.Context, summary *ToolUseSummary) error

// ToolUseSummaryRegistry manages tool use summary hooks.
type ToolUseSummaryRegistry struct {
	mu     sync.RWMutex
	hooks  []ToolUseSummaryHook
	logger *slog.Logger
}

// NewToolUseSummaryRegistry creates a new tool use summary registry.
func NewToolUseSummaryRegistry(logger *slog.Logger) *ToolUseSummaryRegistry {
	return &ToolUseSummaryRegistry{
		logger: logger,
	}
}

// Register adds a tool use summary hook.
func (r *ToolUseSummaryRegistry) Register(hook ToolUseSummaryHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Clear removes all registered hooks.
func (r *ToolUseSummaryRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = r.hooks[:0]
}

// Execute runs all tool use summary hooks for a given tool call.
func (r *ToolUseSummaryRegistry) Execute(ctx context.Context, summary *ToolUseSummary) {
	r.mu.RLock()
	hooks := make([]ToolUseSummaryHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	for i, hook := range hooks {
		if err := hook(ctx, summary); err != nil {
			r.logger.Error("tool use summary hook error",
				"hook_index", i,
				"error", err,
			)
		}
	}
}

// --- Message Transformation Hook ---
// Transforms messages before they're added to the conversation.

// MessageTransformHook can modify a message before it's committed.
// Return the modified message. Return nil to suppress the message.
type MessageTransformHook func(ctx context.Context, msg *Message) (*Message, error)

// MessageTransformRegistry manages message transformation hooks.
type MessageTransformRegistry struct {
	mu     sync.RWMutex
	hooks  []MessageTransformHook
	logger *slog.Logger
}

// NewMessageTransformRegistry creates a new message transform registry.
func NewMessageTransformRegistry(logger *slog.Logger) *MessageTransformRegistry {
	return &MessageTransformRegistry{
		logger: logger,
	}
}

// Register adds a message transform hook.
func (r *MessageTransformRegistry) Register(hook MessageTransformHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Clear removes all registered hooks.
func (r *MessageTransformRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = r.hooks[:0]
}

// Transform applies all registered transforms to a message.
// Returns nil if the message should be suppressed.
func (r *MessageTransformRegistry) Transform(ctx context.Context, msg *Message) (*Message, error) {
	r.mu.RLock()
	hooks := make([]MessageTransformHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	current := msg
	for i, hook := range hooks {
		result, err := hook(ctx, current)
		if err != nil {
			r.logger.Error("message transform hook error",
				"hook_index", i,
				"error", err,
			)
			continue
		}
		if result == nil {
			// Message suppressed
			return nil, nil
		}
		current = result
	}
	return current, nil
}

// --- Response Filter Hook ---
// Filters/modifies the final response before it's sent to the user.

// ResponseFilterHook can modify or filter the assistant response.
type ResponseFilterHook func(ctx context.Context, response string) (string, error)

// ResponseFilterRegistry manages response filter hooks.
type ResponseFilterRegistry struct {
	mu     sync.RWMutex
	hooks  []ResponseFilterHook
	logger *slog.Logger
}

// NewResponseFilterRegistry creates a new response filter registry.
func NewResponseFilterRegistry(logger *slog.Logger) *ResponseFilterRegistry {
	return &ResponseFilterRegistry{
		logger: logger,
	}
}

// Register adds a response filter hook.
func (r *ResponseFilterRegistry) Register(hook ResponseFilterHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Clear removes all registered hooks.
func (r *ResponseFilterRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = r.hooks[:0]
}

// Filter applies all registered filters to a response string.
func (r *ResponseFilterRegistry) Filter(ctx context.Context, response string) (string, error) {
	r.mu.RLock()
	hooks := make([]ResponseFilterHook, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	current := response
	for i, hook := range hooks {
		result, err := hook(ctx, current)
		if err != nil {
			r.logger.Error("response filter hook error",
				"hook_index", i,
				"error", err,
			)
			continue
		}
		current = result
	}
	return current, nil
}
