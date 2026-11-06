package query

import (
	"fmt"
	"time"
)

// Token budget management for the query loop.
// Migrated from Claude Code query/tokenBudget.ts.
//
// The token budget system provides automatic continuation when the model's
// response is cut short by max_output_tokens but the budget still has room.
// It also detects diminishing returns (the model repeating itself or
// producing very little new output) and stops early.

// Budget thresholds — tune these to control continuation behavior.
const (
	// completionThreshold is the fraction of budget consumed before we stop.
	// Below this ratio the model is auto-continued with a nudge message.
	completionThreshold = 0.9

	// diminishingThreshold is the minimum delta (in tokens) between
	// consecutive continuations. If two consecutive deltas are below this,
	// we declare diminishing returns and stop.
	diminishingThreshold = 500
)

// BudgetTracker accumulates state across continuations within a single turn.
type BudgetTracker struct {
	ContinuationCount  int
	LastDeltaTokens    int
	LastGlobalTurnToks int
	StartedAt          time.Time
}

// NewBudgetTracker creates a fresh tracker for a new turn.
func NewBudgetTracker() *BudgetTracker {
	return &BudgetTracker{
		StartedAt: time.Now(),
	}
}

// TokenBudgetAction is the decision from CheckTokenBudget.
type TokenBudgetAction int

const (
	// BudgetContinue means the model should be auto-continued.
	BudgetContinue TokenBudgetAction = iota
	// BudgetStop means the turn is done (either budget met or no budget).
	BudgetStop
)

// TokenBudgetDecision is the output of CheckTokenBudget.
type TokenBudgetDecision struct {
	Action          TokenBudgetAction
	NudgeMessage    string                 // only set when Action == BudgetContinue
	CompletionEvent *BudgetCompletionEvent // set on meaningful stop
}

// BudgetCompletionEvent carries analytics data when a budgeted turn completes.
type BudgetCompletionEvent struct {
	ContinuationCount  int
	Pct                int
	TurnTokens         int
	Budget             int
	DiminishingReturns bool
	DurationMs         int64
}

// CheckTokenBudget evaluates whether the current turn should auto-continue
// (the model hit max_output_tokens but the budget has room) or stop.
//
// Parameters:
//   - agentID: if non-empty, budgets don't apply (subagents manage their own).
//   - budget:  the total output-token budget for this turn. <=0 means no budget.
//   - globalTurnTokens: cumulative output tokens consumed this turn so far.
func CheckTokenBudget(
	tracker *BudgetTracker,
	agentID string,
	budget int,
	globalTurnTokens int,
) TokenBudgetDecision {
	// Subagents don't participate in budget tracking.
	if agentID != "" || budget <= 0 {
		return TokenBudgetDecision{Action: BudgetStop}
	}

	turnTokens := globalTurnTokens
	pct := 0
	if budget > 0 {
		pct = int(float64(turnTokens) / float64(budget) * 100)
	}
	deltaSinceLastCheck := globalTurnTokens - tracker.LastGlobalTurnToks

	isDiminishing := tracker.ContinuationCount >= 3 &&
		deltaSinceLastCheck < diminishingThreshold &&
		tracker.LastDeltaTokens < diminishingThreshold

	// Under completion threshold and not diminishing → continue.
	if !isDiminishing && float64(turnTokens) < float64(budget)*completionThreshold {
		tracker.ContinuationCount++
		tracker.LastDeltaTokens = deltaSinceLastCheck
		tracker.LastGlobalTurnToks = globalTurnTokens

		return TokenBudgetDecision{
			Action:       BudgetContinue,
			NudgeMessage: budgetContinuationMessage(pct, turnTokens, budget),
		}
	}

	// Emit a completion event if we had any continuations.
	if isDiminishing || tracker.ContinuationCount > 0 {
		return TokenBudgetDecision{
			Action: BudgetStop,
			CompletionEvent: &BudgetCompletionEvent{
				ContinuationCount:  tracker.ContinuationCount,
				Pct:                pct,
				TurnTokens:         turnTokens,
				Budget:             budget,
				DiminishingReturns: isDiminishing,
				DurationMs:         time.Since(tracker.StartedAt).Milliseconds(),
			},
		}
	}

	return TokenBudgetDecision{Action: BudgetStop}
}

// budgetContinuationMessage generates the nudge message injected as a
// synthetic user message when auto-continuing.
func budgetContinuationMessage(pct, turnTokens, budget int) string {
	return fmt.Sprintf(
		"[Token budget: %d%% used (%d / %d tokens). "+
			"Continue working — do not summarize or stop. "+
			"Resume directly from where you left off.]",
		pct, turnTokens, budget,
	)
}

// --- Token estimation and budget allocation ---

// EstimateMessageTokens returns a rough token count for a message.
// Uses the ~4 chars per token heuristic plus per-message overhead.
func EstimateMessageTokens(m Message) int {
	charCount := len(m.Content)
	if m.Role != "" {
		charCount += len(m.Role)
	}
	// ~4 chars per token + 4 tokens message overhead (role markers, separators)
	return charCount/4 + 4
}

// EstimateTokenCount returns a rough total token count for a message slice.
func EstimateTokenCount(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += EstimateMessageTokens(m)
	}
	return total
}

// ContextBudget describes how the context window is allocated.
type ContextBudget struct {
	// Total is the full context window size (e.g., 200000 for Claude).
	Total int

	// SystemPromptBudget is the allocation for the system prompt.
	SystemPromptBudget int

	// ToolDescriptionBudget is the allocation for tool definitions.
	ToolDescriptionBudget int

	// ConversationBudget is what remains for messages.
	ConversationBudget int

	// OutputReservation is the space reserved for the model's response.
	OutputReservation int
}

// AllocateContextBudget divides the total context window among components.
// The allocation strategy mirrors Claude Code's approach:
//   - System prompt: measured (plus 10% padding)
//   - Tool definitions: measured (plus 10% padding)
//   - Output reservation: maxOutputTokens or 20% of total
//   - Conversation: remainder
func AllocateContextBudget(
	totalContextWindow int,
	systemPromptTokens int,
	toolDescriptionTokens int,
	maxOutputTokens int,
) ContextBudget {
	// Pad measured sizes by 10%
	systemBudget := int(float64(systemPromptTokens) * 1.1)
	toolBudget := int(float64(toolDescriptionTokens) * 1.1)

	// Output reservation
	outputReserve := maxOutputTokens
	if outputReserve <= 0 {
		outputReserve = totalContextWindow / 5 // 20% default
	}

	conversationBudget := totalContextWindow - systemBudget - toolBudget - outputReserve
	if conversationBudget < 0 {
		conversationBudget = 0
	}

	return ContextBudget{
		Total:                 totalContextWindow,
		SystemPromptBudget:    systemBudget,
		ToolDescriptionBudget: toolBudget,
		ConversationBudget:    conversationBudget,
		OutputReservation:     outputReserve,
	}
}

// CompactionThresholdConfig controls when compaction triggers.
type CompactionThresholdConfig struct {
	// WarnRatio is the fraction of conversation budget that triggers a warning.
	// Default: 0.75
	WarnRatio float64

	// CompactRatio is the fraction that triggers automatic compaction.
	// Default: 0.90
	CompactRatio float64

	// BlockRatio is the fraction that blocks the query until /compact runs.
	// Default: 0.95
	BlockRatio float64
}

// DefaultCompactionThreshold returns the standard thresholds.
func DefaultCompactionThreshold() CompactionThresholdConfig {
	return CompactionThresholdConfig{
		WarnRatio:    0.75,
		CompactRatio: 0.90,
		BlockRatio:   0.95,
	}
}

// TokenWarningState indicates how close we are to context limits.
type TokenWarningState struct {
	// IsAtWarningLimit is true when conversation tokens exceed the warn threshold.
	IsAtWarningLimit bool

	// IsAtCompactLimit is true when autocompact should trigger.
	IsAtCompactLimit bool

	// IsAtBlockingLimit is true when the query should be blocked.
	IsAtBlockingLimit bool

	// UsageRatio is the current fraction of the conversation budget used.
	UsageRatio float64
}

// CalculateTokenWarningState evaluates the current token usage against thresholds.
func CalculateTokenWarningState(
	conversationTokens int,
	budget ContextBudget,
	thresholds CompactionThresholdConfig,
) TokenWarningState {
	if budget.ConversationBudget <= 0 {
		return TokenWarningState{IsAtBlockingLimit: true, UsageRatio: 1.0}
	}

	ratio := float64(conversationTokens) / float64(budget.ConversationBudget)

	return TokenWarningState{
		IsAtWarningLimit:  ratio >= thresholds.WarnRatio,
		IsAtCompactLimit:  ratio >= thresholds.CompactRatio,
		IsAtBlockingLimit: ratio >= thresholds.BlockRatio,
		UsageRatio:        ratio,
	}
}

// NeedsCompaction returns true if the message history exceeds the autocompact
// threshold for the given context budget.
func NeedsCompaction(messages []Message, budget ContextBudget, thresholds CompactionThresholdConfig) bool {
	tokens := EstimateTokenCount(messages)
	state := CalculateTokenWarningState(tokens, budget, thresholds)
	return state.IsAtCompactLimit
}

// ToolResultBudget defines limits for individual tool result sizes.
type ToolResultBudget struct {
	// MaxCharsPerResult is the maximum character count for a single tool result.
	// Results exceeding this are truncated with a marker.
	MaxCharsPerResult int

	// MaxTotalChars is the maximum total chars across all tool results in a turn.
	MaxTotalChars int
}

// DefaultToolResultBudget returns sensible defaults.
func DefaultToolResultBudget() ToolResultBudget {
	return ToolResultBudget{
		MaxCharsPerResult: 100_000, // ~25k tokens
		MaxTotalChars:     500_000, // ~125k tokens
	}
}

// TruncateToolResult truncates a tool result string if it exceeds the budget.
func TruncateToolResult(content string, maxChars int) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	// Keep first 60% and last 20%, insert truncation marker
	headLen := int(float64(maxChars) * 0.6)
	tailLen := int(float64(maxChars) * 0.2)

	head := content[:headLen]
	tail := content[len(content)-tailLen:]
	removed := len(content) - headLen - tailLen

	return fmt.Sprintf(
		"%s\n\n[... %d characters truncated for context budget ...]\n\n%s",
		head, removed, tail,
	)
}
