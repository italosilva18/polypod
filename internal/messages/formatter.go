package messages

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FormatToolCallHeader returns a compact one-line display for a tool invocation.
//
//	Example: "Bash(command: "ls -la", workdir: "/tmp")"
func FormatToolCallHeader(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return toolName + "()"
	}

	var parts []string
	for k, v := range args {
		s := formatArgValue(v)
		parts = append(parts, fmt.Sprintf("%s: %s", k, s))
	}
	// Keep it compact: truncate the entire args region at 120 chars.
	joined := strings.Join(parts, ", ")
	if len(joined) > 120 {
		joined = joined[:117] + "..."
	}
	return toolName + "(" + joined + ")"
}

// FormatToolResult formats a tool result for display, with error styling.
func FormatToolResult(toolName string, result string, isError bool) string {
	prefix := toolName
	if isError {
		prefix += " [ERROR]"
	}

	result = strings.TrimSpace(result)
	lines := strings.Split(result, "\n")

	if len(lines) == 1 && len(result) <= 200 {
		return fmt.Sprintf("%s: %s", prefix, result)
	}

	// Multi-line: indent under the header.
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s:\n", prefix)
	for _, line := range lines {
		sb.WriteString("  ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}

// FormatTokenUsage returns a compact usage summary string.
//
//	Example: "1.2K prompt + 856 completion = 2.1K tokens ($0.0234)"
func FormatTokenUsage(prompt, completion, total int, cost float64) string {
	if total == 0 {
		total = prompt + completion
	}
	costStr := formatCost(cost)
	return fmt.Sprintf("%s prompt + %s completion = %s tokens (%s)",
		FormatNumber(prompt), FormatNumber(completion), FormatNumber(total), costStr)
}

// FormatDurationHuman formats a time.Duration in human-friendly notation.
//
//	Examples: "230ms", "4.2s", "1m 23s", "2h 5m"
func FormatDurationHuman(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		secs := d.Seconds()
		if secs == float64(int(secs)) {
			return fmt.Sprintf("%ds", int(secs))
		}
		return fmt.Sprintf("%.1fs", secs)
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours >= 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}

// FormatFileSize formats a byte count in human-friendly notation.
//
//	Examples: "128 B", "4.5 KB", "12.3 MB", "1.1 GB"
func FormatFileSize(bytes int64) string {
	if bytes < 0 {
		return "0 B"
	}
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(tb))
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// TruncateWithEllipsis truncates s to maxLen runes, appending "..." if truncated.
// Unlike Truncate (which uses "…"), this uses three ASCII dots for terminal compatibility.
func TruncateWithEllipsis(s string, maxLen int) string {
	if maxLen <= 0 {
		return "..."
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// FormatErrorWithContext returns a formatted error string with additional context.
//
//	Example: "[file read] open /foo: permission denied"
func FormatErrorWithContext(err error, context string) string {
	if err == nil {
		return ""
	}
	if context == "" {
		return err.Error()
	}
	return fmt.Sprintf("[%s] %s", context, err.Error())
}

// FormatMarkdownTable builds a GitHub-flavored markdown table from headers and rows.
// All columns are left-aligned. Cells are padded for visual alignment.
func FormatMarkdownTable(headers []string, rows [][]string) string {
	if len(headers) == 0 {
		return ""
	}

	cols := len(headers)

	// Compute column widths.
	widths := make([]int, cols)
	for i, h := range headers {
		if len(h) > widths[i] {
			widths[i] = len(h)
		}
	}
	for _, row := range rows {
		for i := 0; i < cols && i < len(row); i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	var sb strings.Builder

	// Header row.
	sb.WriteByte('|')
	for i, h := range headers {
		fmt.Fprintf(&sb, " %-*s |", widths[i], h)
	}
	sb.WriteByte('\n')

	// Separator row.
	sb.WriteByte('|')
	for _, w := range widths {
		sb.WriteByte('-')
		sb.WriteString(strings.Repeat("-", w))
		sb.WriteString("-|")
	}
	sb.WriteByte('\n')

	// Data rows.
	for _, row := range rows {
		sb.WriteByte('|')
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			fmt.Fprintf(&sb, " %-*s |", widths[i], cell)
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// --- helpers ----------------------------------------------------------------

func formatArgValue(v any) string {
	switch val := v.(type) {
	case string:
		if len(val) > 60 {
			return `"` + val[:57] + `..."`
		}
		return `"` + val + `"`
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		if val == float64(int(val)) {
			return fmt.Sprintf("%d", int(val))
		}
		return fmt.Sprintf("%.2f", val)
	case int:
		return fmt.Sprintf("%d", val)
	case nil:
		return "null"
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		s := string(data)
		if len(s) > 60 {
			return s[:57] + "..."
		}
		return s
	}
}

func formatCost(cost float64) string {
	if cost >= 0.5 {
		return fmt.Sprintf("$%.2f", cost)
	}
	return fmt.Sprintf("$%.4f", cost)
}
