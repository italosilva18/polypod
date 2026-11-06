package shareexport

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExportFormat identifies the output format for exported conversations.
type ExportFormat string

const (
	FormatJSON     ExportFormat = "json"
	FormatMarkdown ExportFormat = "markdown"
	FormatHTML     ExportFormat = "html"
	FormatText     ExportFormat = "text"
)

// ExportConfig controls how conversations are exported.
type ExportConfig struct {
	Format            ExportFormat
	IncludeToolCalls  bool
	IncludeThinking   bool
	IncludeTimestamps bool
	MaxMessages       int // 0 means all messages
	Title             string
}

// Message represents a single conversation message for export.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	Thinking  string    `json:"thinking,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// Export formats messages according to the given ExportConfig and returns the result as bytes.
func Export(messages []Message, cfg ExportConfig) ([]byte, error) {
	msgs := filterMessages(messages, cfg)

	switch cfg.Format {
	case FormatJSON:
		return ToJSON(msgs, cfg)
	case FormatMarkdown:
		return []byte(ToMarkdown(msgs, cfg)), nil
	case FormatHTML:
		return []byte(ToHTML(msgs, cfg)), nil
	case FormatText:
		return []byte(ToText(msgs, cfg)), nil
	default:
		return nil, fmt.Errorf("shareexport: unknown format %q", cfg.Format)
	}
}

// ExportToFile exports messages to the specified file path.
func ExportToFile(messages []Message, cfg ExportConfig, path string) error {
	data, err := Export(messages, cfg)
	if err != nil {
		return fmt.Errorf("shareexport: export failed: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("shareexport: create directory: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ToMarkdown renders messages as markdown with role headers.
func ToMarkdown(messages []Message, cfg ExportConfig) string {
	var sb strings.Builder

	if cfg.Title != "" {
		fmt.Fprintf(&sb, "# %s\n\n", cfg.Title)
		sb.WriteString("---\n\n")
	}

	for _, m := range messages {
		writeMarkdownMessage(&sb, m, cfg)
	}

	return sb.String()
}

func writeMarkdownMessage(sb *strings.Builder, m Message, cfg ExportConfig) {
	heading := roleHeading(m.Role)
	fmt.Fprintf(sb, "## %s\n\n", heading)

	if cfg.IncludeTimestamps && !m.Timestamp.IsZero() {
		fmt.Fprintf(sb, "*%s*\n\n", m.Timestamp.Format("2006-01-02 15:04:05"))
	}

	if cfg.IncludeThinking && m.Thinking != "" {
		sb.WriteString("<details>\n<summary>Thinking</summary>\n\n")
		sb.WriteString(m.Thinking)
		sb.WriteString("\n\n</details>\n\n")
	}

	sb.WriteString(m.Content)
	sb.WriteString("\n\n")

	if m.ToolName != "" {
		fmt.Fprintf(sb, "> **Tool**: `%s`\n\n", m.ToolName)
	}
}

// ToHTML renders messages as a standalone HTML document with embedded CSS.
func ToHTML(messages []Message, cfg ExportConfig) string {
	var sb strings.Builder

	title := cfg.Title
	if title == "" {
		title = "Conversation Export"
	}

	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + htmlEscape(title) + `</title>
<style>
*, *::before, *::after { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, 'Segoe UI', sans-serif;
  background: #0d1117; color: #c9d1d9;
  max-width: 860px; margin: 0 auto; padding: 32px 20px;
  line-height: 1.7;
}
h1 { font-size: 1.75rem; color: #f0f6fc; margin-bottom: 8px; }
.meta { color: #8b949e; font-size: 0.8rem; margin-bottom: 28px; padding-bottom: 16px; border-bottom: 1px solid #21262d; }
.message { margin-bottom: 24px; padding: 16px 20px; border-radius: 10px; }
.message.user { background: #161b22; border-left: 3px solid #58a6ff; }
.message.assistant { background: #161b22; border-left: 3px solid #bc8cff; }
.message.tool { background: #0d1117; border-left: 3px solid #3fb950; font-family: 'Fira Code', 'Cascadia Code', monospace; font-size: 0.85rem; }
.message.system { background: #161b22; border-left: 3px solid #8b949e; opacity: 0.7; }
.role { font-size: 0.7rem; text-transform: uppercase; letter-spacing: 0.08em; color: #8b949e; margin-bottom: 8px; font-weight: 600; }
.timestamp { font-size: 0.7rem; color: #484f58; margin-left: 12px; font-weight: 400; }
.tool-name { font-size: 0.75rem; color: #3fb950; margin-bottom: 6px; }
.thinking { background: #1c1c2e; border-radius: 6px; padding: 12px; margin-bottom: 10px; font-size: 0.85rem; color: #8b949e; border: 1px solid #30363d; }
.thinking summary { cursor: pointer; color: #bc8cff; font-weight: 600; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; }
.content { white-space: pre-wrap; word-wrap: break-word; }
pre { background: #010409; padding: 14px; border-radius: 8px; overflow-x: auto; font-size: 0.85rem; margin: 10px 0; }
code { font-family: 'Fira Code', monospace; font-size: 0.85rem; background: #21262d; padding: 2px 6px; border-radius: 4px; }
</style>
</head>
<body>
`)

	fmt.Fprintf(&sb, "<h1>%s</h1>\n", htmlEscape(title))
	sb.WriteString(`<div class="meta">Exported ` + time.Now().Format("2006-01-02 15:04") + `</div>` + "\n")

	for _, m := range messages {
		writeHTMLMessage(&sb, m, cfg)
	}

	sb.WriteString("</body>\n</html>\n")
	return sb.String()
}

func writeHTMLMessage(sb *strings.Builder, m Message, cfg ExportConfig) {
	class := m.Role
	if class == "" {
		class = "unknown"
	}
	fmt.Fprintf(sb, `<div class="message %s">`+"\n", class)

	// Role label with optional timestamp.
	sb.WriteString(`<div class="role">` + htmlEscape(m.Role))
	if cfg.IncludeTimestamps && !m.Timestamp.IsZero() {
		sb.WriteString(`<span class="timestamp">` + m.Timestamp.Format("15:04:05") + `</span>`)
	}
	sb.WriteString("</div>\n")

	// Tool name badge.
	if m.ToolName != "" {
		fmt.Fprintf(sb, `<div class="tool-name">%s</div>`+"\n", htmlEscape(m.ToolName))
	}

	// Thinking block.
	if cfg.IncludeThinking && m.Thinking != "" {
		sb.WriteString("<details class=\"thinking\">\n<summary>Thinking</summary>\n<div class=\"content\">")
		sb.WriteString(htmlEscape(m.Thinking))
		sb.WriteString("</div>\n</details>\n")
	}

	// Message content.
	sb.WriteString(`<div class="content">`)
	sb.WriteString(htmlEscape(m.Content))
	sb.WriteString("</div>\n</div>\n")
}

// ToJSON marshals messages into structured JSON with metadata.
func ToJSON(messages []Message, cfg ExportConfig) ([]byte, error) {
	type jsonExport struct {
		Title      string    `json:"title,omitempty"`
		ExportedAt time.Time `json:"exported_at"`
		Count      int       `json:"message_count"`
		Messages   []Message `json:"messages"`
	}

	doc := jsonExport{
		Title:      cfg.Title,
		ExportedAt: time.Now().UTC(),
		Count:      len(messages),
		Messages:   messages,
	}

	return json.MarshalIndent(doc, "", "  ")
}

// ToText renders messages as plain text.
func ToText(messages []Message, cfg ExportConfig) string {
	var sb strings.Builder

	if cfg.Title != "" {
		sb.WriteString(cfg.Title)
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("=", len(cfg.Title)))
		sb.WriteString("\n\n")
	}

	for i, m := range messages {
		writeTextMessage(&sb, m, cfg)
		if i < len(messages)-1 {
			sb.WriteString(strings.Repeat("-", 40))
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}

func writeTextMessage(sb *strings.Builder, m Message, cfg ExportConfig) {
	label := strings.ToUpper(m.Role)
	if m.ToolName != "" {
		label += " [" + m.ToolName + "]"
	}

	sb.WriteString(label)

	if cfg.IncludeTimestamps && !m.Timestamp.IsZero() {
		fmt.Fprintf(sb, "  (%s)", m.Timestamp.Format("2006-01-02 15:04:05"))
	}
	sb.WriteString("\n\n")

	if cfg.IncludeThinking && m.Thinking != "" {
		sb.WriteString("[Thinking]\n")
		sb.WriteString(m.Thinking)
		sb.WriteString("\n\n")
	}

	sb.WriteString(m.Content)
	sb.WriteString("\n\n")
}

// GenerateShareURL creates a self-contained data URI, or falls back to
// writing a temporary file and returning a file:// URL if the payload is
// too large for a data URI (> 128 KB).
func GenerateShareURL(data []byte) (string, error) {
	const maxDataURI = 128 * 1024

	if len(data) == 0 {
		return "", fmt.Errorf("shareexport: empty data")
	}

	if len(data) <= maxDataURI {
		encoded := base64.StdEncoding.EncodeToString(data)
		mime := detectMIME(data)
		return fmt.Sprintf("data:%s;base64,%s", mime, encoded), nil
	}

	// Payload too large for a data URI — write to a temp file.
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, "polypod-share-*.html")
	if err != nil {
		return "", fmt.Errorf("shareexport: create temp file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("shareexport: write temp file: %w", err)
	}

	return "file://" + f.Name(), nil
}

// filterMessages applies MaxMessages and IncludeToolCalls filters.
func filterMessages(messages []Message, cfg ExportConfig) []Message {
	var filtered []Message
	for _, m := range messages {
		if !cfg.IncludeToolCalls && m.Role == "tool" {
			continue
		}
		if !cfg.IncludeToolCalls && m.ToolName != "" {
			// Skip assistant messages that are pure tool calls.
			if m.Content == "" {
				continue
			}
		}
		filtered = append(filtered, m)
	}

	if cfg.MaxMessages > 0 && len(filtered) > cfg.MaxMessages {
		filtered = filtered[len(filtered)-cfg.MaxMessages:]
	}

	if !cfg.IncludeThinking {
		for i := range filtered {
			filtered[i].Thinking = ""
		}
	}

	return filtered
}

func roleHeading(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool Result"
	case "system":
		return "System"
	default:
		if len(role) == 0 {
			return "Unknown"
		}
		return strings.ToUpper(role[:1]) + role[1:]
	}
}

func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

func detectMIME(data []byte) string {
	s := string(data)
	switch {
	case len(data) > 0 && data[0] == '{':
		return "application/json"
	case strings.HasPrefix(s, "<!DOCTYPE html") || strings.HasPrefix(s, "<html"):
		return "text/html"
	default:
		return "text/plain"
	}
}
