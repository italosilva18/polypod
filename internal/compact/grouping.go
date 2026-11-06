package compact

import (
	"github.com/costa/polypod/internal/conversation"
)

// GroupMessagesByAPIRound groups messages at API-round boundaries.
//
// A boundary fires when a new assistant response begins. For well-formed
// conversations this is an API-safe split point — the API contract requires
// every tool_use to be resolved before the next assistant turn, so pairing
// validity falls out of the assistant-turn boundary.
//
// This provides finer-grained grouping than human-turn grouping (which only
// splits at real user prompts), allowing compaction to operate on single-prompt
// agentic sessions where the entire workload is one human turn.
//
// Each returned group is a contiguous slice of messages that form one logical
// API round-trip. Group 0 contains the preamble (system messages, initial user
// prompt). Subsequent groups each start with an assistant message.
func GroupMessagesByAPIRound(messages []conversation.Message) [][]conversation.Message {
	if len(messages) == 0 {
		return nil
	}

	var groups [][]conversation.Message
	var current []conversation.Message

	// Track whether we've seen any assistant message to determine boundaries.
	// The first assistant message doesn't trigger a boundary — it joins group 0.
	seenAssistant := false

	for _, msg := range messages {
		if msg.Role == conversation.RoleAssistant {
			if seenAssistant && len(current) > 0 {
				// New assistant turn = new group boundary
				groups = append(groups, current)
				current = []conversation.Message{msg}
			} else {
				current = append(current, msg)
				seenAssistant = true
			}
		} else {
			current = append(current, msg)
		}
	}

	if len(current) > 0 {
		groups = append(groups, current)
	}

	return groups
}

// GroupInfo describes a message group for compaction decisions.
type GroupInfo struct {
	// Messages in this group.
	Messages []conversation.Message

	// Index is the group's position in the group list (0-based).
	Index int

	// TokenEstimate is the rough token count for this group.
	TokenEstimate int

	// HasToolUse indicates the group contains tool_use blocks.
	HasToolUse bool

	// HasUserPrompt indicates the group contains a real user prompt
	// (not a tool result or system message).
	HasUserPrompt bool

	// IsCompactable indicates whether this group can be safely compacted.
	IsCompactable bool
}

// AnalyzeGroups enriches message groups with metadata for compaction decisions.
// Protected groups (those containing system prompts or marked as non-compactable)
// are identified. Recent groups within the preserve window are also protected.
func AnalyzeGroups(groups [][]conversation.Message, preserveRecent int) []GroupInfo {
	result := make([]GroupInfo, len(groups))

	for i, group := range groups {
		info := GroupInfo{
			Messages:      group,
			Index:         i,
			TokenEstimate: EstimateTokens(group),
		}

		for _, msg := range group {
			switch msg.Role {
			case conversation.RoleUser:
				// Check if it's a real user prompt vs tool result
				if !isToolResult(msg) {
					info.HasUserPrompt = true
				}
			case conversation.RoleAssistant:
				if containsToolUse(msg) {
					info.HasToolUse = true
				}
			}
		}

		// A group is compactable unless it's a system-only group or
		// within the preserve window
		info.IsCompactable = true
		if isSystemOnlyGroup(group) {
			info.IsCompactable = false
		}

		result[i] = info
	}

	// Protect the most recent N groups from compaction
	if preserveRecent > 0 && preserveRecent < len(result) {
		for i := len(result) - preserveRecent; i < len(result); i++ {
			result[i].IsCompactable = false
		}
	} else if preserveRecent >= len(result) {
		// Preserve all groups
		for i := range result {
			result[i].IsCompactable = false
		}
	}

	return result
}

// SelectGroupsForCompaction partitions analyzed groups into those to summarize
// and those to preserve, respecting the token budget. It compacts from oldest
// first until enough tokens are freed.
//
// targetFreeTokens is the minimum number of tokens the compaction should free.
// If 0, all compactable groups are selected.
func SelectGroupsForCompaction(
	groups []GroupInfo,
	targetFreeTokens int,
) (toSummarize, toPreserve []GroupInfo) {
	if len(groups) == 0 {
		return nil, nil
	}

	freed := 0
	for _, g := range groups {
		if !g.IsCompactable {
			toPreserve = append(toPreserve, g)
			continue
		}

		if targetFreeTokens > 0 && freed >= targetFreeTokens {
			toPreserve = append(toPreserve, g)
			continue
		}

		toSummarize = append(toSummarize, g)
		freed += g.TokenEstimate
	}

	return toSummarize, toPreserve
}

// FlattenGroups extracts all messages from a slice of GroupInfo in order.
func FlattenGroups(groups []GroupInfo) []conversation.Message {
	var messages []conversation.Message
	for _, g := range groups {
		messages = append(messages, g.Messages...)
	}
	return messages
}

// AdjustIndexToPreserveAPIPairs adjusts a start index to avoid splitting
// tool_use/tool_result pairs. If the message at startIndex is a tool result
// (user message following an assistant tool_use), the index is moved backward
// to include the assistant message that triggered the tool use.
func AdjustIndexToPreserveAPIPairs(messages []conversation.Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(messages) {
		return startIndex
	}

	adjusted := startIndex

	// Walk backwards from the start index looking for orphaned tool results
	// that need their corresponding tool_use messages included.
	for i := adjusted; i < len(messages); i++ {
		if !isToolResult(messages[i]) {
			continue
		}
		// This is a tool result — find the assistant message with the
		// matching tool_use. Walk backward from adjusted-1.
		for j := adjusted - 1; j >= 0; j-- {
			if messages[j].Role == conversation.RoleAssistant && containsToolUse(messages[j]) {
				adjusted = j
				break
			}
		}
	}

	return adjusted
}

// isToolResult checks if a user message looks like a tool result rather than
// a real user prompt.
func isToolResult(msg conversation.Message) bool {
	if msg.Role != conversation.RoleUser {
		return false
	}
	// Heuristic: tool results typically contain structured output patterns
	// In the Polypod message format, tool results are user messages that
	// contain the tool output. We check for common indicators.
	content := msg.Content
	if len(content) == 0 {
		return false
	}
	// Check for tool_result markers
	return containsToolResultMarker(content)
}

// containsToolResultMarker checks if content contains markers indicating
// it's a tool result rather than a user prompt.
func containsToolResultMarker(content string) bool {
	markers := []string{
		"tool_result",
		"tool_use_id:",
		ClearedToolResultMessage,
	}
	for _, m := range markers {
		if len(content) >= len(m) && contains(content, m) {
			return true
		}
	}
	return false
}

// containsToolUse checks if an assistant message contains tool_use blocks.
func containsToolUse(msg conversation.Message) bool {
	if msg.Role != conversation.RoleAssistant {
		return false
	}
	return contains(msg.Content, "tool_use") || contains(msg.Content, "tool_call")
}

// isSystemOnlyGroup checks if a group contains only system messages.
func isSystemOnlyGroup(group []conversation.Message) bool {
	for _, msg := range group {
		if msg.Role != conversation.RoleSystem {
			return false
		}
	}
	return true
}

// contains is a simple string containment check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
