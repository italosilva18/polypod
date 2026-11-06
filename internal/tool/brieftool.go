package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/types"
)

// BriefMessage represents a single message for brief generation.
type BriefMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	ToolName string `json:"tool_name,omitempty"`
}

// BriefConfig controls how the brief is generated.
type BriefConfig struct {
	MaxLength        int    `json:"max_length"`
	IncludeToolCalls bool   `json:"include_tool_calls"`
	Format           string `json:"format"` // "text", "markdown", "json"
}

// DefaultBriefConfig returns a BriefConfig with sensible defaults.
func DefaultBriefConfig() BriefConfig {
	return BriefConfig{
		MaxLength:        500,
		IncludeToolCalls: false,
		Format:           "text",
	}
}

// briefStats holds computed conversation statistics.
type briefStats struct {
	TotalMessages     int      `json:"total_messages"`
	UserMessages      int      `json:"user_messages"`
	AssistantMessages int      `json:"assistant_messages"`
	ToolCalls         int      `json:"tool_calls"`
	KeyTopics         []string `json:"key_topics"`
	ToolsUsed         []string `json:"tools_used,omitempty"`
	TokenEstimate     int      `json:"token_estimate"`
}

// GenerateBrief creates a concise summary of the conversation messages.
func GenerateBrief(messages []BriefMessage, cfg BriefConfig) string {
	if cfg.MaxLength <= 0 {
		cfg.MaxLength = 500
	}
	if cfg.Format == "" {
		cfg.Format = "text"
	}

	stats := computeStats(messages)

	switch cfg.Format {
	case "json":
		return formatBriefJSON(stats, cfg)
	case "markdown":
		return formatBriefMarkdown(stats, cfg)
	default:
		return formatBriefText(stats, cfg)
	}
}

// computeStats extracts statistics from the message list.
func computeStats(messages []BriefMessage) briefStats {
	var stats briefStats
	stats.TotalMessages = len(messages)

	toolSet := make(map[string]struct{})
	var userContents []string

	for _, m := range messages {
		switch m.Role {
		case "user":
			stats.UserMessages++
			if m.Content != "" {
				userContents = append(userContents, m.Content)
			}
		case "assistant":
			stats.AssistantMessages++
		}
		if m.ToolName != "" {
			stats.ToolCalls++
			toolSet[m.ToolName] = struct{}{}
		}
	}

	// Key topics: first 3 user messages, truncated.
	topicLimit := 3
	if len(userContents) < topicLimit {
		topicLimit = len(userContents)
	}
	for i := 0; i < topicLimit; i++ {
		topic := userContents[i]
		if len(topic) > 80 {
			topic = topic[:80] + "..."
		}
		stats.KeyTopics = append(stats.KeyTopics, topic)
	}

	// Deduplicated tools list.
	for name := range toolSet {
		stats.ToolsUsed = append(stats.ToolsUsed, name)
	}

	// Token estimate: ~4 chars per token is a common rough heuristic.
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	stats.TokenEstimate = totalChars / 4
	if stats.TokenEstimate == 0 && totalChars > 0 {
		stats.TokenEstimate = 1
	}

	return stats
}

func formatBriefText(stats briefStats, cfg BriefConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conversation Brief\n")
	fmt.Fprintf(&b, "Messages: %d total (%d user, %d assistant)\n",
		stats.TotalMessages, stats.UserMessages, stats.AssistantMessages)
	fmt.Fprintf(&b, "Tool calls: %d\n", stats.ToolCalls)
	fmt.Fprintf(&b, "Token estimate: ~%d\n", stats.TokenEstimate)

	if len(stats.KeyTopics) > 0 {
		fmt.Fprintf(&b, "Key topics:\n")
		for _, t := range stats.KeyTopics {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}

	if cfg.IncludeToolCalls && len(stats.ToolsUsed) > 0 {
		fmt.Fprintf(&b, "Tools used: %s\n", strings.Join(stats.ToolsUsed, ", "))
	}

	return truncate(b.String(), cfg.MaxLength)
}

func formatBriefMarkdown(stats briefStats, cfg BriefConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Conversation Brief\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|--------|-------|\n")
	fmt.Fprintf(&b, "| Total messages | %d |\n", stats.TotalMessages)
	fmt.Fprintf(&b, "| User messages | %d |\n", stats.UserMessages)
	fmt.Fprintf(&b, "| Assistant messages | %d |\n", stats.AssistantMessages)
	fmt.Fprintf(&b, "| Tool calls | %d |\n", stats.ToolCalls)
	fmt.Fprintf(&b, "| Token estimate | ~%d |\n\n", stats.TokenEstimate)

	if len(stats.KeyTopics) > 0 {
		fmt.Fprintf(&b, "### Key Topics\n\n")
		for _, t := range stats.KeyTopics {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		b.WriteString("\n")
	}

	if cfg.IncludeToolCalls && len(stats.ToolsUsed) > 0 {
		fmt.Fprintf(&b, "### Tools Used\n\n")
		for _, t := range stats.ToolsUsed {
			fmt.Fprintf(&b, "- `%s`\n", t)
		}
		b.WriteString("\n")
	}

	return truncate(b.String(), cfg.MaxLength)
}

func formatBriefJSON(stats briefStats, cfg BriefConfig) string {
	out := stats
	if !cfg.IncludeToolCalls {
		out.ToolsUsed = nil
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return truncate(string(data), cfg.MaxLength)
}

// truncate ensures the output does not exceed maxLen bytes.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// briefMessagesFn is the package-level closure for retrieving conversation
// messages. Set via SetBriefMessagesFn during wiring.
var briefMessagesFn func() []BriefMessage

// SetBriefMessagesFn sets the closure used by BriefTool to access conversation
// messages at execution time.
func SetBriefMessagesFn(fn func() []BriefMessage) {
	briefMessagesFn = fn
}

// NewBriefTool creates the conversation brief/summary tool.
func NewBriefTool() types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return "brief" },
		DescriptionFn: func() string {
			return "Generate a brief/summary of the current conversation including message counts, key topics, tools used, and token estimates."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"max_length": map[string]any{
						"type":        "integer",
						"description": "Maximum length of the output in characters (default: 500).",
					},
					"format": map[string]any{
						"type":        "string",
						"enum":        []string{"text", "markdown", "json"},
						"description": "Output format (default: text).",
					},
					"include_tools": map[string]any{
						"type":        "boolean",
						"description": "Whether to include the list of tools used (default: false).",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if briefMessagesFn == nil {
				return types.ToolResult{
					IsError: true,
					Content: "brief tool not wired: no message provider configured",
				}, nil
			}

			cfg := DefaultBriefConfig()

			if v, ok := args["max_length"]; ok {
				switch n := v.(type) {
				case float64:
					cfg.MaxLength = int(n)
				case int:
					cfg.MaxLength = n
				}
			}
			if v, ok := args["format"].(string); ok && v != "" {
				cfg.Format = v
			}
			if v, ok := args["include_tools"].(bool); ok {
				cfg.IncludeToolCalls = v
			}

			messages := briefMessagesFn()
			summary := GenerateBrief(messages, cfg)
			return types.ToolResult{Content: summary}, nil
		},
	})
}
