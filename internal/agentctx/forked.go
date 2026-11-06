package agentctx

import (
	"context"
	"sync"

	"github.com/costa/polypod/internal/types"
)

// ForkedAgentConfig holds parameters for creating a forked agent context.
// Forked agents share the parent's prompt cache for efficiency but have
// isolated mutable state to prevent interference with the main loop.
type ForkedAgentConfig struct {
	// ForkLabel identifies this fork for analytics (e.g. "session_memory", "supervisor").
	ForkLabel string

	// QuerySource identifies the source of the fork query.
	QuerySource string

	// MaxOutputTokens caps the output tokens. CAUTION: changing this breaks
	// prompt cache sharing (thinking config is part of the cache key).
	MaxOutputTokens int

	// MaxTurns limits the number of API round-trips.
	MaxTurns int

	// SkipTranscript skips sidechain transcript recording.
	SkipTranscript bool

	// SkipCacheWrite skips writing new prompt cache entries on the last message.
	SkipCacheWrite bool

	// ParentDepth is the query tracking depth of the parent context.
	ParentDepth int
}

// ForkedAgentContext holds isolated state for a sub-agent execution.
// It clones key fields from the parent to maintain cache-safe parameters
// while isolating mutable state.
type ForkedAgentContext struct {
	// Config contains the fork configuration.
	Config ForkedAgentConfig

	// AgentCtx is the agent identity context for this fork.
	AgentCtx *AgentContext

	// Context is the Go context with cancellation.
	Context context.Context

	// Cancel cancels this forked context.
	Cancel context.CancelFunc

	// mu protects mutable fields.
	mu sync.Mutex

	// tokenBudget is the allocated token budget for this fork.
	tokenBudget int

	// tokensUsed tracks tokens consumed by this fork.
	tokensUsed ForkedUsage

	// messages stores output messages from the fork.
	messages []ForkedMessage

	// cleaned indicates if CleanupForkedContext has been called.
	cleaned bool
}

// ForkedUsage tracks token usage across all API calls in a forked agent loop.
type ForkedUsage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// ForkedMessage represents a message produced by a forked agent.
type ForkedMessage struct {
	Type    string // "assistant", "user", "progress"
	Content string
	UUID    string
}

// PrepareForkedContext creates an isolated context for sub-agent execution.
// The returned ForkedAgentContext:
//  1. Has its own cancellation (linked to parent so parent abort propagates)
//  2. Has a new AgentContext with incremented depth
//  3. Tracks usage independently from the parent
func PrepareForkedContext(
	parentCtx context.Context,
	parentSessionID types.SessionID,
	cfg ForkedAgentConfig,
) *ForkedAgentContext {
	ctx, cancel := context.WithCancel(parentCtx)

	// Create a new agent context for this fork with incremented depth.
	agentCtx := &AgentContext{
		AgentID:      types.NewAgentID(cfg.ForkLabel),
		SessionID:    parentSessionID,
		AgentType:    AgentTypeSubagent,
		SubagentName: cfg.ForkLabel,
		IsBuiltIn:    true,
		Depth:        cfg.ParentDepth + 1,
		TokenBudget:  cfg.MaxOutputTokens,
	}

	// Attach the agent context to the Go context.
	ctx = SetAgentContext(ctx, agentCtx)

	return &ForkedAgentContext{
		Config:      cfg,
		AgentCtx:    agentCtx,
		Context:     ctx,
		Cancel:      cancel,
		tokenBudget: cfg.MaxOutputTokens,
	}
}

// AddUsage accumulates token usage from an API response into the fork's total.
func (fc *ForkedAgentContext) AddUsage(usage ForkedUsage) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.tokensUsed.InputTokens += usage.InputTokens
	fc.tokensUsed.OutputTokens += usage.OutputTokens
	fc.tokensUsed.CacheReadInputTokens += usage.CacheReadInputTokens
	fc.tokensUsed.CacheCreationInputTokens += usage.CacheCreationInputTokens
}

// TotalUsage returns a copy of the accumulated usage for this fork.
func (fc *ForkedAgentContext) TotalUsage() ForkedUsage {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.tokensUsed
}

// AddMessage records an output message from the forked agent.
func (fc *ForkedAgentContext) AddMessage(msg ForkedMessage) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.messages = append(fc.messages, msg)
}

// Messages returns a copy of all output messages.
func (fc *ForkedAgentContext) Messages() []ForkedMessage {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	result := make([]ForkedMessage, len(fc.messages))
	copy(result, fc.messages)
	return result
}

// ExtractResultText extracts the text content from the last assistant message
// in the forked agent's output. Returns defaultText if no assistant message found.
func ExtractResultText(messages []ForkedMessage, defaultText string) string {
	if defaultText == "" {
		defaultText = "Execution completed"
	}

	// Walk backwards to find the last assistant message.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Type == "assistant" && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return defaultText
}

// ForkedTokenBudget allocates a token budget for a forked agent.
// The budget is derived from the parent's remaining budget with a cap.
//
//   - parentRemainingTokens: tokens the parent has left
//   - maxForkedTokens: hard cap for this fork (0 = no cap)
//   - ratio: fraction of parent budget to allocate (0.0-1.0, default 0.5)
func ForkedTokenBudget(parentRemainingTokens int, maxForkedTokens int, ratio float64) int {
	if ratio <= 0 || ratio > 1 {
		ratio = 0.5
	}

	budget := int(float64(parentRemainingTokens) * ratio)

	// Apply hard cap if specified.
	if maxForkedTokens > 0 && budget > maxForkedTokens {
		budget = maxForkedTokens
	}

	// Minimum sensible budget.
	const minBudget = 1024
	if budget < minBudget {
		budget = minBudget
	}

	return budget
}

// CleanupForkedContext releases resources held by the forked context.
// Safe to call multiple times. Cancels the context and clears message buffers.
func CleanupForkedContext(fc *ForkedAgentContext) {
	if fc == nil {
		return
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.cleaned {
		return
	}
	fc.cleaned = true

	// Cancel the context to release goroutines.
	fc.Cancel()

	// Clear the message buffer to free memory.
	fc.messages = nil
}

// CacheHitRate calculates the prompt cache hit rate for this fork.
// Returns 0.0 if no input tokens were processed.
func (fc *ForkedAgentContext) CacheHitRate() float64 {
	usage := fc.TotalUsage()
	totalInput := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if totalInput == 0 {
		return 0.0
	}
	return float64(usage.CacheReadInputTokens) / float64(totalInput)
}
