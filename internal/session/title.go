package session

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode"
)

// TitleConfig holds configuration for title generation.
type TitleConfig struct {
	MaxLength int
	Logger    *slog.Logger
}

// TitleGenerator handles session title generation and manipulation.
type TitleGenerator struct {
	maxLength int
	logger    *slog.Logger
}

// NewTitleGenerator creates a title generator.
func NewTitleGenerator(cfg TitleConfig) *TitleGenerator {
	if cfg.MaxLength <= 0 {
		cfg.MaxLength = 60
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &TitleGenerator{
		maxLength: cfg.MaxLength,
		logger:    cfg.Logger,
	}
}

// GenerateTitle creates a title from the conversation messages. It examines
// the first few user messages to extract a representative title.
func (tg *TitleGenerator) GenerateTitle(messages []Message) string {
	text := extractConversationText(messages)
	if text == "" {
		return "Untitled session"
	}

	title := TitleFromText(text, tg.maxLength)
	return SanitizeTitle(title)
}

// TitleFromFirstMessage extracts a title from the first user message.
func TitleFromFirstMessage(messages []Message) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		// Skip meta/system-like messages that start with XML tags.
		if strings.HasPrefix(content, "<") {
			continue
		}
		return TitleFromText(content, 60)
	}
	return "Untitled session"
}

// TitleFromText generates a title from arbitrary text content.
func TitleFromText(text string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 60
	}

	// Collapse whitespace.
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)

	if text == "" {
		return "Untitled session"
	}

	// If short enough, use as-is.
	if len(text) <= maxLen {
		return sentenceCase(text)
	}

	// Try to find a natural break point.
	truncated := text[:maxLen]

	// Break at last sentence boundary.
	for _, sep := range []string{". ", "? ", "! ", "; "} {
		if idx := strings.LastIndex(truncated, sep); idx > maxLen/3 {
			return sentenceCase(truncated[:idx+1])
		}
	}

	// Break at last word boundary.
	if idx := strings.LastIndex(truncated, " "); idx > maxLen/3 {
		return sentenceCase(truncated[:idx])
	}

	return sentenceCase(truncated[:maxLen-3] + "...")
}

// SanitizeTitle cleans a title for filesystem use and display.
func SanitizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Untitled session"
	}

	// Remove or replace filesystem-unsafe characters.
	unsafe := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	title = unsafe.ReplaceAllString(title, "")

	// Collapse multiple spaces.
	title = strings.Join(strings.Fields(title), " ")

	// Trim trailing dots/spaces (Windows filesystem restriction).
	title = strings.TrimRight(title, ". ")

	if title == "" {
		return "Untitled session"
	}

	// Enforce max length.
	if len(title) > 200 {
		title = title[:197] + "..."
	}

	return title
}

// SlugFromTitle converts a title to a filesystem-safe slug.
func SlugFromTitle(title string) string {
	title = strings.ToLower(title)

	// Replace non-alphanumeric with hyphens.
	slug := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(title, "-")

	// Trim leading/trailing hyphens.
	slug = strings.Trim(slug, "-")

	// Collapse multiple hyphens.
	slug = regexp.MustCompile(`-{2,}`).ReplaceAllString(slug, "-")

	if slug == "" {
		slug = "untitled"
	}

	// Max 50 chars for slug.
	if len(slug) > 50 {
		slug = slug[:50]
		slug = strings.TrimRight(slug, "-")
	}

	return slug
}

// RenameSession updates a session's title. The title is sanitized before
// being applied. Returns an error if the title is empty after sanitization.
func RenameSession(storage SessionStorage, sessionID, newTitle string) error {
	sanitized := SanitizeTitle(newTitle)
	if sanitized == "Untitled session" && strings.TrimSpace(newTitle) != "" {
		// The sanitization stripped everything. Try harder.
		sanitized = strings.TrimSpace(newTitle)
	}
	if sanitized == "" {
		return fmt.Errorf("title cannot be empty")
	}

	sess, err := storage.Load(sessionID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	sess.Title = sanitized
	sess.Metadata.CustomTitle = sanitized

	return storage.Save(sess)
}

// --- helpers ---

// extractConversationText flattens user/assistant messages into a single string.
// Tail-slices to the last 1000 chars for title generation.
func extractConversationText(messages []Message) string {
	const maxChars = 1000
	var parts []string
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content != "" {
			parts = append(parts, content)
		}
	}
	text := strings.Join(parts, "\n")
	if len(text) > maxChars {
		text = text[len(text)-maxChars:]
	}
	return text
}

// sentenceCase capitalizes only the first letter and leaves the rest as-is.
func sentenceCase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
