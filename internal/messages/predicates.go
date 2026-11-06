// Package messages provides message handling utilities including predicates,
// queuing, collapsing, formatting, and attachment processing.
package messages

// MessageRole identifies the sender of a message.
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// ContentType identifies the type of a content block.
type ContentType string

const (
	ContentText           ContentType = "text"
	ContentToolUse        ContentType = "tool_use"
	ContentToolResult     ContentType = "tool_result"
	ContentImage          ContentType = "image"
	ContentThinking       ContentType = "thinking"
	ContentRedactThinking ContentType = "redacted_thinking"
)

// ContentBlock represents a single content block in a message.
type ContentBlock struct {
	Type      ContentType    `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
}

// Message represents a conversation message.
type Message struct {
	Role          MessageRole    `json:"role"`
	Content       []ContentBlock `json:"content"`
	IsMeta        bool           `json:"is_meta,omitempty"`
	ToolUseResult any            `json:"tool_use_result,omitempty"`
}

// Predicate is a function that tests a message against some condition.
type Predicate func(*Message) bool

// IsUserMessage returns true if the message is from a user.
func IsUserMessage(m *Message) bool {
	return m.Role == RoleUser
}

// IsAssistantMessage returns true if the message is from the assistant.
func IsAssistantMessage(m *Message) bool {
	return m.Role == RoleAssistant
}

// IsSystemMessage returns true if the message is a system message.
func IsSystemMessage(m *Message) bool {
	return m.Role == RoleSystem
}

// IsHumanTurn returns true if this is a genuine human turn (user message
// that is not meta and not a tool result).
func IsHumanTurn(m *Message) bool {
	return m.Role == RoleUser && !m.IsMeta && m.ToolUseResult == nil
}

// IsToolUse returns true if the message contains a tool_use content block.
func IsToolUse(m *Message) bool {
	for _, block := range m.Content {
		if block.Type == ContentToolUse {
			return true
		}
	}
	return false
}

// IsToolResult returns true if the message contains a tool_result content block.
func IsToolResult(m *Message) bool {
	for _, block := range m.Content {
		if block.Type == ContentToolResult {
			return true
		}
	}
	return false
}

// HasToolCalls returns true if the message contains any tool_use blocks.
func HasToolCalls(m *Message) bool {
	return IsToolUse(m)
}

// HasTextContent returns true if the message has at least one non-empty text block.
func HasTextContent(m *Message) bool {
	for _, block := range m.Content {
		if block.Type == ContentText && len(block.Text) > 0 {
			return true
		}
	}
	return false
}

// HasImageContent returns true if the message contains an image content block.
func HasImageContent(m *Message) bool {
	for _, block := range m.Content {
		if block.Type == ContentImage {
			return true
		}
	}
	return false
}

// IsMetaMessage returns true if the message is a meta/system-injected message.
func IsMetaMessage(m *Message) bool {
	return m.IsMeta
}

// IsTransient returns true if the message is transient (thinking blocks, redacted thinking).
func IsTransient(m *Message) bool {
	for _, block := range m.Content {
		if block.Type == ContentThinking || block.Type == ContentRedactThinking {
			return true
		}
	}
	return false
}

// FilterMessages applies a chain of predicates to a message slice,
// returning only messages that pass ALL predicates.
func FilterMessages(msgs []*Message, predicates ...Predicate) []*Message {
	result := make([]*Message, 0, len(msgs))
	for _, m := range msgs {
		pass := true
		for _, pred := range predicates {
			if !pred(m) {
				pass = false
				break
			}
		}
		if pass {
			result = append(result, m)
		}
	}
	return result
}

// Not returns a predicate that negates the given predicate.
func Not(p Predicate) Predicate {
	return func(m *Message) bool {
		return !p(m)
	}
}

// And returns a predicate that requires all given predicates to pass.
func And(predicates ...Predicate) Predicate {
	return func(m *Message) bool {
		for _, p := range predicates {
			if !p(m) {
				return false
			}
		}
		return true
	}
}

// Or returns a predicate that requires at least one predicate to pass.
func Or(predicates ...Predicate) Predicate {
	return func(m *Message) bool {
		for _, p := range predicates {
			if p(m) {
				return true
			}
		}
		return false
	}
}

// CountTokensInMessage estimates the token count for a message.
// Uses a simple heuristic of ~4 characters per token.
func CountTokensInMessage(m *Message) int {
	total := 0
	for _, block := range m.Content {
		switch block.Type {
		case ContentText:
			total += len(block.Text)
		case ContentToolUse:
			total += len(block.Text) + len(block.Name)*2
		case ContentToolResult:
			total += len(block.Content)
		}
	}
	// Rough estimate: ~4 chars per token.
	tokens := total / 4
	if tokens < 1 && total > 0 {
		tokens = 1
	}
	return tokens
}

// ExtractTextContent extracts and joins all text blocks from a message.
func ExtractTextContent(m *Message, sep string) string {
	var parts []string
	for _, block := range m.Content {
		if block.Type == ContentText && len(block.Text) > 0 {
			parts = append(parts, block.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

// GetToolUseIDs returns all tool_use IDs from a message.
func GetToolUseIDs(m *Message) []string {
	var ids []string
	for _, block := range m.Content {
		if block.Type == ContentToolUse && block.ID != "" {
			ids = append(ids, block.ID)
		}
	}
	return ids
}

// GetToolResultIDs returns all tool_use_ids from tool_result blocks.
func GetToolResultIDs(m *Message) []string {
	var ids []string
	for _, block := range m.Content {
		if block.Type == ContentToolResult && block.ToolUseID != "" {
			ids = append(ids, block.ToolUseID)
		}
	}
	return ids
}
