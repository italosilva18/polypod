// Package cli — tool rendering registry.
//
// TypeScript tools can provide custom rendering via `renderToolUseMessage()`
// and `renderToolResultMessage()`. Go's Tool interface is headless and does
// not support per-tool rendering hooks, so this file provides a parallel
// mechanism: a registry that maps tool names to rendering functions. The
// adapter can look up a custom renderer per tool without the core Tool
// interface having to change.
//
// A caller typically does:
//
//	reg := DefaultRenderers()
//	if out, ok := reg.RenderUse("bash", input); ok {
//	    // print styled output
//	} else {
//	    // fall back to FormatToolCallDisplay
//	}
//
// Or, more simply:
//
//	out := RenderWithFallback(reg, "bash", input)
package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// ── Renderer types ──────────────────────────────────────────────────────────

// ToolRenderer holds custom rendering callbacks for a single tool. Either
// field may be nil, in which case the registry will fall through to default
// rendering for that half of the tool call lifecycle.
type ToolRenderer struct {
	// RenderUse formats the tool invocation (before execution).
	RenderUse func(toolName string, input map[string]any) string
	// RenderResult formats the tool output (after execution). isError
	// indicates the tool reported a failure.
	RenderResult func(toolName string, input map[string]any, output string, isError bool) string
}

// ToolRendererRegistry is a thread-safe lookup of tool name → renderer.
type ToolRendererRegistry struct {
	mu        sync.RWMutex
	renderers map[string]*ToolRenderer
}

// NewToolRendererRegistry returns an empty registry.
func NewToolRendererRegistry() *ToolRendererRegistry {
	return &ToolRendererRegistry{
		renderers: make(map[string]*ToolRenderer),
	}
}

// Register associates a renderer with a tool name. A nil renderer clears any
// existing registration.
func (r *ToolRendererRegistry) Register(toolName string, renderer *ToolRenderer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if renderer == nil {
		delete(r.renderers, toolName)
		return
	}
	r.renderers[toolName] = renderer
}

// get returns the renderer for toolName, or nil if none is registered.
func (r *ToolRendererRegistry) get(toolName string) *ToolRenderer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.renderers[toolName]
}

// Has reports whether a renderer is registered for toolName.
func (r *ToolRendererRegistry) Has(toolName string) bool {
	return r.get(toolName) != nil
}

// Names returns all registered tool names (useful for tests / diagnostics).
func (r *ToolRendererRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.renderers))
	for k := range r.renderers {
		out = append(out, k)
	}
	return out
}

// RenderUse looks up a custom renderer and invokes its RenderUse callback.
// When no custom renderer exists (or the callback is nil), it returns
// FormatToolCallDisplay output together with ok=false so callers can tell
// whether styling came from a dedicated renderer.
func (r *ToolRendererRegistry) RenderUse(toolName string, input map[string]any) (string, bool) {
	if rd := r.get(toolName); rd != nil && rd.RenderUse != nil {
		return rd.RenderUse(toolName, input), true
	}
	return FormatToolCallDisplay(toolName, input), false
}

// RenderResult looks up a custom renderer and invokes its RenderResult
// callback. When no custom renderer exists (or the callback is nil), it
// returns a plain indented result block with ok=false.
func (r *ToolRendererRegistry) RenderResult(toolName string, input map[string]any, output string, isError bool) (string, bool) {
	if rd := r.get(toolName); rd != nil && rd.RenderResult != nil {
		return rd.RenderResult(toolName, input, output, isError), true
	}
	return defaultResultRender(toolName, output, isError), false
}

// RenderWithFallback is a convenience that returns the custom rendering when
// one exists, or FormatToolCallDisplay otherwise. Safe to call with a nil
// registry.
func RenderWithFallback(reg *ToolRendererRegistry, toolName string, input map[string]any) string {
	if reg == nil {
		return FormatToolCallDisplay(toolName, input)
	}
	out, _ := reg.RenderUse(toolName, input)
	return out
}

// DefaultRenderer returns a fallback renderer for unknown tools that dumps
// the input and output as JSON. It is not auto-registered; callers may use it
// explicitly or wire it into their own registry.
func DefaultRenderer() *ToolRenderer {
	return &ToolRenderer{
		RenderUse: func(toolName string, input map[string]any) string {
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolNameStyle.Render("▶ " + toolName))
			b.WriteString("\n")
			if len(input) > 0 {
				raw, err := json.MarshalIndent(input, "    ", "  ")
				if err != nil {
					raw = []byte(fmt.Sprintf("%v", input))
				}
				b.WriteString("    ")
				b.WriteString(toolArgValStyle.Render(string(raw)))
				b.WriteString("\n")
			}
			return b.String()
		},
		RenderResult: func(toolName string, input map[string]any, output string, isError bool) string {
			return defaultResultRender(toolName, output, isError)
		},
	}
}

// defaultResultRender is the shared default for result rendering.
func defaultResultRender(toolName, output string, isError bool) string {
	var b strings.Builder
	b.WriteString("  ")
	if isError {
		b.WriteString(errorStyle.Render("✗ " + toolName))
	} else {
		b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
	}
	b.WriteString("\n")
	if output != "" {
		lines := strings.Split(output, "\n")
		const maxLines = 30
		for i, line := range lines {
			if i >= maxLines {
				rem := len(lines) - maxLines
				b.WriteString("    ")
				b.WriteString(subtleStyle.Render(fmt.Sprintf("... +%d linhas", rem)))
				b.WriteString("\n")
				break
			}
			b.WriteString("    ")
			if isError {
				b.WriteString(errorStyle.Render(line))
			} else {
				b.WriteString(contentDimStyle.Render(line))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ── Default renderers ──────────────────────────────────────────────────────

// toolRenderHeaderStyle is used by custom renderers for the per-tool header.
var (
	toolRenderHeaderStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	toolRenderMonoStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	toolRenderErrorStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				Bold(true)

	toolRenderAccentStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	toolRenderMetaStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)

	toolRenderPathStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Underline(true)
)

// DefaultRenderers returns a registry pre-populated with renderers for the
// tools commonly surfaced in Polypod's CLI: bash, read_file / file_read,
// file_edit / edit_file, grep / search_files, glob, and web_fetch.
func DefaultRenderers() *ToolRendererRegistry {
	reg := NewToolRendererRegistry()

	// ── bash ────────────────────────────────────────────────────────────
	bash := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			cmd := stringField(input, "command", "cmd", "script")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("$ "))
			b.WriteString(toolRenderMonoStyle.Render(cmd))
			if desc := stringField(input, "description", "desc"); desc != "" {
				b.WriteString("\n    ")
				b.WriteString(toolRenderMetaStyle.Render(desc))
			}
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(_ string, _ map[string]any, output string, isError bool) string {
			var b strings.Builder
			b.WriteString("  ")
			if isError {
				b.WriteString(toolRenderErrorStyle.Render("✗ bash"))
			} else {
				b.WriteString(toolResultHeaderStyle.Render("✓ bash"))
			}
			b.WriteString("\n")
			if output != "" {
				lines := strings.Split(output, "\n")
				const maxLines = 30
				for i, line := range lines {
					if i >= maxLines {
						rem := len(lines) - maxLines
						b.WriteString("    ")
						b.WriteString(subtleStyle.Render(fmt.Sprintf("... +%d linhas", rem)))
						b.WriteString("\n")
						break
					}
					b.WriteString("    ")
					if isError {
						b.WriteString(toolRenderErrorStyle.Render(line))
					} else {
						b.WriteString(toolRenderMonoStyle.Render(line))
					}
					b.WriteString("\n")
				}
			}
			return b.String()
		},
	}
	reg.Register("bash", bash)
	reg.Register("shell", bash)

	// ── read_file / file_read ───────────────────────────────────────────
	readFile := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			path := stringField(input, "path", "file_path", "filename")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("📄 "))
			b.WriteString(toolRenderPathStyle.Render(path))
			if off := stringField(input, "offset"); off != "" {
				b.WriteString(" ")
				b.WriteString(toolRenderMetaStyle.Render("offset=" + off))
			}
			if lim := stringField(input, "limit"); lim != "" {
				b.WriteString(" ")
				b.WriteString(toolRenderMetaStyle.Render("limit=" + lim))
			}
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(toolName string, _ map[string]any, output string, isError bool) string {
			if isError {
				return defaultResultRender(toolName, output, true)
			}
			lines := strings.Split(output, "\n")
			var preview []string
			for i, ln := range lines {
				if i >= 5 {
					break
				}
				preview = append(preview, ln)
			}
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
			b.WriteString(" ")
			b.WriteString(toolRenderMetaStyle.Render(fmt.Sprintf("(%d linhas)", len(lines))))
			b.WriteString("\n")
			for _, ln := range preview {
				b.WriteString("    ")
				b.WriteString(contentDimStyle.Render(ln))
				b.WriteString("\n")
			}
			if len(lines) > len(preview) {
				b.WriteString("    ")
				b.WriteString(subtleStyle.Render(fmt.Sprintf("... +%d linhas", len(lines)-len(preview))))
				b.WriteString("\n")
			}
			return b.String()
		},
	}
	reg.Register("read_file", readFile)
	reg.Register("file_read", readFile)
	reg.Register("Read", readFile)

	// ── file_edit / edit_file ───────────────────────────────────────────
	editFile := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			path := stringField(input, "path", "file_path", "filename")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("✎ "))
			b.WriteString(toolRenderPathStyle.Render(path))
			// Summarize change
			oldS := stringField(input, "old_string", "old_str", "find")
			newS := stringField(input, "new_string", "new_str", "replace")
			if oldS != "" || newS != "" {
				summary := fmt.Sprintf(" (-%d +%d chars)", len(oldS), len(newS))
				b.WriteString(toolRenderMetaStyle.Render(summary))
			}
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(toolName string, _ map[string]any, output string, isError bool) string {
			if isError {
				return defaultResultRender(toolName, output, true)
			}
			// Use the existing diff-aware formatter for pretty output.
			return FormatFileEditResult(toolName, output)
		},
	}
	reg.Register("file_edit", editFile)
	reg.Register("edit_file", editFile)
	reg.Register("file_write", editFile)
	reg.Register("write_file", editFile)
	reg.Register("Edit", editFile)
	reg.Register("Write", editFile)

	// ── grep / search_files ─────────────────────────────────────────────
	grep := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			pat := stringField(input, "pattern", "query", "regex")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("🔍 "))
			b.WriteString(toolRenderAccentStyle.Render(pat))
			if path := stringField(input, "path", "dir", "directory"); path != "" {
				b.WriteString(" ")
				b.WriteString(toolRenderMetaStyle.Render("in " + path))
			}
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(toolName string, _ map[string]any, output string, isError bool) string {
			if isError {
				return defaultResultRender(toolName, output, true)
			}
			count := countNonEmptyLines(output)
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
			b.WriteString(" ")
			b.WriteString(toolRenderMetaStyle.Render(fmt.Sprintf("(%d matches)", count)))
			b.WriteString("\n")
			return b.String()
		},
	}
	reg.Register("grep", grep)
	reg.Register("search_files", grep)
	reg.Register("Grep", grep)

	// ── glob ────────────────────────────────────────────────────────────
	glob := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			pat := stringField(input, "pattern", "glob", "query")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("* "))
			b.WriteString(toolRenderAccentStyle.Render(pat))
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(toolName string, _ map[string]any, output string, isError bool) string {
			if isError {
				return defaultResultRender(toolName, output, true)
			}
			count := countNonEmptyLines(output)
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
			b.WriteString(" ")
			b.WriteString(toolRenderMetaStyle.Render(fmt.Sprintf("(%d matches)", count)))
			b.WriteString("\n")
			return b.String()
		},
	}
	reg.Register("glob", glob)
	reg.Register("Glob", glob)

	// ── web_fetch ───────────────────────────────────────────────────────
	webFetch := &ToolRenderer{
		RenderUse: func(_ string, input map[string]any) string {
			url := stringField(input, "url", "URL", "link")
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolRenderHeaderStyle.Render("🌐 "))
			b.WriteString(toolRenderPathStyle.Render(url))
			b.WriteString("\n")
			return b.String()
		},
		RenderResult: func(toolName string, _ map[string]any, output string, isError bool) string {
			if isError {
				return defaultResultRender(toolName, output, true)
			}
			title, preview := extractTitleAndPreview(output, 100)
			var b strings.Builder
			b.WriteString("  ")
			b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
			b.WriteString("\n")
			if title != "" {
				b.WriteString("    ")
				b.WriteString(toolRenderHeaderStyle.Render(title))
				b.WriteString("\n")
			}
			if preview != "" {
				b.WriteString("    ")
				b.WriteString(contentDimStyle.Render(preview))
				b.WriteString("\n")
			}
			return b.String()
		},
	}
	reg.Register("web_fetch", webFetch)
	reg.Register("webfetch", webFetch)
	reg.Register("WebFetch", webFetch)
	reg.Register("fetch_url", webFetch)

	return reg
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// stringField returns the first string value found for any of the given keys
// in input. Non-string values are formatted with fmt.Sprintf("%v", ...).
func stringField(input map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := input[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// countNonEmptyLines returns the number of non-blank lines in s.
func countNonEmptyLines(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// extractTitleAndPreview returns a best-effort (title, preview) pair from an
// HTML / plain text document. The title is the first line of content; the
// preview is up to maxChars of the remaining text with surrounding whitespace
// collapsed.
func extractTitleAndPreview(s string, maxChars int) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	// Detect a simple <title>…</title> block.
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "<title>"); i >= 0 {
		j := strings.Index(lower[i:], "</title>")
		if j >= 0 {
			title := strings.TrimSpace(s[i+len("<title>") : i+j])
			rest := s[i+j+len("</title>"):]
			return title, previewText(rest, maxChars)
		}
	}
	// Otherwise split on first newline.
	if nl := strings.Index(s, "\n"); nl >= 0 {
		return strings.TrimSpace(s[:nl]), previewText(s[nl+1:], maxChars)
	}
	return previewText(s, maxChars), ""
}

// previewText collapses whitespace in s and truncates it to maxChars.
func previewText(s string, maxChars int) string {
	fields := strings.Fields(s)
	joined := strings.Join(fields, " ")
	if len(joined) > maxChars {
		joined = joined[:maxChars] + "..."
	}
	return joined
}
