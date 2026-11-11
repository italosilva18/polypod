package ai

import (
	"fmt"

	"github.com/costa/polypod/internal/contextwindow"
)

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

// promptConversationBudget also accounts for the already-built system prompt.
// Tool schemas and provider formatting have a separate conservative reserve.
func promptConversationBudget(model, systemPrompt string) (int, error) {
	limits := contextwindow.WellKnownLimits(model)
	const toolAndFramingReserve = 4096
	remaining := limits.MaxContextTokens - limits.ReservedForOutput - promptTokens(systemPrompt) - toolAndFramingReserve
	if remaining < 1024 {
		return 0, fmt.Errorf("instruções e contexto de sistema excedem a janela do modelo %s", model)
	}
	return min(historyWindowBudget(model), remaining), nil
}
