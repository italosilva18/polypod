package ai

import (
	"fmt"

	"github.com/costa/polypod/internal/contextwindow"
)

const (
	maxHistoryWindowTokens   = 24_000
	maxHistoryWindowTokens1M = 120_000
	minHistoryWindowTokens   = 4_000
)

// historyWindowBudget triggers a persisted summary before the conversation
// occupies too much of a model prompt. Models with >=500K token context windows
// (such as qwen3-coder-plus, qwen3.7-plus, gemini-2.5-pro, gpt-4.1) receive a
// scaled 120K history window so long coding sessions are not compacted early.
func historyWindowBudget(model string) int {
	limits := contextwindow.WellKnownLimits(model)
	usable := limits.MaxContextTokens - limits.ReservedForOutput
	budget := usable / 3
	capTokens := maxHistoryWindowTokens
	if limits.MaxContextTokens >= 500_000 {
		capTokens = maxHistoryWindowTokens1M
	}
	if budget > capTokens {
		budget = capTokens
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
