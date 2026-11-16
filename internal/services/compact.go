// Package compact — Context Compaction extracted from Claude Code
// Sources: src/services/compact/

package compact

import (
	"fmt"
	"strings"
)

// CompactResult holds compaction result
type CompactResult struct {
	OriginalMessages int    `json:"original_messages"`
	NewMessages      int    `json:"new_messages"`
	Summary          string `json:"summary"`
	TokensSaved      int    `json:"tokens_saved"`
}

// Compactor compacts conversation context
type Compactor struct {
	maxMessages int
}

// NewCompactor creates new compactor
func NewCompactor(maxMessages int) *Compactor {
	return &Compactor{maxMessages: maxMessages}
}

// Compact compacts messages
func (c *Compactor) Compact(messages []string) *CompactResult {
	if len(messages) <= c.maxMessages {
		return &CompactResult{
			OriginalMessages: len(messages),
			NewMessages:      len(messages),
			Summary:          "No compaction needed",
			TokensSaved:      0,
		}
	}
	
	// Keep last N messages
	keepMessages := messages[len(messages)-c.maxMessages:]
	
	// Create summary of old messages
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Previous conversation (%d messages):\n", len(messages)-c.maxMessages))
	for i := 0; i < len(messages)-c.maxMessages; i++ {
		summary.WriteString(fmt.Sprintf("- Message %d\n", i+1))
	}
	
	tokensSaved := (len(messages) - c.maxMessages) * 100 // rough estimate
	
	return &CompactResult{
		OriginalMessages: len(messages),
		NewMessages:      c.maxMessages + 1, // +1 for summary
		Summary:          summary.String(),
		TokensSaved:      tokensSaved,
	}
}

// FormatCompactResult formats compaction result
func FormatCompactResult(result *CompactResult) string {
	var sb strings.Builder
	
	sb.WriteString("✅ **Conversation Compacted**:\n\n")
	sb.WriteString(fmt.Sprintf("Messages: %d → %d\n", result.OriginalMessages, result.NewMessages))
	sb.WriteString(fmt.Sprintf("Tokens saved: ~%d\n", result.TokensSaved))
	sb.WriteString(fmt.Sprintf("\n**Summary**:\n%s", result.Summary))
	
	return sb.String()
}
