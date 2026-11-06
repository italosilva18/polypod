// Package thinkback replays extended thinking blocks extracted from
// conversation history, enabling users to review the model's reasoning
// process across turns.
package thinkback

import (
	"regexp"
	"strings"
	"time"
)

// thinkingRe matches <thinking>...</thinking> tags in message content,
// including newlines inside the block.
var thinkingRe = regexp.MustCompile(`(?s)<thinking>(.*?)</thinking>`)

// ThinkingBlock holds a single extracted thinking segment.
type ThinkingBlock struct {
	TurnNumber int
	Content    string
	Duration   time.Duration
	Timestamp  time.Time
	Model      string
}

// PlayerMessage is the interface that messages must satisfy for extraction.
// Both messages.Message (content-block based) and conversation.Message
// (plain string) can implement this via thin wrappers.
type PlayerMessage struct {
	Role      string
	Content   string         // plain-text content (may contain <thinking> tags)
	Blocks    []ContentBlock // structured content blocks (optional)
	Timestamp time.Time
	Model     string
}

// ContentBlock mirrors the subset of messages.ContentBlock needed here.
type ContentBlock struct {
	Type string // "thinking", "text", etc.
	Text string
}

// Player scans conversation history and replays thinking blocks.
type Player struct {
	// MinLength ignores thinking blocks shorter than this many characters.
	MinLength int
}

// NewPlayer creates a Player with sensible defaults.
func NewPlayer() *Player {
	return &Player{
		MinLength: 0,
	}
}

// Extract scans messages for thinking content. It handles two formats:
//
//  1. Structured ContentBlock with Type == "thinking" (from messages.Message)
//  2. Inline <thinking>...</thinking> tags embedded in Content strings
//     (produced by provider/anthropic.go parseResponse)
//
// Only assistant messages are inspected. Blocks are returned in
// conversation order.
func (p *Player) Extract(messages []PlayerMessage) []ThinkingBlock {
	var blocks []ThinkingBlock
	turn := 0

	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		turn++

		// Strategy 1: structured content blocks.
		for _, cb := range msg.Blocks {
			if cb.Type == "thinking" || cb.Type == "redacted_thinking" {
				text := strings.TrimSpace(cb.Text)
				if len(text) < p.MinLength {
					continue
				}
				blocks = append(blocks, ThinkingBlock{
					TurnNumber: turn,
					Content:    text,
					Timestamp:  msg.Timestamp,
					Model:      msg.Model,
				})
			}
		}

		// Strategy 2: inline <thinking> tags in plain content.
		matches := thinkingRe.FindAllStringSubmatch(msg.Content, -1)
		for _, m := range matches {
			text := strings.TrimSpace(m[1])
			if len(text) < p.MinLength {
				continue
			}
			blocks = append(blocks, ThinkingBlock{
				TurnNumber: turn,
				Content:    text,
				Timestamp:  msg.Timestamp,
				Model:      msg.Model,
			})
		}
	}

	// Estimate duration by distributing time between consecutive assistant
	// turns. The first block gets zero duration because there is no prior
	// reference point.
	for i := 1; i < len(blocks); i++ {
		if !blocks[i].Timestamp.IsZero() && !blocks[i-1].Timestamp.IsZero() {
			blocks[i].Duration = blocks[i].Timestamp.Sub(blocks[i-1].Timestamp)
		}
	}

	return blocks
}

// StripThinking removes all <thinking>...</thinking> tags from a string,
// returning only the visible content.
func StripThinking(s string) string {
	return strings.TrimSpace(thinkingRe.ReplaceAllString(s, ""))
}

// Summary returns a compact multi-line summary of thinking blocks suitable
// for terminal display.
func Summary(blocks []ThinkingBlock) string {
	if len(blocks) == 0 {
		return "Nenhum bloco de thinking encontrado."
	}

	var sb strings.Builder
	for i, b := range blocks {
		preview := b.Content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		// Remove newlines from preview for compact display.
		preview = strings.ReplaceAll(preview, "\n", " ")

		sb.WriteString(formatEntry(i+1, b.TurnNumber, b.Model, preview))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func formatEntry(idx, turn int, model, preview string) string {
	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(itoa(idx))
	sb.WriteString("] turno ")
	sb.WriteString(itoa(turn))
	if model != "" {
		sb.WriteString(" (")
		sb.WriteString(model)
		sb.WriteString(")")
	}
	sb.WriteString(": ")
	sb.WriteString(preview)
	return sb.String()
}

// itoa converts an int to string without importing strconv (keeps imports minimal).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
