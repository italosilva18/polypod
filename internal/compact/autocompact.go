package compact

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/conversation"
)

// TokenWarningState describes how close the context window is to its limits.
type TokenWarningState struct {
	// PercentLeft is the percentage of context window remaining (0-100).
	PercentLeft int

	// IsAboveWarningThreshold indicates a soft warning should be displayed.
	IsAboveWarningThreshold bool

	// IsAboveErrorThreshold indicates the context is critically full.
	IsAboveErrorThreshold bool

	// IsAboveAutoCompactThreshold indicates auto-compaction should trigger.
	IsAboveAutoCompactThreshold bool

	// IsAtBlockingLimit indicates no more messages can be added.
	IsAtBlockingLimit bool
}

// Buffer constants matching the TypeScript original.
const (
	WarningThresholdBufferTokens = 20_000
	ErrorThresholdBufferTokens   = 20_000
	ManualCompactBufferTokens    = 3_000
)

// AutoCompactTracker tracks auto-compaction state across turns in a conversation.
type AutoCompactTracker struct {
	mu sync.Mutex

	// Compacted indicates whether compaction happened in the current chain.
	Compacted bool

	// TurnCounter counts turns since the last compaction.
	TurnCounter int

	// TurnID is a unique identifier for the current turn.
	TurnID string

	// ConsecutiveFailures counts consecutive auto-compact failures.
	// Reset on success. Used as a circuit breaker.
	ConsecutiveFailures int
}

// NewAutoCompactTracker creates a new tracker.
func NewAutoCompactTracker() *AutoCompactTracker {
	return &AutoCompactTracker{}
}

// RecordSuccess resets the failure counter and marks compacted.
func (t *AutoCompactTracker) RecordSuccess() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Compacted = true
	t.ConsecutiveFailures = 0
	t.TurnCounter = 0
}

// RecordFailure increments the failure counter.
func (t *AutoCompactTracker) RecordFailure() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ConsecutiveFailures++
}

// IncrementTurn advances the turn counter.
func (t *AutoCompactTracker) IncrementTurn(turnID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TurnCounter++
	t.TurnID = turnID
}

// IsCircuitBroken returns true if the failure circuit breaker has tripped.
func (t *AutoCompactTracker) IsCircuitBroken(maxFailures int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ConsecutiveFailures >= maxFailures
}

// AutoCompactor monitors context window usage and triggers compaction
// when the threshold is exceeded. It integrates with the CompactService
// for the actual compaction work.
type AutoCompactor struct {
	service *CompactService
	logger  *slog.Logger

	mu              sync.Mutex
	lastCompactTime time.Time
	tracker         *AutoCompactTracker
}

// NewAutoCompactor creates a new auto-compactor wrapping the given service.
func NewAutoCompactor(service *CompactService, logger *slog.Logger) *AutoCompactor {
	if logger == nil {
		logger = slog.Default()
	}
	return &AutoCompactor{
		service: service,
		logger:  logger,
		tracker: NewAutoCompactTracker(),
	}
}

// Tracker returns the auto-compact tracking state.
func (ac *AutoCompactor) Tracker() *AutoCompactTracker {
	return ac.tracker
}

// CalculateTokenWarningState computes the current context window pressure.
func (ac *AutoCompactor) CalculateTokenWarningState(tokenUsage int) TokenWarningState {
	autoThreshold := ac.service.AutoCompactThreshold()

	threshold := autoThreshold
	if !ac.service.config.AutoCompactEnabled {
		threshold = ac.service.EffectiveContextWindow()
	}

	percentLeft := 0
	if threshold > 0 {
		percentLeft = max(0, ((threshold-tokenUsage)*100)/threshold)
	}

	warningThreshold := threshold - WarningThresholdBufferTokens
	errorThreshold := threshold - ErrorThresholdBufferTokens

	effectiveWindow := ac.service.EffectiveContextWindow()
	blockingLimit := effectiveWindow - ManualCompactBufferTokens

	return TokenWarningState{
		PercentLeft:                 percentLeft,
		IsAboveWarningThreshold:     tokenUsage >= warningThreshold,
		IsAboveErrorThreshold:       tokenUsage >= errorThreshold,
		IsAboveAutoCompactThreshold: ac.service.config.AutoCompactEnabled && tokenUsage >= autoThreshold,
		IsAtBlockingLimit:           tokenUsage >= blockingLimit,
	}
}

// ShouldAutoCompact determines whether auto-compaction should trigger for
// the given messages. It checks:
//   - Whether auto-compact is enabled
//   - Whether the circuit breaker has tripped
//   - Whether the cooldown period has elapsed
//   - Whether the token count exceeds the threshold
func (ac *AutoCompactor) ShouldAutoCompact(messages []conversation.Message) bool {
	if !ac.service.config.Enabled || !ac.service.config.AutoCompactEnabled {
		return false
	}

	// Circuit breaker
	if ac.tracker.IsCircuitBroken(ac.service.config.MaxConsecutiveFailures) {
		ac.logger.Debug("auto-compact skipped: circuit breaker tripped",
			"consecutive_failures", ac.tracker.ConsecutiveFailures,
		)
		return false
	}

	// Cooldown
	ac.mu.Lock()
	elapsed := time.Since(ac.lastCompactTime)
	ac.mu.Unlock()
	if elapsed < ac.service.config.CooldownDuration && ac.tracker.Compacted {
		ac.logger.Debug("auto-compact skipped: cooldown active",
			"elapsed", elapsed,
			"cooldown", ac.service.config.CooldownDuration,
		)
		return false
	}

	tokenCount := EstimateTokens(messages)
	threshold := ac.service.AutoCompactThreshold()

	ac.logger.Debug("auto-compact check",
		"tokens", tokenCount,
		"threshold", threshold,
		"effective_window", ac.service.EffectiveContextWindow(),
	)

	return tokenCount >= threshold
}

// AutoCompactIfNeeded checks whether auto-compaction should trigger and
// performs it if so. Returns the result if compaction happened, nil otherwise.
//
// The querySource parameter helps guard against recursive compaction — if
// the source is "compact" or "session_memory", compaction is skipped to
// prevent deadlock in forked agent scenarios.
func (ac *AutoCompactor) AutoCompactIfNeeded(
	ctx context.Context,
	messages []conversation.Message,
	querySource string,
) (*CompactResult, error) {
	if !ac.service.config.Enabled {
		return nil, nil
	}

	// Guard against recursive compaction
	switch querySource {
	case "compact", "session_memory":
		return nil, nil
	}

	if !ac.ShouldAutoCompact(messages) {
		return nil, nil
	}

	ac.logger.Info("auto-compact triggered",
		"messages", len(messages),
		"tokens", EstimateTokens(messages),
		"threshold", ac.service.AutoCompactThreshold(),
		"turn_counter", ac.tracker.TurnCounter,
	)

	result, err := ac.service.Compact(ctx, messages, "", true)
	if err != nil {
		ac.tracker.RecordFailure()
		ac.logger.Warn("auto-compact failed",
			"error", err,
			"consecutive_failures", ac.tracker.ConsecutiveFailures,
		)
		return nil, fmt.Errorf("auto-compact: %w", err)
	}

	ac.tracker.RecordSuccess()

	ac.mu.Lock()
	ac.lastCompactTime = time.Now()
	ac.mu.Unlock()

	return result, nil
}

// ResetTracker resets the auto-compact tracking state. Useful when
// the conversation is manually compacted or messages are otherwise pruned.
func (ac *AutoCompactor) ResetTracker() {
	ac.mu.Lock()
	ac.lastCompactTime = time.Time{}
	ac.mu.Unlock()
	ac.tracker.mu.Lock()
	ac.tracker.Compacted = false
	ac.tracker.TurnCounter = 0
	ac.tracker.ConsecutiveFailures = 0
	ac.tracker.mu.Unlock()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
