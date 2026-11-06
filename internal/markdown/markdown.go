// Package markdown provides markdown parsing, rendering, and extraction utilities.
// Migrated from Claude Code's markdown.ts, stream.ts, streamlinedTransform.ts.
package markdown

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// CodeBlock represents a fenced code block extracted from markdown.
type CodeBlock struct {
	// Language is the language identifier after the opening fence (e.g., "go", "python").
	Language string

	// Content is the raw text content within the code fence.
	Content string

	// StartLine is the 1-based line number where the code block starts.
	StartLine int

	// EndLine is the 1-based line number where the code block ends.
	EndLine int
}

// Frontmatter holds parsed YAML frontmatter from a markdown document.
type Frontmatter struct {
	// Raw is the raw YAML text between the --- delimiters.
	Raw string

	// Fields maps frontmatter key-value pairs (string values only, for simplicity).
	Fields map[string]string
}

// Node represents a node in a simplified markdown AST.
type Node struct {
	Type     NodeType
	Content  string
	Children []*Node
	Depth    int    // for headings
	Lang     string // for code blocks
	Ordered  bool   // for lists
}

// NodeType identifies the kind of markdown element.
type NodeType int

const (
	NodeDocument NodeType = iota
	NodeParagraph
	NodeHeading
	NodeCodeBlock
	NodeCodeSpan
	NodeBlockquote
	NodeList
	NodeListItem
	NodeHorizontalRule
	NodeEmphasis
	NodeStrong
	NodeLink
	NodeImage
	NodeText
	NodeTable
	NodeTableRow
	NodeTableCell
)

// ParseMarkdown parses markdown text into a simplified AST.
func ParseMarkdown(input string) *Node {
	root := &Node{
		Type:     NodeDocument,
		Children: make([]*Node, 0),
	}

	lines := strings.Split(input, "\n")
	i := 0

	for i < len(lines) {
		line := lines[i]

		// Fenced code block.
		if strings.HasPrefix(line, "```") {
			lang := strings.TrimPrefix(line, "```")
			lang = strings.TrimSpace(lang)
			var content strings.Builder
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
				if content.Len() > 0 {
					content.WriteString("\n")
				}
				content.WriteString(lines[i])
				i++
			}
			root.Children = append(root.Children, &Node{
				Type:    NodeCodeBlock,
				Lang:    lang,
				Content: content.String(),
			})
			if i < len(lines) {
				i++ // skip closing ```
			}
			continue
		}

		// Heading.
		if strings.HasPrefix(line, "#") {
			depth := 0
			for _, c := range line {
				if c == '#' {
					depth++
				} else {
					break
				}
			}
			if depth > 0 && depth <= 6 {
				text := strings.TrimSpace(line[depth:])
				root.Children = append(root.Children, &Node{
					Type:    NodeHeading,
					Content: text,
					Depth:   depth,
				})
				i++
				continue
			}
		}

		// Blockquote.
		if strings.HasPrefix(line, "> ") || line == ">" {
			var content strings.Builder
			for i < len(lines) && (strings.HasPrefix(lines[i], "> ") || lines[i] == ">") {
				text := strings.TrimPrefix(lines[i], "> ")
				text = strings.TrimPrefix(text, ">")
				if content.Len() > 0 {
					content.WriteString("\n")
				}
				content.WriteString(text)
				i++
			}
			root.Children = append(root.Children, &Node{
				Type:    NodeBlockquote,
				Content: content.String(),
			})
			continue
		}

		// Horizontal rule.
		trimmedLine := strings.TrimSpace(line)
		if len(trimmedLine) >= 3 && (allSameChar(trimmedLine, '-') || allSameChar(trimmedLine, '*') || allSameChar(trimmedLine, '_')) {
			root.Children = append(root.Children, &Node{
				Type: NodeHorizontalRule,
			})
			i++
			continue
		}

		// List item.
		if isListItem(line) {
			root.Children = append(root.Children, &Node{
				Type:    NodeListItem,
				Content: extractListContent(line),
			})
			i++
			continue
		}

		// Empty line.
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}

		// Paragraph (default).
		var content strings.Builder
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(lines[i], "#") && !strings.HasPrefix(lines[i], "```") && !strings.HasPrefix(lines[i], "> ") {
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString(lines[i])
			i++
		}
		if content.Len() > 0 {
			root.Children = append(root.Children, &Node{
				Type:    NodeParagraph,
				Content: content.String(),
			})
		}
	}

	return root
}

// ExtractCodeBlocks extracts all fenced code blocks from markdown text.
func ExtractCodeBlocks(input string) []CodeBlock {
	var blocks []CodeBlock
	lines := strings.Split(input, "\n")
	i := 0

	for i < len(lines) {
		line := lines[i]

		if strings.HasPrefix(line, "```") {
			startLine := i + 1
			lang := strings.TrimPrefix(line, "```")
			lang = strings.TrimSpace(lang)
			var content strings.Builder
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
				if content.Len() > 0 {
					content.WriteString("\n")
				}
				content.WriteString(lines[i])
				i++
			}
			endLine := i + 1
			if i < len(lines) {
				i++ // skip closing ```
			}
			blocks = append(blocks, CodeBlock{
				Language:  lang,
				Content:   content.String(),
				StartLine: startLine,
				EndLine:   endLine,
			})
			continue
		}
		i++
	}

	return blocks
}

// ExtractFrontmatter parses YAML frontmatter from the start of a markdown document.
// Returns nil if no frontmatter is found. Frontmatter must start on the first line
// with "---" and end with another "---".
func ExtractFrontmatter(input string) *Frontmatter {
	lines := strings.Split(input, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}

	if endIdx < 0 {
		return nil
	}

	rawLines := lines[1:endIdx]
	raw := strings.Join(rawLines, "\n")

	// Simple key: value parsing (no nested YAML).
	fields := make(map[string]string)
	for _, line := range rawLines {
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			// Remove surrounding quotes if present.
			value = strings.Trim(value, "\"'")
			if key != "" {
				fields[key] = value
			}
		}
	}

	return &Frontmatter{
		Raw:    raw,
		Fields: fields,
	}
}

// StripMarkdown removes all markdown formatting and returns plain text.
func StripMarkdown(input string) string {
	// Remove code blocks entirely (keep content).
	codeBlockRe := regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")
	result := codeBlockRe.ReplaceAllString(input, "$1")

	// Remove inline code.
	inlineCodeRe := regexp.MustCompile("`([^`]+)`")
	result = inlineCodeRe.ReplaceAllString(result, "$1")

	// Remove headings markers.
	headingRe := regexp.MustCompile(`(?m)^#{1,6}\s+`)
	result = headingRe.ReplaceAllString(result, "")

	// Remove bold.
	boldRe := regexp.MustCompile(`\*\*([^*]+)\*\*`)
	result = boldRe.ReplaceAllString(result, "$1")

	// Remove italic.
	italicRe := regexp.MustCompile(`\*([^*]+)\*`)
	result = italicRe.ReplaceAllString(result, "$1")

	// Remove links, keep text.
	linkRe := regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	result = linkRe.ReplaceAllString(result, "$1")

	// Remove images, keep alt text.
	imageRe := regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	result = imageRe.ReplaceAllString(result, "$1")

	// Remove blockquote markers.
	blockquoteRe := regexp.MustCompile(`(?m)^>\s*`)
	result = blockquoteRe.ReplaceAllString(result, "")

	// Remove horizontal rules.
	hrRe := regexp.MustCompile(`(?m)^[-*_]{3,}$`)
	result = hrRe.ReplaceAllString(result, "")

	// Remove list markers.
	ulRe := regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	result = ulRe.ReplaceAllString(result, "")
	olRe := regexp.MustCompile(`(?m)^\s*\d+\.\s+`)
	result = olRe.ReplaceAllString(result, "")

	// Collapse multiple newlines.
	multiNewline := regexp.MustCompile(`\n{3,}`)
	result = multiNewline.ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result)
}

// RenderMarkdown renders markdown to terminal-friendly text with ANSI formatting.
// This is a simplified renderer for CLI output.
func RenderMarkdown(input string) string {
	ast := ParseMarkdown(input)
	var b strings.Builder
	renderNode(&b, ast, 0)
	return strings.TrimSpace(b.String())
}

func renderNode(b *strings.Builder, node *Node, depth int) {
	switch node.Type {
	case NodeDocument:
		for i, child := range node.Children {
			if i > 0 {
				b.WriteString("\n")
			}
			renderNode(b, child, depth)
		}

	case NodeHeading:
		prefix := strings.Repeat("#", node.Depth)
		b.WriteString(prefix)
		b.WriteString(" ")
		b.WriteString(node.Content)
		b.WriteString("\n")

	case NodeParagraph:
		b.WriteString(node.Content)
		b.WriteString("\n")

	case NodeCodeBlock:
		b.WriteString("```")
		if node.Lang != "" {
			b.WriteString(node.Lang)
		}
		b.WriteString("\n")
		b.WriteString(node.Content)
		b.WriteString("\n```\n")

	case NodeBlockquote:
		for _, line := range strings.Split(node.Content, "\n") {
			b.WriteString("> ")
			b.WriteString(line)
			b.WriteString("\n")
		}

	case NodeHorizontalRule:
		b.WriteString("---\n")

	case NodeListItem:
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString("- ")
		b.WriteString(node.Content)
		b.WriteString("\n")

	case NodeText:
		b.WriteString(node.Content)
	}
}

// Stream implements a single-consumer async iterable stream.
// Migrated from Claude Code's stream.ts.
type Stream[T any] struct {
	mu      sync.Mutex
	queue   []T
	done    bool
	err     error
	waitCh  chan struct{}
	started bool
}

// NewStream creates a new empty stream.
func NewStream[T any]() *Stream[T] {
	return &Stream[T]{
		queue:  make([]T, 0, 8),
		waitCh: make(chan struct{}, 1),
	}
}

// Enqueue adds a value to the stream.
func (s *Stream[T]) Enqueue(value T) {
	s.mu.Lock()
	s.queue = append(s.queue, value)
	s.mu.Unlock()

	// Signal waiters.
	select {
	case s.waitCh <- struct{}{}:
	default:
	}
}

// Done marks the stream as complete (no more values).
func (s *Stream[T]) Done() {
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()

	select {
	case s.waitCh <- struct{}{}:
	default:
	}
}

// Error marks the stream as failed with the given error.
func (s *Stream[T]) Error(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()

	select {
	case s.waitCh <- struct{}{}:
	default:
	}
}

// Next returns the next value from the stream.
// Returns (value, true) if a value is available, or (zero, false) if the stream is done.
// Returns an error if the stream has an error.
func (s *Stream[T]) Next() (T, bool, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			val := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return val, true, nil
		}
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			var zero T
			return zero, false, err
		}
		if s.done {
			s.mu.Unlock()
			var zero T
			return zero, false, nil
		}
		s.mu.Unlock()

		// Wait for signal.
		<-s.waitCh
	}
}

// Drain reads all remaining values from the stream into a slice.
func (s *Stream[T]) Drain() ([]T, error) {
	var result []T
	for {
		val, ok, err := s.Next()
		if err != nil {
			return result, err
		}
		if !ok {
			return result, nil
		}
		result = append(result, val)
	}
}

// ToolCounts tracks tool usage by category for streamlined output.
type ToolCounts struct {
	Searches int
	Reads    int
	Writes   int
	Commands int
	Other    int
}

// GetToolSummaryText generates a summary string for tool counts.
// Returns empty string if no tools were used.
func (tc ToolCounts) GetToolSummaryText() string {
	var parts []string

	if tc.Searches > 0 {
		noun := "pattern"
		if tc.Searches != 1 {
			noun = "patterns"
		}
		parts = append(parts, fmt.Sprintf("searched %d %s", tc.Searches, noun))
	}
	if tc.Reads > 0 {
		noun := "file"
		if tc.Reads != 1 {
			noun = "files"
		}
		parts = append(parts, fmt.Sprintf("read %d %s", tc.Reads, noun))
	}
	if tc.Writes > 0 {
		noun := "file"
		if tc.Writes != 1 {
			noun = "files"
		}
		parts = append(parts, fmt.Sprintf("wrote %d %s", tc.Writes, noun))
	}
	if tc.Commands > 0 {
		noun := "command"
		if tc.Commands != 1 {
			noun = "commands"
		}
		parts = append(parts, fmt.Sprintf("ran %d %s", tc.Commands, noun))
	}
	if tc.Other > 0 {
		noun := "tool"
		if tc.Other != 1 {
			noun = "tools"
		}
		parts = append(parts, fmt.Sprintf("%d other %s", tc.Other, noun))
	}

	if len(parts) == 0 {
		return ""
	}

	result := strings.Join(parts, ", ")
	// Capitalize first letter.
	if len(result) > 0 {
		runes := []rune(result)
		runes[0] = unicode.ToUpper(runes[0])
		result = string(runes)
	}
	return result
}

// Helper functions.

func allSameChar(s string, c byte) bool {
	for i := range s {
		if s[i] != c {
			return false
		}
	}
	return len(s) > 0
}

func isListItem(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return true
	}
	// Numbered list: "1. " etc.
	for i, c := range trimmed {
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' && i > 0 && i < len(trimmed)-1 && trimmed[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

func extractListContent(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return trimmed[2:]
	}
	// Numbered: skip "N. "
	for i, c := range trimmed {
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' && i > 0 && i < len(trimmed)-1 {
			return strings.TrimLeft(trimmed[i+1:], " ")
		}
		break
	}
	return trimmed
}
