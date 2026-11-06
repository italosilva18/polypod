// Package hooks — structured-response executor wrapper.
//
// This file adds a thin executor layer that wraps any "legacy" hook executor
// (one that returns a HookResult with raw stdout) and upgrades the result by
// parsing stdout as a StructuredHookResponse (see structured.go).
//
// The wrapper is additive: existing call sites in executors.go and the hook
// dispatcher are NOT modified. Callers that want structured-response behaviour
// wrap their executor with NewStructuredHookExecutor and use the returned
// StructuredHookResult to apply updatedInput / updatedMCPToolOutput via the
// helpers in inputmod.go.
//
// Since there is no pre-existing "HookInput" type or executor interface in
// this package, we define a small adapter shape here. The shell code path
// remains untouched — callers can build a thin LegacyExecutor on top of
// ExecShellHook + InterpretShellResult (see ShellLegacyExecutor below) and
// hand it to NewStructuredHookExecutor.
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
)

// HookInput is a minimal, executor-agnostic input envelope used by the
// structured-response wrapper. It is deliberately decoupled from
// ShellHookInput so that HTTP/prompt/agent executors can also be wrapped
// without dragging in shell-specific fields.
//
// Fields are a superset of what the existing executors need; unused fields
// are simply ignored by a given backend.
type HookInput struct {
	Hook      HookCommand
	HookName  string
	HookEvent HookEvent
	ToolName  string
	ToolUseID string
	JSONInput string
	Shell     ShellHookInput // populated when routing through the shell path
}

// LegacyExecutor is the minimal interface satisfied by any "raw" hook
// executor: given a context and a HookInput, produce a HookResult whose
// Stdout may or may not be a structured JSON response.
//
// The shape matches what ExecShellHook + InterpretShellResult produce in
// aggregate, so an adapter is trivial (see ShellLegacyExecutor).
type LegacyExecutor interface {
	Execute(ctx context.Context, input HookInput) (*HookResult, error)
}

// LegacyExecutorFunc adapts an ordinary function to the LegacyExecutor
// interface, matching the common http.HandlerFunc pattern.
type LegacyExecutorFunc func(ctx context.Context, input HookInput) (*HookResult, error)

// Execute implements LegacyExecutor.
func (f LegacyExecutorFunc) Execute(ctx context.Context, input HookInput) (*HookResult, error) {
	return f(ctx, input)
}

// ShellLegacyExecutor is a ready-made LegacyExecutor that routes HookInput
// through the existing shell execution path (ExecShellHook +
// InterpretShellResult). It is provided as a convenience so that callers
// wiring the structured wrapper into the dispatcher do not need to write
// their own adapter.
type ShellLegacyExecutor struct {
	Logger *slog.Logger
}

// Execute implements LegacyExecutor for the shell path.
func (s *ShellLegacyExecutor) Execute(ctx context.Context, input HookInput) (*HookResult, error) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	output, err := ExecShellHook(ctx, input.Hook, input.Shell, logger)
	if err != nil {
		return &HookResult{
			Hook:    &input.Hook,
			Outcome: HookOutcomeCancelled,
			Stderr:  err.Error(),
		}, err
	}
	return InterpretShellResult(&input.Hook, input.HookName, input.HookEvent, output, input.ToolUseID), nil
}

// StructuredHookExecutor wraps any LegacyExecutor and upgrades its output
// to carry a parsed StructuredHookResponse when stdout is JSON.
//
// Behaviour:
//
//   - Delegates Execute to the inner executor.
//   - If the returned result's Stdout parses as a StructuredHookResponse,
//     the upgraded StructuredHookResult carries a non-nil Structured field
//     and the underlying HookResult is augmented (Decision, SystemMessage,
//     PermissionDecision, UpdatedInput, ...) where those fields were not
//     already populated by the inner executor.
//   - If stdout is not structured (ErrNotStructured), the result is
//     returned unchanged with Structured == nil.
//   - If stdout is structured-looking but fails to parse, a warning is
//     logged and the raw result is returned unchanged (graceful degradation).
type StructuredHookExecutor struct {
	inner  LegacyExecutor
	logger *slog.Logger
}

// NewStructuredHookExecutor constructs a StructuredHookExecutor that wraps
// the given legacy executor. A nil logger falls back to slog.Default().
func NewStructuredHookExecutor(inner LegacyExecutor) *StructuredHookExecutor {
	return &StructuredHookExecutor{inner: inner, logger: slog.Default()}
}

// WithLogger returns a copy of e that uses the provided logger. The logger
// is used only for graceful-degradation warnings on malformed JSON.
func (e *StructuredHookExecutor) WithLogger(logger *slog.Logger) *StructuredHookExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &StructuredHookExecutor{inner: e.inner, logger: logger}
}

// Execute runs the inner executor and attempts to parse its stdout as a
// StructuredHookResponse. It always returns a *StructuredHookResult (never
// nil on success) so callers can use its Apply* helpers uniformly.
func (e *StructuredHookExecutor) Execute(ctx context.Context, input HookInput) (*StructuredHookResult, error) {
	raw, err := e.inner.Execute(ctx, input)
	if raw == nil {
		// Preserve the inner error but synthesise an empty result so callers
		// can still dereference the return value defensively.
		raw = &HookResult{Hook: &input.Hook, Outcome: HookOutcomeCancelled}
	}

	structured := &StructuredHookResult{HookResult: *raw}

	parsed, parseErr := ParseStructuredResponse([]byte(raw.Stdout))
	switch {
	case parseErr == nil:
		structured.Structured = parsed
		applyStructuredToResult(parsed, &structured.HookResult)
	case errors.Is(parseErr, ErrNotStructured):
		// Plain-text stdout — nothing to upgrade.
	default:
		// Looked like JSON but failed to decode. Log and degrade gracefully.
		e.logger.Warn("structured hook response parse failed",
			"hook", input.HookName,
			"event", string(input.HookEvent),
			"error", parseErr,
		)
	}

	return structured, err
}

// applyStructuredToResult merges parsed structured fields into the
// underlying HookResult. Fields already set by the inner executor are
// preserved (we only fill in blanks) so that exit-code-driven decisions
// remain authoritative.
func applyStructuredToResult(parsed *StructuredHookResponse, r *HookResult) {
	if parsed == nil || r == nil {
		return
	}

	// Top-level continue / stop-reason.
	if parsed.Continue != nil && !*parsed.Continue {
		r.PreventContinuation = true
		if r.StopReason == "" {
			r.StopReason = parsed.StopReason
		}
		if r.Outcome != HookOutcomeBlocking {
			r.Outcome = HookOutcomeBlocking
		}
	}

	// Decision → blocking error, without clobbering an existing one.
	switch strings.ToLower(parsed.Decision) {
	case "block", "deny":
		if r.BlockingError == nil {
			r.BlockingError = &HookBlockingError{BlockingError: parsed.Reason}
		}
		if r.Outcome != HookOutcomeBlocking {
			r.Outcome = HookOutcomeBlocking
		}
	}

	// System message surfaced through Message.
	if parsed.SystemMessage != "" && r.Message == "" {
		r.Message = parsed.SystemMessage
	}

	hso := parsed.HookSpecificOutput

	// Permission decision.
	if hso.PermissionDecision != "" && r.PermissionBehavior == "" {
		r.PermissionBehavior = strings.ToLower(hso.PermissionDecision)
	}
	if hso.PermissionDecisionReason != "" && r.HookPermissionDecisionReason == "" {
		r.HookPermissionDecisionReason = hso.PermissionDecisionReason
	}

	// Additional context / initial user message.
	if hso.AdditionalContext != "" && r.AdditionalContext == "" {
		r.AdditionalContext = hso.AdditionalContext
	}
	if hso.InitialUserMessage != "" && r.InitialUserMessage == "" {
		r.InitialUserMessage = hso.InitialUserMessage
	}

	// Retry flag (PermissionDenied).
	if hso.Retry != nil && !r.Retry {
		r.Retry = *hso.Retry
	}

	// UpdatedInput: the legacy HookResult uses map[string]interface{}, while
	// the structured payload is json.RawMessage. Decode on a best-effort basis
	// so aggregation code can still merge it. The raw form lives on the
	// Structured pointer for callers that want byte-level control.
	if len(hso.UpdatedInput) > 0 && r.UpdatedInput == nil {
		var m map[string]interface{}
		if err := json.Unmarshal(hso.UpdatedInput, &m); err == nil && m != nil {
			r.UpdatedInput = m
		}
	}

	// UpdatedMCPToolOutput: same best-effort decode into interface{}.
	if len(hso.UpdatedMCPToolOutput) > 0 && r.UpdatedMCPToolOutput == nil {
		var v interface{}
		if err := json.Unmarshal(hso.UpdatedMCPToolOutput, &v); err == nil {
			r.UpdatedMCPToolOutput = v
		}
	}
}

// StructuredHookResult is a HookResult augmented with the parsed structured
// response (if any). It embeds HookResult by value so that all existing
// consumers of HookResult fields continue to work unchanged.
type StructuredHookResult struct {
	HookResult
	// Structured is the parsed response, or nil if stdout was plain text /
	// failed to parse. Callers should nil-check before dereferencing.
	Structured *StructuredHookResponse
}

// ApplyToToolInput applies any updatedInput field from the structured
// response onto the given original tool-input JSON. If no structured
// response is attached, or no updatedInput was provided, the original is
// returned unchanged.
//
// This is the PreToolUse path: hooks can rewrite tool arguments before
// the tool actually runs.
func (r *StructuredHookResult) ApplyToToolInput(original json.RawMessage) (json.RawMessage, error) {
	if r == nil || r.Structured == nil {
		return original, nil
	}
	updated := r.Structured.HookSpecificOutput.UpdatedInput
	if len(updated) == 0 {
		return original, nil
	}
	return ApplyInputModification(original, updated)
}

// ApplyToToolOutput applies any updatedMCPToolOutput field from the
// structured response onto the given original tool-output JSON. If no
// structured response is attached, or no updatedMCPToolOutput was provided,
// the original is returned unchanged.
//
// This is the PostToolUse path: hooks can rewrite tool results before the
// assistant sees them.
func (r *StructuredHookResult) ApplyToToolOutput(original json.RawMessage) (json.RawMessage, error) {
	if r == nil || r.Structured == nil {
		return original, nil
	}
	updated := r.Structured.HookSpecificOutput.UpdatedMCPToolOutput
	if len(updated) == 0 {
		return original, nil
	}
	return ApplyOutputModification(original, updated)
}
