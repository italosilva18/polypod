package compact

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/conversation"
)

// Reactive compaction fires when the model itself rejects a request because
// the prompt no longer fits. Unlike proactive auto-compact (which watches
// token estimates before a call) or microcompact (which prunes old tool
// outputs), reactive compact is strictly a recovery path: it runs AFTER the
// API has returned a context-overflow error and then retries the failed turn
// with a freshly compacted history.
//
// This mirrors the TypeScript reactiveCompact subsystem in
// src_extracted/src/services/compact/reactiveCompact.ts (referenced from
// query.ts around the isWithheld413 branch). In TS the trigger is gated on
// the last assistant message being a prompt-too-long API error; in Go we
// inspect the error returned from turnFn directly.

// Errors specific to reactive compaction.
var (
	// ErrReactiveCompactDisabled is returned when reactive compaction is
	// requested but the compactor has been configured as disabled.
	ErrReactiveCompactDisabled = errors.New("reactive compaction disabled")

	// ErrReactiveRetryExhausted is returned when a reactive compaction and
	// retry still cannot recover from the overflow error. The wrapped error
	// is the most recent failure from the retried turn.
	ErrReactiveRetryExhausted = errors.New("reactive compaction retry still failing")
)

// ReactiveCompactorBackend is the minimal contract a compactor must satisfy
// to be driven reactively. It exists so that reactive.go can stay in the
// compact package without importing query/model packages (which would create
// an import cycle: query -> compact -> query).
//
// The production implementation is *CompactService.Compact, but tests can
// substitute a stub that records calls and lets them assert retry behavior
// without spinning up a real summarizer.
type ReactiveCompactorBackend interface {
	// CompactNow runs a full compaction pass and returns the result. The
	// isAuto flag should be true — reactive compaction is a form of auto
	// compaction from the user's point of view, since it is not triggered
	// by an explicit /compact command.
	CompactNow(ctx context.Context) (*CompactResult, error)
}

// ReactiveModelClient is an optional hook exposed so that call sites can
// react to the compact result (for example, rebuild a system prompt or
// refresh token budgets) before the retry runs. Implementations are free
// to be no-ops. It is accepted as an interface to avoid a dependency on
// the model or provider packages from within compact/.
type ReactiveModelClient interface {
	// OnCompacted is invoked once, synchronously, after a successful
	// reactive compaction and before the turn function is retried.
	OnCompacted(ctx context.Context, result *CompactResult) error
}

// ReactiveBudget is the optional budget interface. The proactive auto-compact
// path uses a task budget to carry over token spend across a compaction
// boundary; the reactive path exposes the same hook so that a failed-and-
// recovered turn does not double-charge the budget. It is intentionally tiny
// — we only need to signal the transition.
type ReactiveBudget interface {
	// NotifyReactiveCompact is called once on a successful reactive
	// compaction so the budget can record the pre/post token counts. Errors
	// are logged but do not abort the retry.
	NotifyReactiveCompact(pre, post int)
}

// ReactiveCompactor coordinates the "model returned overflow -> compact ->
// retry once" recovery loop. Dependencies are accepted as interfaces so the
// compact package does not introduce import cycles with query/provider.
//
// A ReactiveCompactor is safe for concurrent use; an internal mutex
// serializes retries so that two turns hitting overflow at once do not race
// on the underlying compactor.
type ReactiveCompactor struct {
	backend ReactiveCompactorBackend
	model   ReactiveModelClient // optional, may be nil
	budget  ReactiveBudget      // optional, may be nil
	logger  *slog.Logger

	// MaxRetries caps how many reactive retries are attempted per turn. The
	// TypeScript implementation uses a single-shot retry and we follow the
	// same policy by default; values <= 0 are normalized to 1.
	maxRetries int

	mu           sync.Mutex
	lastAttempt  bool // whether reactive compact already ran this turn
	totalRetries int  // lifetime counter, exposed via Stats()
}

// ReactiveConfig bundles the optional knobs for NewReactiveCompactor. Zero
// values map to safe defaults.
type ReactiveConfig struct {
	// MaxRetries is how many times to compact-and-retry per turn. Defaults
	// to 1 (single-shot). Values > 1 should be used with caution — if the
	// first retry still overflows, the second is very unlikely to succeed
	// and risks burning tokens on repeated summarization.
	MaxRetries int

	// Logger is the slog instance to use. nil falls back to slog.Default().
	Logger *slog.Logger
}

// NewReactiveCompactor constructs a ReactiveCompactor. The backend is
// required; model and budget are optional and may be nil.
func NewReactiveCompactor(
	backend ReactiveCompactorBackend,
	model ReactiveModelClient,
	budget ReactiveBudget,
	cfg ReactiveConfig,
) *ReactiveCompactor {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	return &ReactiveCompactor{
		backend:    backend,
		model:      model,
		budget:     budget,
		logger:     logger,
		maxRetries: maxRetries,
	}
}

// Stats reports the total number of reactive retries attempted over the
// lifetime of this compactor. Useful for metrics and tests.
func (rc *ReactiveCompactor) Stats() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.totalRetries
}

// ResetAttempt clears the per-turn "already attempted" guard. Call this at
// the start of each new turn if you want reactive compaction to be available
// again on the next overflow. RecoverAndRetry resets it automatically on
// success; this method is only needed if you surface a non-overflow error
// and still want to keep the compactor usable for the next turn.
func (rc *ReactiveCompactor) ResetAttempt() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.lastAttempt = false
}

// IsContextOverflowError reports whether err looks like a context-window /
// prompt-too-long rejection from the upstream model or provider. The check
// is intentionally permissive: different providers phrase the same condition
// differently, and we would rather recover once too often than silently drop
// a recoverable turn.
//
// Patterns recognized (case-insensitive substring match):
//   - "prompt is too long"          (Anthropic direct API)
//   - "context length"              (OpenAI family, generic)
//   - "context_length_exceeded"     (OpenAI error code)
//   - "maximum context length"      (OpenAI long-form phrasing)
//   - "token limit"                 (various)
//   - "input too long" / "input is too long" (Google, generic)
//   - "request too large"           (Anthropic 413 message)
//   - "413"                         (HTTP status code in wrapped errors)
//   - "content too long"            (some OpenAI-compatibles)
//
// A nil error always returns false.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range overflowNeedles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// overflowNeedles is the lowercase pattern set used by IsContextOverflowError.
// Kept as a package-level slice so tests can reference it and so extending
// the list is a one-liner as new providers surface.
var overflowNeedles = []string{
	"prompt is too long",
	"prompt too long",
	"context length",
	"context_length_exceeded",
	"context window",
	"maximum context length",
	"token limit",
	"too many tokens",
	"input too long",
	"input is too long",
	"request too large",
	"content too long",
	"413",
}

// ShouldReactivelyCompact applies the policy gate for whether a given error
// at a given turn count warrants a reactive compaction attempt. The gate is:
//
//  1. The error must match the overflow pattern.
//  2. turnCount must be > 0 — reacting on the very first turn (turn 0) would
//     imply the user's opening prompt alone exceeds the context, which
//     compaction cannot fix and which would loop forever if retried.
//
// Returning false does not mean the error is unrecoverable; it just means
// reactive compact is not the right tool. Callers should surface the error.
func ShouldReactivelyCompact(err error, turnCount int) bool {
	if turnCount <= 0 {
		return false
	}
	return IsContextOverflowError(err)
}

// RecoverAndRetry invokes turnFn. If the returned error is a context overflow
// and reactive compaction has not already been attempted on this turn, it
// runs the backend's compaction pass and invokes turnFn once more.
//
// Semantics:
//   - nil error from turnFn:           returned as-is, no compaction.
//   - non-overflow error from turnFn:  returned as-is, no compaction.
//   - overflow error, compact fails:   the compact error is returned,
//     wrapping the original turn error in the chain via errors.Join.
//   - overflow error, compact ok,
//     retry still overflow:            ErrReactiveRetryExhausted wrapping
//     the retry error — the caller should treat this as unrecoverable.
//   - overflow error, compact ok,
//     retry succeeds:                  nil.
//
// The per-turn "already attempted" guard prevents an infinite
// overflow -> compact -> overflow -> compact cycle when the prompt is so
// large that even a compacted history still overflows. Callers that want to
// allow reactive compaction on a subsequent turn should call ResetAttempt
// before invoking the next turn, or rely on a fresh ReactiveCompactor.
func (rc *ReactiveCompactor) RecoverAndRetry(ctx context.Context, turnFn func() error) error {
	if turnFn == nil {
		return errors.New("reactive compact: turnFn is nil")
	}

	// Initial turn attempt.
	err := turnFn()
	if err == nil {
		rc.mu.Lock()
		rc.lastAttempt = false
		rc.mu.Unlock()
		return nil
	}
	if !IsContextOverflowError(err) {
		return err
	}

	// Guard against repeated reactive compaction within the same turn.
	rc.mu.Lock()
	if rc.lastAttempt {
		rc.mu.Unlock()
		rc.logger.Warn("reactive compact already attempted this turn; surfacing error",
			"error", err,
		)
		return fmt.Errorf("%w: %v", ErrReactiveRetryExhausted, err)
	}
	rc.lastAttempt = true
	rc.totalRetries++
	attempts := rc.maxRetries
	rc.mu.Unlock()

	rc.logger.Info("reactive compact triggered by overflow",
		"error", err,
		"max_retries", attempts,
	)

	// Run compaction. A failure here means we cannot even summarize, so
	// there is nothing to retry — surface the compact error joined with the
	// original overflow so callers still see both halves of the story.
	result, compactErr := rc.backend.CompactNow(ctx)
	if compactErr != nil {
		rc.logger.Warn("reactive compact backend failed",
			"compact_error", compactErr,
			"original_error", err,
		)
		if errors.Is(compactErr, ErrCompactionInProgress) {
			// Another goroutine is already compacting; the turn error
			// should be surfaced unchanged so the caller can retry later.
			return err
		}
		return errors.Join(compactErr, err)
	}
	if result == nil {
		rc.logger.Warn("reactive compact returned nil result; surfacing original error")
		return err
	}

	// Notify the optional budget + model hooks. Hook errors are logged but
	// do not abort the retry — the compaction itself already succeeded and
	// the retry is the whole point.
	if rc.budget != nil {
		rc.budget.NotifyReactiveCompact(result.PreCompactTokens, result.PostCompactTokens)
	}
	if rc.model != nil {
		if hookErr := rc.model.OnCompacted(ctx, result); hookErr != nil {
			rc.logger.Warn("reactive compact model hook failed",
				"error", hookErr,
			)
		}
	}

	// Single retry. The TS implementation is single-shot; maxRetries > 1 is
	// a loop but each iteration still goes through the guard above on the
	// very next call because lastAttempt is already set.
	var retryErr error
	for i := 0; i < attempts; i++ {
		retryErr = turnFn()
		if retryErr == nil {
			rc.logger.Info("reactive compact retry succeeded",
				"attempt", i+1,
				"pre_tokens", result.PreCompactTokens,
				"post_tokens", result.PostCompactTokens,
			)
			// Clear the per-turn guard so the NEXT turn (a fresh user
			// message) can use reactive compaction again if needed.
			rc.mu.Lock()
			rc.lastAttempt = false
			rc.mu.Unlock()
			return nil
		}
		if !IsContextOverflowError(retryErr) {
			// Non-overflow failure on retry — surface directly, we have
			// nothing more to do. Leave lastAttempt set so we don't try
			// again on the caller's behalf.
			rc.logger.Warn("reactive compact retry failed with non-overflow error",
				"attempt", i+1,
				"error", retryErr,
			)
			return retryErr
		}
		rc.logger.Warn("reactive compact retry still overflowing",
			"attempt", i+1,
			"error", retryErr,
		)
	}

	return fmt.Errorf("%w: %v", ErrReactiveRetryExhausted, retryErr)
}

// NewCompactServiceBackend wraps a *CompactService plus a messages provider
// closure into a ReactiveCompactorBackend. The messagesFn is invoked each
// time compaction runs and should return the current conversation history
// captured from the caller's thread state.
//
// The compact package already imports conversation, so we take
// []conversation.Message directly — there is no cycle risk here, only with
// query/provider, which this file never imports.
//
// Typical wiring from the query layer:
//
//	backend := compact.NewCompactServiceBackend(svc, func() []conversation.Message {
//	    return thread.Messages()
//	}, "")
//	rc := compact.NewReactiveCompactor(backend, nil, budget, compact.ReactiveConfig{})
func NewCompactServiceBackend(
	svc *CompactService,
	messagesFn func() []conversation.Message,
	customInstructions string,
) ReactiveCompactorBackend {
	return &liveBackend{
		svc:          svc,
		messagesFn:   messagesFn,
		customPrompt: customInstructions,
	}
}

// liveBackend is the concrete adapter produced by NewCompactServiceBackend.
// Callers hold it through the ReactiveCompactorBackend interface.
type liveBackend struct {
	svc          *CompactService
	messagesFn   func() []conversation.Message
	customPrompt string
}

// CompactNow implements ReactiveCompactorBackend. It fetches the current
// messages via the provider closure and delegates to CompactService.Compact
// with isAuto=true — reactive recovery is a form of automatic compaction
// from the user's point of view, and should count toward the circuit
// breaker + failure tracker the service maintains.
func (b *liveBackend) CompactNow(ctx context.Context) (*CompactResult, error) {
	if b.svc == nil {
		return nil, ErrReactiveCompactDisabled
	}
	if b.messagesFn == nil {
		return nil, errors.New("reactive compact: messages provider is nil")
	}
	msgs := b.messagesFn()
	return b.svc.Compact(ctx, msgs, b.customPrompt, true)
}
