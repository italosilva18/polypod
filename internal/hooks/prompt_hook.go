package hooks

import (
	"context"
	"sync"
	"time"
)

// --- Prompt Hooks ---
// Hooks that fire before and after the AI processes a prompt, allowing
// callers to inspect or modify the prompt text, or skip the AI turn entirely.

// PromptHookEvent carries the data passed to each prompt hook handler.
type PromptHookEvent struct {
	// Phase is either "pre" (before the AI turn) or "post" (after the AI turn).
	Phase string
	// PromptText is the current prompt string.
	PromptText string
	// SessionID identifies the current session.
	SessionID string
	// Timestamp records when the event was created.
	Timestamp time.Time
}

// PromptHookResult is the value returned by a PromptHookHandler.
type PromptHookResult struct {
	// Modified indicates that the handler wants to replace the prompt text.
	Modified bool
	// NewPromptText is the replacement prompt; only used when Modified is true.
	NewPromptText string
	// SkipTurn, when true, instructs the caller to abort the AI turn entirely.
	SkipTurn bool
	// Error carries any non-fatal error the handler wishes to surface.
	Error error
}

// PromptHookHandler is implemented by any type that wishes to intercept prompts.
type PromptHookHandler interface {
	OnPrompt(ctx context.Context, event PromptHookEvent) PromptHookResult
}

// promptNamedHandler pairs a registered name with its handler for ordered storage.
type promptNamedHandler struct {
	name    string
	handler PromptHookHandler
}

// PromptHookRegistry is an ordered, goroutine-safe collection of PromptHookHandlers.
type PromptHookRegistry struct {
	mu       sync.RWMutex
	handlers []promptNamedHandler
}

// NewPromptHookRegistry creates an empty PromptHookRegistry.
func NewPromptHookRegistry() *PromptHookRegistry {
	return &PromptHookRegistry{}
}

// Register appends a handler under the given name. Handlers run in
// registration order.
func (r *PromptHookRegistry) Register(name string, h PromptHookHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, promptNamedHandler{name: name, handler: h})
}

// FirePre runs all registered handlers in the "pre" phase. Each handler
// receives the prompt text that was (potentially) modified by the previous
// handler. If any handler returns SkipTurn=true, FirePre immediately returns
// (skip=true) without running remaining handlers.
//
// Returns the final prompt text and whether the AI turn should be skipped.
func (r *PromptHookRegistry) FirePre(ctx context.Context, prompt string, sessionID string) (finalPrompt string, skip bool) {
	r.mu.RLock()
	handlers := make([]promptNamedHandler, len(r.handlers))
	copy(handlers, r.handlers)
	r.mu.RUnlock()

	current := prompt
	for _, nh := range handlers {
		event := PromptHookEvent{
			Phase:      "pre",
			PromptText: current,
			SessionID:  sessionID,
			Timestamp:  time.Now(),
		}
		result := nh.handler.OnPrompt(ctx, event)
		if result.SkipTurn {
			return current, true
		}
		if result.Modified {
			current = result.NewPromptText
		}
	}
	return current, false
}

// FirePost runs all registered handlers in the "post" phase. Return values
// from handlers are intentionally ignored — this phase is side-effects only.
func (r *PromptHookRegistry) FirePost(ctx context.Context, prompt string, sessionID string) {
	r.mu.RLock()
	handlers := make([]promptNamedHandler, len(r.handlers))
	copy(handlers, r.handlers)
	r.mu.RUnlock()

	for _, nh := range handlers {
		event := PromptHookEvent{
			Phase:      "post",
			PromptText: prompt,
			SessionID:  sessionID,
			Timestamp:  time.Now(),
		}
		nh.handler.OnPrompt(ctx, event)
	}
}

// globalPromptHookRegistry is the package-level singleton registry.
var globalPromptHookRegistry = NewPromptHookRegistry()

// GetPromptHookRegistry returns the package-level PromptHookRegistry.
func GetPromptHookRegistry() *PromptHookRegistry {
	return globalPromptHookRegistry
}
