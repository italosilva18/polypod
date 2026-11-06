package messages

import (
	"fmt"
	"strings"
)

// CollapseConfig controls collapsing behavior with thresholds.
type CollapseConfig struct {
	// MinGroupSize is the minimum number of consecutive items to collapse.
	MinGroupSize int
	// Verbose disables collapsing when true (shows all messages individually).
	Verbose bool
	// FullscreenMode enables additional collapsing behaviors.
	FullscreenMode bool
}

// DefaultCollapseConfig returns a default configuration.
func DefaultCollapseConfig() CollapseConfig {
	return CollapseConfig{
		MinGroupSize:   2,
		Verbose:        false,
		FullscreenMode: false,
	}
}

// CollapsedGroup represents a group of collapsed messages with summary counts.
type CollapsedGroup struct {
	Type        string     // "collapsed_read_search", "collapsed_bash", "collapsed_hook"
	SearchCount int        // Number of search operations in the group.
	ReadCount   int        // Number of unique file reads.
	ListCount   int        // Number of directory listing operations.
	BashCount   int        // Number of bash commands.
	Messages    []*Message // Original messages in the group.
	DisplayHint string     // Last operation summary for display.
	ReadPaths   []string   // File paths that were read.
	SearchArgs  []string   // Search patterns used.
}

// Summary returns a human-readable summary of the collapsed group.
func (g *CollapsedGroup) Summary(active bool) string {
	return GetSearchReadSummaryText(g.SearchCount, g.ReadCount, active, g.ListCount)
}

// TotalOperations returns the total number of operations in the group.
func (g *CollapsedGroup) TotalOperations() int {
	return g.SearchCount + g.ReadCount + g.ListCount + g.BashCount
}

// CollapseBackgroundBash collapses consecutive completed background bash
// notifications into a single "N background commands completed" notification.
// Failed/killed tasks are left alone.
func CollapseBackgroundBash(messages []*Message, cfg CollapseConfig) []*Message {
	if cfg.Verbose {
		return messages
	}

	result := make([]*Message, 0, len(messages))
	i := 0

	for i < len(messages) {
		msg := messages[i]
		if isCompletedBackgroundBash(msg) {
			count := 0
			for i < len(messages) && isCompletedBackgroundBash(messages[i]) {
				count++
				i++
			}
			if count == 1 {
				result = append(result, msg)
			} else {
				result = append(result, &Message{
					Role: RoleUser,
					Content: []ContentBlock{{
						Type: ContentText,
						Text: fmt.Sprintf("%d background commands completed", count),
					}},
					IsMeta: true,
				})
			}
		} else {
			result = append(result, msg)
			i++
		}
	}

	return result
}

func isCompletedBackgroundBash(m *Message) bool {
	if m.Role != RoleUser || !m.IsMeta {
		return false
	}
	for _, block := range m.Content {
		if block.Type == ContentText && strings.Contains(block.Text, "background") &&
			strings.Contains(block.Text, "completed") {
			return true
		}
	}
	return false
}

// CollapseHookSummaries collapses consecutive hook summary messages with the
// same label into a single summary. This happens when parallel tool calls
// each emit their own hook summary.
func CollapseHookSummaries(messages []*Message, cfg CollapseConfig) []*Message {
	if cfg.Verbose {
		return messages
	}

	result := make([]*Message, 0, len(messages))
	i := 0

	for i < len(messages) {
		msg := messages[i]
		if isHookSummary(msg) {
			label := getHookLabel(msg)
			group := []*Message{msg}
			i++
			for i < len(messages) && isHookSummary(messages[i]) && getHookLabel(messages[i]) == label {
				group = append(group, messages[i])
				i++
			}
			if len(group) == 1 {
				result = append(result, msg)
			} else {
				// Merge into one summary.
				merged := &Message{
					Role:    msg.Role,
					Content: msg.Content,
					IsMeta:  true,
				}
				result = append(result, merged)
			}
		} else {
			result = append(result, msg)
			i++
		}
	}

	return result
}

func isHookSummary(m *Message) bool {
	return m.Role == RoleSystem && m.IsMeta
}

func getHookLabel(m *Message) string {
	for _, block := range m.Content {
		if block.Type == ContentText {
			return block.Text
		}
	}
	return ""
}

// CollapseReadSearch collapses consecutive read/search operations into
// summary groups. Groups break when assistant text or non-collapsible
// tool uses appear.
func CollapseReadSearch(messages []*Message, isSearchOrRead func(*Message) (isSearch, isRead, isList bool), cfg CollapseConfig) []*Message {
	if cfg.Verbose {
		return messages
	}

	result := make([]*Message, 0, len(messages))
	var currentGroup *CollapsedGroup
	var groupMsgs []*Message

	flushGroup := func() {
		if currentGroup == nil || len(groupMsgs) == 0 {
			return
		}
		if currentGroup.TotalOperations() < cfg.MinGroupSize {
			// Too few to collapse, emit individually.
			result = append(result, groupMsgs...)
		} else {
			// Emit a collapsed summary message.
			summary := currentGroup.Summary(false)
			result = append(result, &Message{
				Role: RoleAssistant,
				Content: []ContentBlock{{
					Type: ContentText,
					Text: summary,
				}},
				IsMeta: true,
			})
		}
		currentGroup = nil
		groupMsgs = nil
	}

	for _, msg := range messages {
		isSearch, isRead, isList := isSearchOrRead(msg)
		if isSearch || isRead || isList {
			if currentGroup == nil {
				currentGroup = &CollapsedGroup{Type: "collapsed_read_search"}
			}
			if isSearch {
				currentGroup.SearchCount++
			}
			if isRead {
				currentGroup.ReadCount++
			}
			if isList {
				currentGroup.ListCount++
			}
			currentGroup.Messages = append(currentGroup.Messages, msg)
			groupMsgs = append(groupMsgs, msg)
		} else if IsAssistantMessage(msg) && HasTextContent(msg) {
			// Text breaks the group.
			flushGroup()
			result = append(result, msg)
		} else if IsSystemMessage(msg) || IsTransient(msg) {
			// Skip without breaking group.
			if currentGroup != nil {
				groupMsgs = append(groupMsgs, msg)
			} else {
				result = append(result, msg)
			}
		} else {
			flushGroup()
			result = append(result, msg)
		}
	}
	flushGroup()

	return result
}

// GroupToolUses groups related tool_use + tool_result message pairs.
// Messages with the same tool name from the same API response are grouped.
type GroupedToolUse struct {
	ToolName    string
	ToolUses    []*Message // Assistant messages with tool_use.
	ToolResults []*Message // User messages with tool_result.
}

// GroupToolUses groups tool_use and tool_result message pairs by tool name.
// Only groups 2+ tool uses of the same type. Returns the messages with
// grouped messages replacing individual ones.
func GroupToolUses(messages []*Message, groupableTools map[string]bool, verbose bool) []*Message {
	if verbose || len(groupableTools) == 0 {
		return messages
	}

	// First pass: identify groups by tool name.
	type groupKey struct {
		toolName string
	}

	groups := make(map[string][]*Message) // toolName -> messages
	toolUseIDs := make(map[string]string) // tool_use_id -> toolName

	for _, msg := range messages {
		if msg.Role != RoleAssistant {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == ContentToolUse && groupableTools[block.Name] {
				key := block.Name
				groups[key] = append(groups[key], msg)
				if block.ID != "" {
					toolUseIDs[block.ID] = key
				}
			}
		}
	}

	// Identify valid groups (2+ items) and their tool_use IDs.
	validGroupIDs := make(map[string]bool)
	for key, group := range groups {
		if len(group) >= 2 {
			for _, msg := range group {
				for _, block := range msg.Content {
					if block.Type == ContentToolUse && block.Name == key {
						validGroupIDs[block.ID] = true
					}
				}
			}
		}
	}

	if len(validGroupIDs) == 0 {
		return messages
	}

	// Second pass: build output, emitting each group once.
	result := make([]*Message, 0, len(messages))
	emitted := make(map[string]bool) // toolName -> emitted

	for _, msg := range messages {
		if msg.Role == RoleAssistant {
			for _, block := range msg.Content {
				if block.Type == ContentToolUse && validGroupIDs[block.ID] {
					key := block.Name
					if !emitted[key] {
						emitted[key] = true
						// Emit the group as a merged summary message.
						groupMsgs := groups[key]
						summary := fmt.Sprintf("Ran %d %s operations", len(groupMsgs), key)
						result = append(result, &Message{
							Role: RoleAssistant,
							Content: []ContentBlock{{
								Type: ContentText,
								Text: summary,
							}},
							IsMeta: true,
						})
					}
					goto nextMsg
				}
			}
		}

		// Skip tool results for grouped tool_use IDs.
		if msg.Role == RoleUser {
			allGrouped := true
			hasToolResult := false
			for _, block := range msg.Content {
				if block.Type == ContentToolResult {
					hasToolResult = true
					if !validGroupIDs[block.ToolUseID] {
						allGrouped = false
					}
				}
			}
			if hasToolResult && allGrouped {
				continue
			}
		}

		result = append(result, msg)
	nextMsg:
	}

	return result
}

// GetSearchReadSummaryText generates summary text for collapsed search/read operations.
func GetSearchReadSummaryText(searchCount, readCount int, active bool, listCount int) string {
	var parts []string

	if searchCount > 0 {
		verb := "Searched for"
		if active {
			verb = "Searching for"
		}
		if len(parts) > 0 {
			verb = strings.ToLower(verb)
		}
		noun := "patterns"
		if searchCount == 1 {
			noun = "pattern"
		}
		parts = append(parts, fmt.Sprintf("%s %d %s", verb, searchCount, noun))
	}

	if readCount > 0 {
		verb := "Read"
		if active {
			verb = "Reading"
		}
		if len(parts) > 0 {
			verb = strings.ToLower(verb)
		}
		noun := "files"
		if readCount == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%s %d %s", verb, readCount, noun))
	}

	if listCount > 0 {
		verb := "Listed"
		if active {
			verb = "Listing"
		}
		if len(parts) > 0 {
			verb = strings.ToLower(verb)
		}
		noun := "directories"
		if listCount == 1 {
			noun = "directory"
		}
		parts = append(parts, fmt.Sprintf("%s %d %s", verb, listCount, noun))
	}

	text := strings.Join(parts, ", ")
	if active && text != "" {
		text += "…"
	}
	return text
}
