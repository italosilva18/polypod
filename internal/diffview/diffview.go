package diffview

import (
	"fmt"
	"strings"
)

// DiffHunk represents a single change hunk in a diff.
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffLine is a single line in a diff.
type DiffLine struct {
	Type    string // "+", "-", " "
	Content string
}

// FileDiff represents changes to a single file.
type FileDiff struct {
	Path    string
	Hunks   []DiffHunk
	Added   int
	Removed int
}

// FormatColorDiff renders a diff with ANSI colors for terminal display.
func FormatColorDiff(oldContent, newContent, path string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\033[1m--- a/%s\033[0m\n", path))
	sb.WriteString(fmt.Sprintf("\033[1m+++ b/%s\033[0m\n", path))

	// Simple line-by-line diff (Myers would be better but this works)
	i, j := 0, 0
	for i < len(oldLines) || j < len(newLines) {
		if i < len(oldLines) && j < len(newLines) && oldLines[i] == newLines[j] {
			sb.WriteString(fmt.Sprintf("  %s\n", oldLines[i]))
			i++
			j++
		} else if i < len(oldLines) && (j >= len(newLines) || !containsAfter(newLines, j, oldLines[i])) {
			sb.WriteString(fmt.Sprintf("\033[31m- %s\033[0m\n", oldLines[i]))
			i++
		} else if j < len(newLines) {
			sb.WriteString(fmt.Sprintf("\033[32m+ %s\033[0m\n", newLines[j]))
			j++
		}
	}

	return sb.String()
}

// FormatPreview creates a preview of what edit_file would change.
func FormatPreview(path, oldText, newText string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("\033[1;36m📝 Alteracao proposta em %s\033[0m\n", path))
	sb.WriteString("────────────────────────────────────────\n")

	// Show old text in red
	for _, line := range strings.Split(oldText, "\n") {
		sb.WriteString(fmt.Sprintf("\033[31m- %s\033[0m\n", line))
	}

	// Show new text in green
	for _, line := range strings.Split(newText, "\n") {
		sb.WriteString(fmt.Sprintf("\033[32m+ %s\033[0m\n", line))
	}

	sb.WriteString("────────────────────────────────────────\n")
	return sb.String()
}

// FormatFilesDiff shows a GitHub-style "files changed" summary.
func FormatFilesDiff(diffs []FileDiff) string {
	var sb strings.Builder
	totalAdded := 0
	totalRemoved := 0

	sb.WriteString("\033[1m📊 Arquivos alterados:\033[0m\n\n")

	for _, d := range diffs {
		totalAdded += d.Added
		totalRemoved += d.Removed

		bar := makeBar(d.Added, d.Removed, 30)
		sb.WriteString(fmt.Sprintf("  %-40s %s +%d -%d\n", d.Path, bar, d.Added, d.Removed))
	}

	sb.WriteString(fmt.Sprintf("\n  %d arquivo(s) alterado(s), \033[32m+%d\033[0m \033[31m-%d\033[0m\n",
		len(diffs), totalAdded, totalRemoved))

	return sb.String()
}

func makeBar(added, removed, width int) string {
	total := added + removed
	if total == 0 {
		return strings.Repeat("░", width)
	}
	greenCount := (added * width) / total
	redCount := (removed * width) / total
	if greenCount+redCount < width {
		greenCount++ // rounding
	}

	return "\033[32m" + strings.Repeat("█", greenCount) + "\033[31m" + strings.Repeat("█", redCount) + "\033[0m"
}

func containsAfter(lines []string, start int, target string) bool {
	for i := start; i < len(lines) && i < start+5; i++ {
		if lines[i] == target {
			return true
		}
	}
	return false
}
