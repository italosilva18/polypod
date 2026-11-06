package compact

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/conversation"
)

// MicroCompactConfig configures lightweight per-tool-result compaction.
type MicroCompactConfig struct {
	// MaxToolResultTokens is the maximum token count for a single tool result
	// before it is truncated (default: 8000).
	MaxToolResultTokens int

	// KeepRecentTools is the number of most recent tool results to preserve
	// unmodified (default: 5).
	KeepRecentTools int

	// TimeGapThresholdMinutes triggers time-based microcompact when the gap
	// since the last assistant message exceeds this value (default: 30).
	TimeGapThresholdMinutes int

	// Enabled controls whether microcompact is active (default: true).
	Enabled bool
}

// DefaultMicroCompactConfig returns production-ready defaults.
func DefaultMicroCompactConfig() MicroCompactConfig {
	return MicroCompactConfig{
		MaxToolResultTokens:     8_000,
		KeepRecentTools:         5,
		TimeGapThresholdMinutes: 30,
		Enabled:                 true,
	}
}

// CompactableTools is the set of tool names whose results can be microcompacted.
var CompactableTools = map[string]bool{
	"Read":       true,
	"Bash":       true,
	"Grep":       true,
	"Glob":       true,
	"WebSearch":  true,
	"WebFetch":   true,
	"Edit":       true,
	"Write":      true,
	"MultiEdit":  true,
	"search":     true,
	"list_files": true,
}

// ClearedToolResultMessage is the placeholder for content-cleared tool results.
const ClearedToolResultMessage = "[Old tool result content cleared]"

// MicroCompactResult holds the outcome of a microcompact pass.
type MicroCompactResult struct {
	// Messages is the (possibly modified) message slice.
	Messages []conversation.Message

	// ToolsCleared is how many tool results were truncated or cleared.
	ToolsCleared int

	// TokensSaved is the estimated token savings.
	TokensSaved int

	// Trigger describes what caused the microcompact ("count", "time", "size", "none").
	Trigger string
}

// ToolResultInfo tracks a tool result found in the conversation for ordering.
type ToolResultInfo struct {
	// MessageIndex is the index in the messages slice.
	MessageIndex int

	// ToolName is the name of the tool that produced this result.
	ToolName string

	// ToolUseID links the result to its tool_use block.
	ToolUseID string

	// Tokens is the estimated token count for this result.
	Tokens int
}

// MicroCompactor performs lightweight, per-tool-result compaction.
// Unlike full compaction which summarizes entire conversation segments,
// microcompact truncates or clears individual tool outputs that are
// large and old enough to be safely compressed.
type MicroCompactor struct {
	config MicroCompactConfig
	logger *slog.Logger

	mu                sync.Mutex
	registeredToolIDs map[string]bool // set of tool_use_ids already processed
}

// NewMicroCompactor creates a new micro-compactor.
func NewMicroCompactor(cfg MicroCompactConfig, logger *slog.Logger) *MicroCompactor {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MaxToolResultTokens == 0 {
		cfg.MaxToolResultTokens = 8_000
	}
	if cfg.KeepRecentTools == 0 {
		cfg.KeepRecentTools = 5
	}
	if cfg.TimeGapThresholdMinutes == 0 {
		cfg.TimeGapThresholdMinutes = 30
	}
	return &MicroCompactor{
		config:            cfg,
		logger:            logger,
		registeredToolIDs: make(map[string]bool),
	}
}

// MicrocompactMessages performs a microcompact pass over the message history.
// It applies multiple strategies in priority order:
//
//  1. Time-based: if the gap since the last assistant message exceeds the
//     threshold, clear all but the most recent N tool results (the cache is cold).
//  2. Size-based: truncate individual tool results that exceed MaxToolResultTokens.
//  3. Count-based: if there are more compactable tool results than KeepRecentTools,
//     clear the oldest ones.
//
// Returns the (possibly modified) messages and metadata about what changed.
func (mc *MicroCompactor) MicrocompactMessages(messages []conversation.Message) MicroCompactResult {
	if !mc.config.Enabled || len(messages) == 0 {
		return MicroCompactResult{Messages: messages, Trigger: "none"}
	}

	// Strategy 1: Time-based trigger
	if result := mc.maybeTimeBasedMicrocompact(messages); result != nil {
		return *result
	}

	// Strategy 2+3: Size-based truncation and count-based clearing
	return mc.standardMicrocompact(messages)
}

// maybeTimeBasedMicrocompact checks if the gap since the last assistant message
// exceeds the configured threshold. If so, it content-clears all but the most
// recent N compactable tool results.
func (mc *MicroCompactor) maybeTimeBasedMicrocompact(messages []conversation.Message) *MicroCompactResult {
	if mc.config.TimeGapThresholdMinutes <= 0 {
		return nil
	}

	// Find the last assistant message timestamp
	var lastAssistantTime time.Time
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == conversation.RoleAssistant {
			lastAssistantTime = messages[i].Timestamp
			break
		}
	}
	if lastAssistantTime.IsZero() {
		return nil
	}

	gapMinutes := time.Since(lastAssistantTime).Minutes()
	if gapMinutes < float64(mc.config.TimeGapThresholdMinutes) {
		return nil
	}

	// Collect compactable tool results
	toolResults := mc.collectCompactableToolResults(messages)
	if len(toolResults) == 0 {
		return nil
	}

	// Keep the most recent N, clear the rest
	keepCount := mc.config.KeepRecentTools
	if keepCount < 1 {
		keepCount = 1
	}
	if keepCount >= len(toolResults) {
		return nil
	}

	toClear := toolResults[:len(toolResults)-keepCount]
	if len(toClear) == 0 {
		return nil
	}

	result, tokensSaved, cleared := mc.clearToolResults(messages, toClear)

	if cleared == 0 {
		return nil
	}

	mc.logger.Info("time-based microcompact triggered",
		"gap_minutes", int(gapMinutes),
		"threshold_minutes", mc.config.TimeGapThresholdMinutes,
		"tools_cleared", cleared,
		"tools_kept", len(toolResults)-cleared,
		"tokens_saved", tokensSaved,
	)

	mc.resetState()

	return &MicroCompactResult{
		Messages:     result,
		ToolsCleared: cleared,
		TokensSaved:  tokensSaved,
		Trigger:      "time",
	}
}

// standardMicrocompact applies size-based truncation and count-based clearing.
func (mc *MicroCompactor) standardMicrocompact(messages []conversation.Message) MicroCompactResult {
	toolResults := mc.collectCompactableToolResults(messages)

	if len(toolResults) == 0 {
		return MicroCompactResult{Messages: messages, Trigger: "none"}
	}

	totalTokensSaved := 0
	totalCleared := 0
	current := make([]conversation.Message, len(messages))
	copy(current, messages)

	// Size-based: truncate any single tool result exceeding the limit
	for _, tr := range toolResults {
		if tr.Tokens > mc.config.MaxToolResultTokens {
			msg := &current[tr.MessageIndex]
			originalTokens := estimateStringTokens(msg.Content)

			truncated := truncateToolResult(msg.Content, mc.config.MaxToolResultTokens)
			current[tr.MessageIndex] = conversation.Message{
				Role:      msg.Role,
				Content:   truncated,
				Timestamp: msg.Timestamp,
			}

			saved := originalTokens - estimateStringTokens(truncated)
			if saved > 0 {
				totalTokensSaved += saved
				totalCleared++
			}
		}
	}

	// Count-based: if we still have too many tool results, clear the oldest
	if len(toolResults) > mc.config.KeepRecentTools {
		toClear := toolResults[:len(toolResults)-mc.config.KeepRecentTools]
		current, savedTokens, cleared := mc.clearToolResults(current, toClear)
		totalTokensSaved += savedTokens
		totalCleared += cleared
		_ = current // reassigned above
	}

	trigger := "none"
	if totalCleared > 0 {
		trigger = "size"
		if len(toolResults) > mc.config.KeepRecentTools {
			trigger = "count"
		}
	}

	return MicroCompactResult{
		Messages:     current,
		ToolsCleared: totalCleared,
		TokensSaved:  totalTokensSaved,
		Trigger:      trigger,
	}
}

// collectCompactableToolResults scans messages for tool results that can be
// microcompacted. Results are returned in encounter order (oldest first).
func (mc *MicroCompactor) collectCompactableToolResults(messages []conversation.Message) []ToolResultInfo {
	var results []ToolResultInfo

	for i, msg := range messages {
		if msg.Role != conversation.RoleUser {
			continue
		}

		// Look for tool result patterns in the content
		toolName, toolID := extractToolResultInfo(msg.Content)
		if toolName == "" {
			continue
		}

		if !CompactableTools[toolName] {
			continue
		}

		results = append(results, ToolResultInfo{
			MessageIndex: i,
			ToolName:     toolName,
			ToolUseID:    toolID,
			Tokens:       estimateStringTokens(msg.Content),
		})
	}

	return results
}

// clearToolResults replaces the content of specified tool results with a
// cleared-content placeholder.
func (mc *MicroCompactor) clearToolResults(
	messages []conversation.Message,
	toClear []ToolResultInfo,
) ([]conversation.Message, int, int) {
	result := make([]conversation.Message, len(messages))
	copy(result, messages)

	tokensSaved := 0
	cleared := 0

	clearSet := make(map[int]bool)
	for _, tr := range toClear {
		clearSet[tr.MessageIndex] = true
	}

	for _, tr := range toClear {
		msg := &result[tr.MessageIndex]
		if msg.Content == ClearedToolResultMessage {
			continue // already cleared
		}

		originalTokens := estimateStringTokens(msg.Content)
		result[tr.MessageIndex] = conversation.Message{
			Role:      msg.Role,
			Content:   ClearedToolResultMessage,
			Timestamp: msg.Timestamp,
		}

		saved := originalTokens - estimateStringTokens(ClearedToolResultMessage)
		if saved > 0 {
			tokensSaved += saved
			cleared++
		}
	}

	return result, tokensSaved, cleared
}

// truncateToolResult truncates a tool result string to fit within the given
// token budget. Adds a truncation marker at the end.
func truncateToolResult(content string, maxTokens int) string {
	currentTokens := estimateStringTokens(content)
	if currentTokens <= maxTokens {
		return content
	}

	marker := "\n\n[... output truncated for context management]"
	// Approximate character budget from token budget (4 chars/token)
	charBudget := maxTokens*4 - len(marker)
	if charBudget < 100 {
		charBudget = 100
	}

	if charBudget >= len(content) {
		return content
	}

	// Try to break at a newline boundary for cleaner truncation
	truncated := content[:charBudget]
	if lastNewline := strings.LastIndex(truncated, "\n"); lastNewline > charBudget/2 {
		truncated = truncated[:lastNewline]
	}

	return truncated + marker
}

// extractToolResultInfo extracts tool name and ID from a message that looks
// like a tool result. This is a heuristic based on common patterns in the
// conversation format.
func extractToolResultInfo(content string) (toolName, toolID string) {
	// Look for patterns like [tool_result for ToolName (id: toolu_xxx)]
	// or structured tool result formats
	if strings.HasPrefix(content, ClearedToolResultMessage) {
		return "", ""
	}

	// Check for common tool result prefixes
	for name := range CompactableTools {
		prefix := fmt.Sprintf("[%s]", name)
		if strings.Contains(content, prefix) {
			return name, ""
		}
		// Also check for tool_result format
		if strings.Contains(content, fmt.Sprintf("tool_use_id: %s", name)) {
			return name, ""
		}
	}

	// Check for generic tool result indicators
	if strings.Contains(content, "tool_result") || strings.Contains(content, "tool_use_id") {
		return "unknown", ""
	}

	return "", ""
}

// resetState clears internal microcompact tracking state.
func (mc *MicroCompactor) resetState() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.registeredToolIDs = make(map[string]bool)
}

// EstimateMessageTokens estimates the token count for a slice of messages.
// Uses the same 4/3 conservative padding as EstimateTokens.
func EstimateMessageTokens(messages []conversation.Message) int {
	return EstimateTokens(messages)
}
