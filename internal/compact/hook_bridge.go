// hook_bridge.go wires compaction lifecycle events to the hook system.
//
// CompactService itself does not import internal/hooks to avoid an import
// cycle and to keep the compact package easy to test in isolation. Instead,
// callers can provide any HookDispatcher implementation (typically a thin
// adapter over hooks.Manager) and this bridge takes care of translating
// CompactMeta/CompactSummary into the map[string]any payload format that
// the hooks subsystem expects.
//
// PreCompact and PostCompact are defined in internal/hooks/events.go as
// HookEvent constants ("PreCompact" and "PostCompact"). Hook failures
// during compaction are intentionally non-fatal: the compaction itself
// must proceed regardless of whether a user-configured hook succeeds.

package compact

import (
	"context"
	"log/slog"
)

// HookDispatcher is the minimal interface the compact package needs from
// the hooks subsystem. Keeping it local (rather than importing
// internal/hooks directly) prevents import cycles and makes the hook
// bridge trivially mockable in tests.
//
// Implementations typically wrap hooks.Manager.Dispatch or a similar
// routine that runs all registered hook commands for the given event
// and returns their combined stdout (or an error if dispatching itself
// failed).
type HookDispatcher interface {
	Dispatch(ctx context.Context, event string, input map[string]any) ([]byte, error)
}

// CompactMeta describes a compaction operation in hook-friendly terms.
// It is passed to PreCompact hooks and included in PostCompact payloads
// so user scripts can inspect why compaction was triggered and how
// large the conversation was at that moment.
type CompactMeta struct {
	// Trigger is one of "auto", "manual", or "reactive" and matches the
	// matcher field documented on the PreCompact/PostCompact events.
	Trigger string

	// SessionID identifies the conversation being compacted.
	SessionID string

	// MessageCount is the number of messages in the conversation at
	// the moment compaction started.
	MessageCount int

	// EstimatedTokens is the rough pre-compaction token count. This
	// mirrors CompactResult.PreCompactTokens but is available before
	// compaction runs.
	EstimatedTokens int

	// CustomInstructions is the optional user-provided instructions
	// string passed through to the summarizer (e.g. "focus on code
	// changes"). PreCompact hooks may want to audit or override these.
	CustomInstructions string
}

// CompactSummary captures the result of a compaction run for PostCompact
// hooks. It is intentionally a plain value (no pointers, no error type)
// so that it can be serialized as a JSON-style map for hook commands.
type CompactSummary struct {
	TokensBefore   int
	TokensAfter    int
	MessagesBefore int
	MessagesAfter  int
	DurationMs     int64
	Success        bool
	// Error holds a textual error message when Success is false.
	Error string
}

// HookBridge fires PreCompact / PostCompact hook events around
// compaction operations.
//
// A zero HookBridge (Dispatcher == nil) is safe but unusual — prefer
// NoopHookBridge when there is no dispatcher configured.
type HookBridge struct {
	Dispatcher HookDispatcher
	Logger     *slog.Logger
}

// NewHookBridge returns a HookBridge with the default logger.
// If dispatcher is nil, the returned bridge becomes a no-op (the Fire*
// methods will skip dispatch silently).
func NewHookBridge(dispatcher HookDispatcher) *HookBridge {
	return &HookBridge{
		Dispatcher: dispatcher,
		Logger:     slog.Default(),
	}
}

// hook event names — mirror hooks.EventPreCompact / EventPostCompact.
// They are defined as string literals (rather than imported from the
// hooks package) to keep this file dependency-free.
const (
	eventPreCompact  = "PreCompact"
	eventPostCompact = "PostCompact"
)

// FirePreCompact runs any configured PreCompact hooks synchronously.
// Dispatcher errors are logged at warn level and then suppressed: the
// caller (typically CompactService.Compact) should treat hook failures
// as non-fatal and proceed with compaction. Timeouts are the caller's
// responsibility via ctx.
func (h *HookBridge) FirePreCompact(ctx context.Context, meta CompactMeta) error {
	if h == nil || h.Dispatcher == nil {
		return nil
	}
	input := CompactMetaToHookInput(meta)
	if _, err := h.Dispatcher.Dispatch(ctx, eventPreCompact, input); err != nil {
		h.logger().Warn("PreCompact hook dispatch failed",
			"event", eventPreCompact,
			"trigger", meta.Trigger,
			"err", err,
		)
		return nil
	}
	return nil
}

// FirePostCompact runs any configured PostCompact hooks synchronously.
// Same non-fatal error semantics as FirePreCompact — a failing hook
// never breaks the compaction result returned to the caller.
func (h *HookBridge) FirePostCompact(ctx context.Context, meta CompactMeta, result CompactSummary) error {
	if h == nil || h.Dispatcher == nil {
		return nil
	}
	input := CompactMetaToHookInput(meta)
	for k, v := range CompactSummaryToHookInput(result) {
		input[k] = v
	}
	if _, err := h.Dispatcher.Dispatch(ctx, eventPostCompact, input); err != nil {
		h.logger().Warn("PostCompact hook dispatch failed",
			"event", eventPostCompact,
			"trigger", meta.Trigger,
			"err", err,
		)
		return nil
	}
	return nil
}

func (h *HookBridge) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// CompactMetaToHookInput converts a CompactMeta into the JSON-style
// map that hook commands receive on stdin. Field names follow the
// snake_case convention used by the rest of the hook system (see
// hooks/events.go matcher descriptions for PreCompact/PostCompact).
func CompactMetaToHookInput(m CompactMeta) map[string]any {
	return map[string]any{
		"trigger":             m.Trigger,
		"session_id":          m.SessionID,
		"message_count":       m.MessageCount,
		"estimated_tokens":    m.EstimatedTokens,
		"custom_instructions": m.CustomInstructions,
	}
}

// CompactSummaryToHookInput converts a CompactSummary into the JSON-style
// map merged into PostCompact hook input. Keys are namespaced under
// "result" so they don't collide with CompactMeta fields.
func CompactSummaryToHookInput(s CompactSummary) map[string]any {
	return map[string]any{
		"result": map[string]any{
			"tokens_before":   s.TokensBefore,
			"tokens_after":    s.TokensAfter,
			"messages_before": s.MessagesBefore,
			"messages_after":  s.MessagesAfter,
			"duration_ms":     s.DurationMs,
			"success":         s.Success,
			"error":           s.Error,
		},
	}
}

// NoopHookBridge is a safe default when no HookDispatcher is wired.
// Its methods never touch the network, never spawn processes, and
// always return nil. CompactService should use it whenever the caller
// did not provide a real bridge.
type NoopHookBridge struct{}

// FirePreCompact on NoopHookBridge is a no-op returning nil.
func (NoopHookBridge) FirePreCompact(ctx context.Context, meta CompactMeta) error {
	return nil
}

// FirePostCompact on NoopHookBridge is a no-op returning nil.
func (NoopHookBridge) FirePostCompact(ctx context.Context, meta CompactMeta, result CompactSummary) error {
	return nil
}
