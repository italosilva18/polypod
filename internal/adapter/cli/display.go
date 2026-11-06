package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ── ANSI color constants ────────────────────────────────────────────────────

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
	ansiWhite   = "\033[37m"
	ansiBgRed   = "\033[41m"
	ansiBgGreen = "\033[42m"
)

// ── Display styles (lipgloss) ───────────────────────────────────────────────

var (
	toolNameStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	toolArgKeyStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	toolArgValStyle = lipgloss.NewStyle().
			Foreground(colorWhiteDim)

	toolResultHeaderStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	toolTimingStyle = lipgloss.NewStyle().
			Foreground(colorSubtle)

	filePathStyle = lipgloss.NewStyle().
			Foreground(colorBlue).
			Underline(true)

	diffAddStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	diffDelStyle = lipgloss.NewStyle().
			Foreground(colorRed)

	diffHunkStyle = lipgloss.NewStyle().
			Foreground(colorCyan).
			Bold(true)

	diffHeaderStyle = lipgloss.NewStyle().
			Foreground(colorMagenta).
			Bold(true)

	progressBarFillStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	progressBarEmptyStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	progressPercentStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)
)

// ── Tool call display ───────────────────────────────────────────────────────

// FormatToolCallDisplay renders a visual representation of a tool invocation.
//
// Example output:
//
//	▶ read_file
//	  path: /src/main.go
//	  offset: 10
func FormatToolCallDisplay(toolName string, args map[string]any) string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(toolNameStyle.Render("▶ " + toolName))
	b.WriteString("\n")

	if len(args) > 0 {
		// Find max key length for alignment
		maxKey := 0
		for k := range args {
			if len(k) > maxKey {
				maxKey = len(k)
			}
		}

		for k, v := range args {
			padding := strings.Repeat(" ", maxKey-len(k))
			valStr := fmt.Sprintf("%v", v)
			// Truncate long values
			if len(valStr) > 80 {
				valStr = valStr[:77] + "..."
			}
			b.WriteString("    ")
			b.WriteString(toolArgKeyStyle.Render(k + padding + ": "))
			// Highlight file paths
			if isFilePath(valStr) {
				b.WriteString(filePathStyle.Render(valStr))
			} else {
				b.WriteString(toolArgValStyle.Render(valStr))
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// ── Tool result display ─────────────────────────────────────────────────────

// FormatToolResultDisplay renders the result of a tool call with timing info.
//
// Example output:
//
//	✓ read_file (120ms)
//	  <result content>
func FormatToolResultDisplay(toolName, result string, elapsed time.Duration) string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(toolResultHeaderStyle.Render("✓ " + toolName))
	b.WriteString(" ")
	b.WriteString(toolTimingStyle.Render("(" + formatDuration(elapsed) + ")"))
	b.WriteString("\n")

	if result != "" {
		lines := strings.Split(result, "\n")
		maxLines := 30
		for i, line := range lines {
			if i >= maxLines {
				remaining := len(lines) - maxLines
				b.WriteString("    ")
				b.WriteString(subtleStyle.Render(fmt.Sprintf("... +%d linhas", remaining)))
				b.WriteString("\n")
				break
			}
			b.WriteString("    ")
			b.WriteString(contentDimStyle.Render(line))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// FormatToolErrorDisplay renders a tool error.
func FormatToolErrorDisplay(toolName string, err error, elapsed time.Duration) string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(errorStyle.Render("✗ " + toolName))
	b.WriteString(" ")
	b.WriteString(toolTimingStyle.Render("(" + formatDuration(elapsed) + ")"))
	b.WriteString("\n")
	b.WriteString("    ")
	b.WriteString(errorStyle.Render(err.Error()))
	b.WriteString("\n")

	return b.String()
}

// ── Thinking indicator ──────────────────────────────────────────────────────

// thinkingFrames are the animation frames for the thinking indicator.
var thinkingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// FormatThinkingIndicator renders an animated thinking indicator with elapsed time.
// The frame is selected based on the elapsed duration so it animates over time.
func FormatThinkingIndicator(elapsed time.Duration) string {
	frameIdx := int(elapsed.Milliseconds()/100) % len(thinkingFrames)
	frame := thinkingFrames[frameIdx]

	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(streamingSpinnerStyle.Render(frame))
	b.WriteString(" ")
	b.WriteString(streamingDimStyle.Render("pensando... "))
	b.WriteString(streamingDimStyle.Render(formatDuration(elapsed)))

	return b.String()
}

// ── Progress bar ────────────────────────────────────────────────────────────

// FormatProgressBar renders a progress bar with the given completion percentage.
//
// Example: [████████░░░░░░░░░░░░] 42%
func FormatProgressBar(percent int, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// Reserve space for brackets and percentage label "  [" + "] XXX%"
	barWidth := width - 8
	if barWidth < 10 {
		barWidth = 10
	}

	filled := barWidth * percent / 100
	empty := barWidth - filled

	var b strings.Builder
	b.WriteString("  [")
	if filled > 0 {
		b.WriteString(progressBarFillStyle.Render(strings.Repeat("█", filled)))
	}
	if empty > 0 {
		b.WriteString(progressBarEmptyStyle.Render(strings.Repeat("░", empty)))
	}
	b.WriteString("] ")
	b.WriteString(progressPercentStyle.Render(fmt.Sprintf("%3d%%", percent)))

	return b.String()
}

// ── Diff display ────────────────────────────────────────────────────────────

// FormatDiffDisplay renders a unified diff with syntax highlighting.
// Addition lines (+) are green, deletion lines (-) are red, hunk headers
// (@@ ... @@) are cyan, and file headers (--- / +++) are magenta.
func FormatDiffDisplay(diff string) string {
	if diff == "" {
		return ""
	}

	var b strings.Builder
	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		b.WriteString("  ")

		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(diffHeaderStyle.Render(line))

		case strings.HasPrefix(line, "@@"):
			b.WriteString(diffHunkStyle.Render(line))

		case strings.HasPrefix(line, "+"):
			b.WriteString(diffAddStyle.Render(line))

		case strings.HasPrefix(line, "-"):
			b.WriteString(diffDelStyle.Render(line))

		case strings.HasPrefix(line, "diff "):
			b.WriteString(diffHeaderStyle.Render(line))

		case strings.HasPrefix(line, "index "):
			b.WriteString(subtleStyle.Render(line))

		default:
			b.WriteString(contentDimStyle.Render(line))
		}

		b.WriteString("\n")
	}

	return b.String()
}

// ── File edit / file write result rendering ────────────────────────────────

// diffAddLineStyle highlights inserted diff lines with green + a subtle dim
// background for slightly more contrast than plain foreground-only.
var diffAddLineStyle = lipgloss.NewStyle().
	Foreground(colorGreen).
	Background(lipgloss.Color("#0a2e16"))

// diffDelLineStyle highlights removed diff lines with red + a subtle dim
// background.
var diffDelLineStyle = lipgloss.NewStyle().
	Foreground(colorRed).
	Background(lipgloss.Color("#2e0a0a"))

// FormatFileEditResult detects when a tool result from file_edit / file_write /
// undo_edit contains a unified diff and renders it with ANSI colors (green for
// additions, red for deletions, cyan for hunk headers, magenta for file
// headers). The preamble (text before the diff) is rendered dimmed.
//
// If no diff is detected, the original content is returned wrapped in a
// standard tool result display so callers can always use this function safely.
func FormatFileEditResult(toolName, result string) string {
	if result == "" {
		return ""
	}

	// Locate the first unified-diff header. The file_edit / file_write tools
	// prepend a status line ("Updated /foo (123 bytes)\n\n") followed by the
	// diff starting with "--- a/...".
	diffStart := findDiffStart(result)
	if diffStart < 0 {
		// No diff → render as plain, indented text.
		return renderPlainToolResult(result)
	}

	var b strings.Builder

	// Render preamble (status line / header) dimmed.
	preamble := strings.TrimRight(result[:diffStart], "\n")
	if preamble != "" {
		for _, line := range strings.Split(preamble, "\n") {
			b.WriteString("    ")
			b.WriteString(contentDimStyle.Render(line))
			b.WriteString("\n")
		}
	}

	// Render the diff body with colors.
	b.WriteString(renderDiffLines(result[diffStart:]))

	return b.String()
}

// renderPlainToolResult indents and dims non-diff tool result output,
// matching the style FormatToolResultDisplay uses for the body.
func renderPlainToolResult(result string) string {
	var b strings.Builder
	lines := strings.Split(result, "\n")
	maxLines := 30
	for i, line := range lines {
		if i >= maxLines {
			remaining := len(lines) - maxLines
			b.WriteString("    ")
			b.WriteString(subtleStyle.Render(fmt.Sprintf("... +%d linhas", remaining)))
			b.WriteString("\n")
			break
		}
		b.WriteString("    ")
		b.WriteString(contentDimStyle.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

// renderDiffLines applies per-line color to unified diff content.
//
//   - "---" / "+++"  → magenta (file headers)
//   - "@@"           → cyan (hunk headers)
//   - "+"            → green on dim green background
//   - "-"            → red on dim red background
//   - " " / other    → dim (context)
func renderDiffLines(diff string) string {
	var b strings.Builder
	lines := strings.Split(diff, "\n")

	// Cap rendered diff size to avoid flooding the TUI on huge edits.
	const maxDiffLines = 120
	truncated := false
	if len(lines) > maxDiffLines {
		lines = lines[:maxDiffLines]
		truncated = true
	}

	for _, line := range lines {
		b.WriteString("    ")
		switch {
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			b.WriteString(diffHeaderStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(diffHunkStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(diffAddLineStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(diffDelLineStyle.Render(line))
		default:
			b.WriteString(contentDimStyle.Render(line))
		}
		b.WriteString("\n")
	}

	if truncated {
		b.WriteString("    ")
		b.WriteString(subtleStyle.Render("... (diff truncado)"))
		b.WriteString("\n")
	}

	return b.String()
}

// isDiffContent reports whether s looks like a unified diff (contains at
// least one "--- a/" header followed by a "+++ b/" header).
func isDiffContent(s string) bool {
	return findDiffStart(s) >= 0
}

// findDiffStart returns the byte index of the first unified-diff header in s,
// or -1 if none is found. It recognises the canonical "--- a/...\n+++ b/..."
// sequence produced by generateUnifiedDiff in the fileedit tool.
func findDiffStart(s string) int {
	idx := 0
	for idx < len(s) {
		next := strings.Index(s[idx:], "--- ")
		if next < 0 {
			return -1
		}
		abs := idx + next
		// Must be at start-of-string or right after a newline.
		if abs != 0 && s[abs-1] != '\n' {
			idx = abs + 1
			continue
		}
		// Must have "+++ " on the following line.
		rest := s[abs:]
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			return -1
		}
		after := rest[nl+1:]
		if strings.HasPrefix(after, "+++ ") {
			return abs
		}
		idx = abs + 1
	}
	return -1
}

// ── Notification formatting ─────────────────────────────────────────────────

// FormatWarning renders a warning message with a ⚠ prefix.
func FormatWarning(msg string) string {
	return "  " + warningStyle.Render("⚠ "+msg)
}

// FormatSuccess renders a success message with a ✓ prefix.
func FormatSuccess(msg string) string {
	return "  " + successStyle.Render("✓ "+msg)
}

// FormatError renders an error message with a ✗ prefix.
func FormatError(msg string) string {
	return "  " + errorStyle.Render("✗ "+msg)
}

// FormatInfo renders an informational message with a ℹ prefix.
func FormatInfo(msg string) string {
	return "  " + notifyInfoStyle.Render("ℹ "+msg)
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// isFileEditTool reports whether name is a tool whose result should be
// rendered with diff-aware highlighting.
func isFileEditTool(name string) bool {
	switch name {
	case "file_edit", "file_write", "undo_edit", "edit_file", "create_file", "write_file":
		return true
	}
	return false
}

// isFilePath checks if a string looks like a file path.
func isFilePath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	// Check for common file extensions
	exts := []string{".go", ".js", ".ts", ".py", ".rs", ".md", ".yaml", ".yml", ".json", ".toml", ".txt", ".sh"}
	for _, ext := range exts {
		if strings.HasSuffix(s, ext) {
			return true
		}
	}
	return false
}
