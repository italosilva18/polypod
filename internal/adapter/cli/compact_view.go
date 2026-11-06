package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// CompactResult holds the data needed to render a visual summary of a
// conversation compaction operation.
type CompactResult struct {
	MessagesBefore int
	MessagesAfter  int
	TokensBefore   int
	TokensAfter    int
	Summary        string   // The compaction summary text (full prose)
	RetainedTopics []string // Key topics kept after compaction
	DroppedCount   int      // How many messages were collapsed away
}

// ── Compact view styles ─────────────────────────────────────────────────────

var (
	compactBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan).
			Padding(1, 2)

	compactTitleStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	compactSectionStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim).
				Bold(true)

	compactOldNumStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				Bold(true)

	compactNewNumStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	compactDeltaStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	compactBulletStyle = lipgloss.NewStyle().
				Foreground(colorCyan)

	compactBodyStyle = lipgloss.NewStyle().
				Foreground(colorWhite)

	compactDimBodyStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	compactTipStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)
)

// FormatCompactResult renders the result of a compaction as a styled box
// summarizing how many messages and tokens were collapsed, the summary
// produced, and the topics retained.
//
// The width parameter controls the outer width of the rendered box.
// Values < 50 are clamped to 50 to keep the layout legible.
func FormatCompactResult(r CompactResult, width int) string {
	if width < 50 {
		width = 50
	}
	// Account for box border + padding (2 borders + 2*2 padding = 6 cols).
	innerWidth := width - 6
	if innerWidth < 30 {
		innerWidth = 30
	}

	var b strings.Builder

	// Title
	b.WriteString(compactTitleStyle.Render("📦 CONVERSATION COMPACTED"))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", innerWidth)))
	b.WriteString("\n\n")

	// Stats line: messages
	msgDelta := r.MessagesBefore - r.MessagesAfter
	b.WriteString(compactSectionStyle.Render("Mensagens:  "))
	b.WriteString(compactOldNumStyle.Render(fmt.Sprintf("%d", r.MessagesBefore)))
	b.WriteString(compactDimBodyStyle.Render(" → "))
	b.WriteString(compactNewNumStyle.Render(fmt.Sprintf("%d", r.MessagesAfter)))
	b.WriteString("  ")
	b.WriteString(compactDeltaStyle.Render(fmt.Sprintf("(-%d)", msgDelta)))
	b.WriteString("\n")

	// Stats line: tokens
	b.WriteString(compactSectionStyle.Render("Tokens:     "))
	b.WriteString(compactOldNumStyle.Render(formatTokenCount(r.TokensBefore)))
	b.WriteString(compactDimBodyStyle.Render(" → "))
	b.WriteString(compactNewNumStyle.Render(formatTokenCount(r.TokensAfter)))
	if r.TokensBefore > 0 {
		pct := 100 - (r.TokensAfter * 100 / r.TokensBefore)
		b.WriteString("  ")
		b.WriteString(compactDeltaStyle.Render(fmt.Sprintf("(-%d%%)", pct)))
	}
	b.WriteString("\n\n")

	// Summary section
	if r.Summary != "" {
		b.WriteString(compactSectionStyle.Render("RESUMO:"))
		b.WriteString("\n")
		bullets := summaryToBullets(r.Summary, 8)
		for _, bullet := range bullets {
			b.WriteString("  ")
			b.WriteString(compactBulletStyle.Render("• "))
			b.WriteString(compactBodyStyle.Render(truncateLine(bullet, innerWidth-4)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Retained topics
	if len(r.RetainedTopics) > 0 {
		b.WriteString(compactSectionStyle.Render("TOPICOS MANTIDOS:"))
		b.WriteString("\n")
		for _, topic := range r.RetainedTopics {
			b.WriteString("  ")
			b.WriteString(compactBulletStyle.Render("• "))
			b.WriteString(compactBodyStyle.Render(truncateLine(topic, innerWidth-4)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Tip
	b.WriteString(compactTipStyle.Render("💡 Use /compact novamente quando atingir 80% do contexto"))

	return compactBoxStyle.Width(width).Render(b.String())
}

// ── helpers ─────────────────────────────────────────────────────────────────

// formatTokenCount is provided by commands_new.go and renders an integer
// token count in human-friendly form (e.g. 85432 → "85k", 1.8M).

// summaryToBullets extracts bullet-like lines from a free-form summary.
//
// Strategy:
//  1. If the summary already contains markdown bullets ("- ", "* ", "• "),
//     extract those.
//  2. Otherwise split on lines and use non-empty trimmed lines.
//  3. Cap to maxBullets entries.
func summaryToBullets(summary string, maxBullets int) []string {
	if maxBullets <= 0 {
		maxBullets = 8
	}
	var bullets []string
	lines := strings.Split(summary, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Strip common bullet prefixes.
		switch {
		case strings.HasPrefix(trimmed, "- "):
			trimmed = strings.TrimPrefix(trimmed, "- ")
		case strings.HasPrefix(trimmed, "* "):
			trimmed = strings.TrimPrefix(trimmed, "* ")
		case strings.HasPrefix(trimmed, "• "):
			trimmed = strings.TrimPrefix(trimmed, "• ")
		}
		// Skip section headers like "Summary:" or "Analysis:" etc.
		if strings.HasSuffix(trimmed, ":") && len(trimmed) < 40 {
			continue
		}
		bullets = append(bullets, trimmed)
		if len(bullets) >= maxBullets {
			break
		}
	}
	return bullets
}

// truncateLine truncates s to max display columns, appending an ellipsis when
// truncated. max <= 0 returns s unchanged.
func truncateLine(s string, max int) string {
	if max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// IsCompactResultContent reports whether s looks like the textual output of
// a compaction operation. Used by the renderer to auto-detect compact tool
// results when the tool name itself is not available.
func IsCompactResultContent(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "COMPACT:") {
		return true
	}
	hasMsg := strings.Contains(s, "Mensagens:")
	hasTok := strings.Contains(s, "Tokens:")
	return hasMsg && hasTok
}

// ParseCompactResult attempts to parse a textual representation of a compact
// operation into a CompactResult struct. The expected formats are:
//
//   - Lines beginning with "Mensagens: <before> -> <after>"
//   - Lines beginning with "Tokens: <before> -> <after>"
//   - "RESUMO:" or "Summary:" followed by bullet lines
//   - "TOPICOS:" or "Topics:" followed by bullet lines
//
// Unrecognised content is folded into Summary so the renderer can still
// produce a usable card.
func ParseCompactResult(s string) CompactResult {
	r := CompactResult{}
	var summaryLines, topicLines []string
	section := ""

	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		switch {
		case strings.HasPrefix(lower, "mensagens:") || strings.HasPrefix(lower, "messages:"):
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				before, after := parseDeltaInts(parts[1])
				r.MessagesBefore, r.MessagesAfter = before, after
				if before > after {
					r.DroppedCount = before - after
				}
			}
			section = ""
		case strings.HasPrefix(lower, "tokens:"):
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				before, after := parseDeltaInts(parts[1])
				r.TokensBefore, r.TokensAfter = before, after
			}
			section = ""
		case strings.HasPrefix(lower, "resumo:") || strings.HasPrefix(lower, "summary:"):
			section = "summary"
		case strings.HasPrefix(lower, "topicos:") || strings.HasPrefix(lower, "topics:") ||
			strings.HasPrefix(lower, "topicos mantidos:"):
			section = "topics"
		default:
			switch section {
			case "summary":
				summaryLines = append(summaryLines, line)
			case "topics":
				topicLines = append(topicLines, strings.TrimLeft(line, "-*• "))
			default:
				// Unknown lines drift into the summary so they're not lost.
				summaryLines = append(summaryLines, line)
			}
		}
	}

	r.Summary = strings.Join(summaryLines, "\n")
	r.RetainedTopics = topicLines
	return r
}

// parseDeltaInts extracts two integers from a string like " 45 -> 12  (-33)"
// or " 85.4k → 18.2k". Returns (0, 0) if parsing fails. Supports both ASCII
// "->" and Unicode "→" arrows and tolerates "k"/"M" suffixes.
func parseDeltaInts(s string) (int, int) {
	s = strings.ReplaceAll(s, "→", "->")
	parts := strings.SplitN(s, "->", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	before := parseLooseInt(parts[0])
	// The after part may include trailing "(-33)" decoration; cut at "(".
	afterRaw := parts[1]
	if i := strings.Index(afterRaw, "("); i >= 0 {
		afterRaw = afterRaw[:i]
	}
	after := parseLooseInt(afterRaw)
	return before, after
}

// parseLooseInt extracts an integer from a string that may contain
// surrounding whitespace, decimal points, and a "k"/"M" suffix.
func parseLooseInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k") || strings.HasSuffix(s, "K"):
		mult = 1000
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "M"):
		mult = 1_000_000
		s = s[:len(s)-1]
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return int(f * mult)
}
