package memory

import (
	"context"
	"strings"
	"time"
)

// Memory represents a persistent memory entry.
type Memory struct {
	ID        int64
	Topic     string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time

	// Enhanced fields
	Tags       []string `json:"tags,omitempty"`       // categorization tags
	Priority   int      `json:"priority,omitempty"`   // 0=normal, 1=high, 2=critical
	SessionID  string   `json:"session_id,omitempty"` // session-scoped memories
	Source     string   `json:"source,omitempty"`     // "user", "auto_extracted", "hook"
	Confidence float64  `json:"confidence,omitempty"` // 0.0-1.0 for auto-extracted
}

// IsRelevant returns true if the memory matches a query with relevance scoring.
func (m *Memory) IsRelevant(query string) (bool, float64) {
	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(m.Content)
	topicLower := strings.ToLower(m.Topic)

	score := 0.0

	// Topic match (highest weight)
	if strings.Contains(topicLower, queryLower) {
		score += 0.6
	} else if strings.Contains(queryLower, topicLower) {
		score += 0.4
	}

	// Content match
	if strings.Contains(contentLower, queryLower) {
		score += 0.4
	}

	// Word overlap
	queryWords := strings.Fields(queryLower)
	contentWords := strings.Fields(contentLower)
	matches := 0
	for _, qw := range queryWords {
		for _, cw := range contentWords {
			if qw == cw && len(qw) > 2 {
				matches++
				break
			}
		}
	}
	if len(queryWords) > 0 {
		score += float64(matches) / float64(len(queryWords)) * 0.3
	}

	// Tag match
	for _, tag := range m.Tags {
		tagLower := strings.ToLower(tag)
		if strings.Contains(queryLower, tagLower) {
			score += 0.2
		}
	}

	// Priority boost
	if m.Priority > 0 {
		score += float64(m.Priority) * 0.1
	}

	return score >= 0.2, score
}

// Store is the interface for memory persistence.
type Store interface {
	Save(ctx context.Context, topic, content string) error
	SaveWithMeta(ctx context.Context, mem Memory) error
	Get(ctx context.Context, topic string) (*Memory, error)
	Search(ctx context.Context, query string) ([]Memory, error)
	SearchRelevant(ctx context.Context, query string, maxResults int, minScore float64) ([]Memory, error)
	List(ctx context.Context) ([]Memory, error)
	Delete(ctx context.Context, topic string) error
}

// SearchResult is a memory with its relevance score.
type SearchResult struct {
	Memory Memory
	Score  float64
}

// AutoInjectConfig controls automatic memory injection.
type AutoInjectConfig struct {
	Enabled     bool
	MaxMemories int
	MinScore    float64
	Prefix      string // prefix for injected context
}

// DefaultAutoInjectConfig returns sensible defaults.
func DefaultAutoInjectConfig() AutoInjectConfig {
	return AutoInjectConfig{
		Enabled:     true,
		MaxMemories: 3,
		MinScore:    0.3,
		Prefix:      "MEMORIA RELEVANTE:",
	}
}

// BuildMemoryContext builds a context string from relevant memories.
func BuildMemoryContext(ctx context.Context, store Store, query string, config AutoInjectConfig) string {
	if !config.Enabled || store == nil {
		return ""
	}

	memories, err := store.SearchRelevant(ctx, query, config.MaxMemories, config.MinScore)
	if err != nil || len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(config.Prefix)
	sb.WriteString("\n")
	for _, mem := range memories {
		sb.WriteString("- ")
		if mem.Topic != "" {
			sb.WriteString("[")
			sb.WriteString(mem.Topic)
			sb.WriteString("] ")
		}
		sb.WriteString(mem.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ExtractDecision extracts a decision from conversation for auto-memory.
func ExtractDecision(content string) *Memory {
	// Simple heuristic: look for decision patterns
	lower := strings.ToLower(content)
	decisionPatterns := []string{
		"decidi", "vamos usar", "a decisao foi", "optamos por",
		"preferimos", "escolhemos", "configurado como",
	}

	for _, pattern := range decisionPatterns {
		if strings.Contains(lower, pattern) {
			return &Memory{
				Topic:      "decisao_auto",
				Content:    content,
				Source:     "auto_extracted",
				Confidence: 0.6,
				Tags:       []string{"decisao", "auto"},
			}
		}
	}
	return nil
}

// ExtractPattern extracts a pattern/convention from conversation.
func ExtractPattern(content string) *Memory {
	lower := strings.ToLower(content)
	patterns := []string{
		"padrao", "convencao", "sempre use", "nunca use",
		"melhor pratica", "regra:", "importante:",
	}

	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return &Memory{
				Topic:      "padrao_auto",
				Content:    content,
				Source:     "auto_extracted",
				Confidence: 0.5,
				Tags:       []string{"padrao", "convencao", "auto"},
			}
		}
	}
	return nil
}
