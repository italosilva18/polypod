package session

import (
	"fmt"
	"log/slog"
	"strings"
)

// CompactionConfig controls when and how context compaction happens.
type CompactionConfig struct {
	MaxTokens      int `yaml:"max_tokens"`      // Trigger compaction above this (default: 8000)
	TargetTokens   int `yaml:"target_tokens"`    // Target after compaction (default: 4000)
	PreserveRecent int `yaml:"preserve_recent"`  // Keep N most recent message pairs (default: 4)
}

// Message mirrors the conversation message type.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Compactor manages context window compression.
type Compactor struct {
	config CompactionConfig
	logger *slog.Logger
}

// NewCompactor creates a compactor with the given config.
func NewCompactor(cfg CompactionConfig, logger *slog.Logger) *Compactor {
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 8000
	}
	if cfg.TargetTokens == 0 {
		cfg.TargetTokens = 4000
	}
	if cfg.PreserveRecent == 0 {
		cfg.PreserveRecent = 4
	}
	return &Compactor{config: cfg, logger: logger}
}

// EstimateTokens gives a rough token count (~4 chars per token).
func EstimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)/4 + 4 // +4 for role/overhead
	}
	return total
}

// NeedsCompaction returns true if messages exceed max token limit.
func (c *Compactor) NeedsCompaction(messages []Message) bool {
	return EstimateTokens(messages) > c.config.MaxTokens
}

// CompactWithFocus compresses older messages with custom focus instructions.
func (c *Compactor) CompactWithFocus(messages []Message, focus string) ([]Message, string) {
	result, summary := c.Compact(messages)
	if focus != "" && summary != "" {
		// Replace generic summary with focus-aware prompt
		for i, m := range result {
			if m.Role == "system" && strings.HasPrefix(m.Content, "[Resumo da conversa anterior]") {
				result[i].Content = fmt.Sprintf("[Resumo da conversa anterior (foco: %s)]\n%s", focus, summary)
			}
		}
	}
	return result, summary
}

// Compact compresses older messages into a summary.
// Returns [system, summary, ...recent] messages.
func (c *Compactor) Compact(messages []Message) ([]Message, string) {
	if len(messages) <= c.config.PreserveRecent+1 {
		return messages, ""
	}

	// Separate system message
	var system *Message
	var rest []Message
	for _, m := range messages {
		if m.Role == "system" && system == nil {
			sys := m
			system = &sys
		} else {
			rest = append(rest, m)
		}
	}

	// Keep recent messages
	preserveCount := c.config.PreserveRecent * 2 // pairs (user+assistant)
	if preserveCount > len(rest) {
		preserveCount = len(rest)
	}

	toCompact := rest[:len(rest)-preserveCount]
	recent := rest[len(rest)-preserveCount:]

	if len(toCompact) == 0 {
		return messages, ""
	}

	// Build summary from compacted messages
	summary := buildSummary(toCompact)

	c.logger.Info("contexto compactado",
		"mensagens_removidas", len(toCompact),
		"mensagens_mantidas", len(recent),
		"tokens_antes", EstimateTokens(messages),
	)

	var result []Message
	if system != nil {
		result = append(result, *system)
	}
	result = append(result, Message{
		Role:    "system",
		Content: fmt.Sprintf("[Resumo da conversa anterior]\n%s", summary),
	})
	result = append(result, recent...)

	return result, summary
}

// BuildSummaryPrompt creates a prompt to ask the AI to summarize messages.
func BuildSummaryPrompt(messages []Message) string {
	var sb strings.Builder
	sb.WriteString("Resuma a conversa a seguir em portugues de forma concisa, preservando:\n")
	sb.WriteString("- Decisoes tomadas\n- Codigo discutido\n- Informacoes-chave\n- Contexto importante\n\n")
	sb.WriteString("Conversa:\n\n")
	for _, m := range messages {
		fmt.Fprintf(&sb, "[%s]: %s\n\n", m.Role, truncateForSummary(m.Content))
	}
	return sb.String()
}

func buildSummary(messages []Message) string {
	var topics []string
	for _, m := range messages {
		if m.Role == "user" {
			snippet := m.Content
			if len(snippet) > 100 {
				snippet = snippet[:100] + "..."
			}
			topics = append(topics, snippet)
		}
	}

	var sb strings.Builder
	sb.WriteString("A conversa anterior abordou os seguintes topicos:\n")
	for i, t := range topics {
		if i >= 10 {
			fmt.Fprintf(&sb, "- ... e mais %d mensagens\n", len(topics)-10)
			break
		}
		fmt.Fprintf(&sb, "- %s\n", t)
	}
	fmt.Fprintf(&sb, "\nTotal: %d mensagens compactadas.", len(messages))
	return sb.String()
}

func truncateForSummary(s string) string {
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
