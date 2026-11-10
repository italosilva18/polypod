package ai

import "github.com/costa/polypod/internal/contextwindow"

const (
	maxHistoryWindowTokens = 24_000
	minHistoryWindowTokens = 4_000
)

// historyWindowBudget triggers a persisted summary before the conversation
// occupies too much of a model prompt. If summarization fails, the original
// history is retained; an unsummarized turn must never be silently dropped.
func historyWindowBudget(model string) int {
	limits := contextwindow.WellKnownLimits(model)
	usable := limits.MaxContextTokens - limits.ReservedForOutput
	budget := usable / 3
	if budget > maxHistoryWindowTokens {
		budget = maxHistoryWindowTokens
	}
	if budget < minHistoryWindowTokens {
		budget = minHistoryWindowTokens
	}
	return budget
}
