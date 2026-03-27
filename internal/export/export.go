package export

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Message for export purposes.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
}

// Session metadata for export.
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Messages  []Message `json:"messages"`
	Cost      float64   `json:"cost_usd"`
	Tokens    int       `json:"total_tokens"`
}

// ToMarkdown exports a session as clean markdown.
func ToMarkdown(s Session) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", s.Title)
	fmt.Fprintf(&sb, "**Session**: %s | **Model**: %s | **Date**: %s\n\n", s.ID, s.Model, s.CreatedAt.Format("2006-01-02 15:04"))
	if s.Cost > 0 {
		fmt.Fprintf(&sb, "**Cost**: $%.4f | **Tokens**: %d\n\n", s.Cost, s.Tokens)
	}
	sb.WriteString("---\n\n")

	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			fmt.Fprintf(&sb, "## User\n\n%s\n\n", m.Content)
		case "assistant":
			fmt.Fprintf(&sb, "## Assistant\n\n%s\n\n", m.Content)
		case "system":
			// Skip system messages in export
		case "tool":
			fmt.Fprintf(&sb, "> **Tool**: %s\n>\n> ```\n> %s\n> ```\n\n",
				m.ToolName, indent(m.Content, "> "))
		}
	}
	return sb.String()
}

// ToJSON exports a session as structured JSON.
func ToJSON(s Session) (string, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ToHTML exports a session as standalone HTML with syntax highlighting.
func ToHTML(s Session) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + escape(s.Title) + `</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}body{font-family:system-ui,-apple-system,sans-serif;background:#0f0f0f;color:#e0e0e0;max-width:900px;margin:0 auto;padding:24px}
h1{font-size:24px;margin-bottom:16px;color:#fff}
.meta{color:#888;font-size:13px;margin-bottom:24px;padding-bottom:16px;border-bottom:1px solid #333}
.msg{margin-bottom:24px;padding:16px;border-radius:12px;line-height:1.6}
.msg.user{background:#1a2a3a;border-left:3px solid #4a9fd4}
.msg.assistant{background:#1a1a2e;border-left:3px solid #9b59b6}
.msg.tool{background:#1a2a1a;border-left:3px solid #4caf50;font-family:monospace;font-size:13px;white-space:pre-wrap}
.role{font-size:11px;text-transform:uppercase;letter-spacing:1px;color:#888;margin-bottom:8px}
pre{background:#000;padding:12px;border-radius:8px;overflow-x:auto;font-size:13px;margin:8px 0}
code{font-family:'Fira Code',monospace;font-size:13px;background:#111;padding:2px 6px;border-radius:4px}
</style></head><body>
<h1>` + escape(s.Title) + `</h1>
<div class="meta">Session: ` + s.ID + ` | Model: ` + s.Model + ` | ` + s.CreatedAt.Format("2006-01-02 15:04") + `</div>
`)

	for _, m := range s.Messages {
		if m.Role == "system" {
			continue
		}
		class := m.Role
		sb.WriteString(`<div class="msg ` + class + `">`)
		sb.WriteString(`<div class="role">` + m.Role + `</div>`)
		sb.WriteString(markdownToHTML(m.Content))
		sb.WriteString("</div>\n")
	}

	sb.WriteString("</body></html>")
	return sb.String()
}

// ToNotebook exports as Jupyter notebook (.ipynb).
func ToNotebook(s Session) (string, error) {
	notebook := map[string]any{
		"nbformat":       4,
		"nbformat_minor": 5,
		"metadata": map[string]any{
			"kernelspec": map[string]string{
				"display_name": "Polypod",
				"language":     "text",
				"name":         "polypod",
			},
		},
		"cells": buildCells(s.Messages),
	}
	data, err := json.MarshalIndent(notebook, "", "  ")
	return string(data), err
}

func buildCells(messages []Message) []map[string]any {
	var cells []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		cellType := "markdown"
		if m.Role == "tool" {
			cellType = "code"
		}
		cells = append(cells, map[string]any{
			"cell_type": cellType,
			"metadata":  map[string]any{"role": m.Role},
			"source":    []string{m.Content},
			"outputs":   []any{},
		})
	}
	return cells
}

// WriteFile saves exported content to a file.
func WriteFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func escape(s string) string {
	r := strings.NewReplacer("<", "&lt;", ">", "&gt;", "&", "&amp;", `"`, "&quot;")
	return r.Replace(s)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func markdownToHTML(md string) string {
	// Simple markdown → HTML conversion for code blocks
	md = escape(md)
	md = strings.ReplaceAll(md, "\n```\n", "\n</pre>\n")
	if strings.Count(md, "```") > 0 {
		md = strings.Replace(md, "```", "<pre>", 1)
	}
	md = strings.ReplaceAll(md, "\n", "<br>\n")
	return md
}
