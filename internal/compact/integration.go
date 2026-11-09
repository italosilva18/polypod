package compact

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/contextwindow"
	"github.com/costa/polypod/internal/conversation"
)

// AutoCompactConfig configures the AutoCompactService that bridges the compact
// package with the contextwindow.Manager and the query/AI layer.
type AutoCompactConfig struct {
	// CtxWinMgr is the context window manager that tracks token usage and
	// fires threshold callbacks.
	CtxWinMgr *contextwindow.Manager

	// CompactFn is called to summarize a slice of message strings via an LLM.
	// It receives the context and the text content of the messages to compact,
	// and returns a single summary string.
	CompactFn func(ctx context.Context, messages []string) (string, error)

	// CompactService uses an existing summarizer and its configured hooks.
	// When nil, the service is built from CompactFn for existing callers.
	CompactService *CompactService

	// Logger for structured logging. Defaults to slog.Default().
	Logger *slog.Logger

	// Enabled controls whether auto-compaction is active.
	Enabled bool

	// Threshold is the fraction of the context window (0.0–1.0) at which
	// auto-compaction triggers. This overrides the contextwindow.Manager's
	// autoCompactThreshold when the service registers itself.
	// A value of 0 keeps the Manager's default (0.80).
	Threshold float64
}

// AutoCompactService connects the compact package to the context window
// manager and AI service, enabling automatic compaction of conversation
// history when the context window fills up.
type AutoCompactService struct {
	mu sync.Mutex

	ctxWinMgr *contextwindow.Manager
	compactFn func(ctx context.Context, messages []string) (string, error)
	logger    *slog.Logger
	enabled   bool
	threshold float64

	// Core compact service — uses a SummaryFunc adapter around compactFn.
	svc *CompactService

	// Statistics
	compactionsRun      int
	totalTokensSaved    int
	lastCompactTime     time.Time
	lastPreTokens       int
	lastPostTokens      int
	consecutiveFailures int
	lastFailureTime     time.Time
}

// NewAutoCompactService creates a service that registers itself as the
// auto-compact callback on the contextwindow.Manager. When triggered (either
// by the Manager callback or by an explicit Check), it compacts the supplied
// conversation history via the underlying CompactService.
func NewAutoCompactService(cfg AutoCompactConfig) *AutoCompactService {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	threshold := cfg.Threshold
	if threshold <= 0 || threshold > 1.0 {
		threshold = 0.80
	}

	// Build an underlying CompactService. We derive its config from the
	// Manager's limits so everything stays consistent.
	var compactCfg CompactConfig
	if cfg.CtxWinMgr != nil {
		limits := cfg.CtxWinMgr.Limits()
		compactCfg = CompactConfig{
			ContextWindowSize:  limits.MaxContextTokens,
			MaxOutputTokens:    limits.MaxOutputTokens,
			Enabled:            cfg.Enabled,
			AutoCompactEnabled: cfg.Enabled,
		}
	} else {
		compactCfg = DefaultConfig()
		compactCfg.Enabled = cfg.Enabled
		compactCfg.AutoCompactEnabled = cfg.Enabled
	}

	// Adapter: convert the simpler CompactFn ([]string → string) into the
	// SummaryFunc signature (systemPrompt, []Message, userPrompt → string).
	var summaryFn SummaryFunc
	if cfg.CompactFn != nil {
		summaryFn = func(ctx context.Context, _ string, msgs []conversation.Message, _ string) (string, error) {
			strs := make([]string, len(msgs))
			for i, m := range msgs {
				strs[i] = string(m.Role) + ": " + m.Content
			}
			return cfg.CompactFn(ctx, strs)
		}
	}

	svc := cfg.CompactService
	if svc == nil {
		svc = NewCompactService(compactCfg, summaryFn, logger)
	}

	acs := &AutoCompactService{
		ctxWinMgr: cfg.CtxWinMgr,
		compactFn: cfg.CompactFn,
		logger:    logger,
		enabled:   cfg.Enabled,
		threshold: threshold,
		svc:       svc,
	}

	// Register on the context window manager.
	if cfg.CtxWinMgr != nil {
		if cfg.Threshold > 0 && cfg.Threshold < 1.0 {
			// Override the manager's auto-compact threshold with our value.
			// Use the same value for the critical threshold but slightly higher.
			critical := cfg.Threshold + (1.0-cfg.Threshold)*0.5
			if critical > 0.99 {
				critical = 0.99
			}
			cfg.CtxWinMgr.SetThresholds(cfg.Threshold, critical)
		}

	}

	return acs
}

// Check examines the current token count and returns true if compaction is needed.
func (s *AutoCompactService) Check(currentTokens int) bool {
	return s.CheckWithCapacity(currentTokens, false)
}

// CheckWithCapacity also compacts when the conversation snapshot is about to
// evict old messages, even if the estimated token count is still small.
func (s *AutoCompactService) CheckWithCapacity(currentTokens int, atCapacity bool) bool {
	s.mu.Lock()
	enabled := s.enabled
	failures := s.consecutiveFailures
	lastCompact := s.lastCompactTime
	lastFailure := s.lastFailureTime
	s.mu.Unlock()

	if !enabled {
		return false
	}
	if s.ctxWinMgr != nil {
		s.ctxWinMgr.Update(0, currentTokens, 0)
	}
	if atCapacity {
		return true
	}
	if failures >= s.svc.config.MaxConsecutiveFailures && time.Since(lastFailure) < time.Minute {
		return false
	}
	if !lastCompact.IsZero() && time.Since(lastCompact) < s.svc.config.CooldownDuration {
		return false
	}
	// The explicit token estimate belongs to this session. A shared manager's
	// previous usage must not trigger compaction in another session.
	if s.ctxWinMgr != nil {
		limit := s.ctxWinMgr.Limits().MaxContextTokens
		return limit > 0 && currentTokens >= int(float64(limit)*s.threshold)
	}

	return currentTokens >= s.svc.AutoCompactThreshold()
}

// CompactHistory compacts the given conversation history. It runs the full
// Compact pipeline through the underlying CompactService and returns a new
// message slice with the older portion replaced by a summary.
//
// The returned messages are ready to be used as the new conversation history.
func (s *AutoCompactService) CompactHistory(
	ctx context.Context,
	history []conversation.Message,
) ([]conversation.Message, error) {
	s.mu.Lock()
	if !s.enabled {
		s.mu.Unlock()
		return history, nil
	}
	s.mu.Unlock()

	result, err := s.svc.Compact(ctx, history, "", true)
	if err != nil {
		s.mu.Lock()
		s.consecutiveFailures++
		s.lastFailureTime = time.Now()
		failures := s.consecutiveFailures
		s.mu.Unlock()

		s.logger.Warn("auto-compact history failed",
			"error", err,
			"consecutive_failures", failures,
		)
		return nil, err
	}

	// Build the compacted message list.
	compacted := BuildResultMessages(result)

	s.mu.Lock()
	s.compactionsRun++
	s.totalTokensSaved += result.PreCompactTokens - result.PostCompactTokens
	s.lastCompactTime = result.CompactedAt
	s.lastPreTokens = result.PreCompactTokens
	s.lastPostTokens = result.PostCompactTokens
	s.consecutiveFailures = 0
	s.lastFailureTime = time.Time{}
	s.mu.Unlock()

	s.logger.Info("auto-compact history succeeded",
		"pre_tokens", result.PreCompactTokens,
		"post_tokens", result.PostCompactTokens,
		"messages_removed", result.MessagesRemoved,
	)

	// Record the compacted context for status and the next turn.
	if s.ctxWinMgr != nil {
		s.ctxWinMgr.Update(0, result.PostCompactTokens, 0)
	}

	return compacted, nil
}

// Enable activates auto-compaction.
func (s *AutoCompactService) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = true
	s.svc.config.Enabled = true
	s.svc.config.AutoCompactEnabled = true
}

// Disable deactivates auto-compaction. Manual compaction via CompactHistory
// is still possible by re-enabling first.
func (s *AutoCompactService) Disable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
	s.svc.config.Enabled = false
	s.svc.config.AutoCompactEnabled = false
}

// IsEnabled returns whether auto-compaction is currently active.
func (s *AutoCompactService) IsEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// SetModel keeps the compaction threshold aligned with the active model.
func (s *AutoCompactService) SetModel(model string) {
	if s.ctxWinMgr != nil {
		s.ctxWinMgr.SetModel(model)
	}
}

// Stats returns compaction statistics as a map suitable for logging or
// debug endpoints.
func (s *AutoCompactService) Stats() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := map[string]any{
		"enabled":              s.enabled,
		"threshold":            s.threshold,
		"compactions_run":      s.compactionsRun,
		"total_tokens_saved":   s.totalTokensSaved,
		"consecutive_failures": s.consecutiveFailures,
	}

	if !s.lastCompactTime.IsZero() {
		stats["last_compact_time"] = s.lastCompactTime.Format(time.RFC3339)
		stats["last_pre_tokens"] = s.lastPreTokens
		stats["last_post_tokens"] = s.lastPostTokens
	}
	if !s.lastFailureTime.IsZero() {
		stats["last_failure_time"] = s.lastFailureTime.Format(time.RFC3339)
	}

	// Include context window stats if available.
	if s.ctxWinMgr != nil {
		stats["ctx_window"] = s.ctxWinMgr.Stats()
	}

	return stats
}

// CompactService returns the underlying CompactService for advanced usage
// (e.g., manual/partial compaction).
func (s *AutoCompactService) CompactService() *CompactService {
	return s.svc
}
