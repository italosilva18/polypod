package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// WarningLevel classifies a token-usage banner into one of five severity
// buckets. Levels are derived from the percentage of the model's context
// window that is currently consumed.
type WarningLevel int

const (
	// WarningNone means usage is below 50% — no banner is shown.
	WarningNone WarningLevel = iota
	// WarningInfo: 50-70% — informational, blue.
	WarningInfo
	// WarningCaution: 70-85% — yellow, suggest /compact soon.
	WarningCaution
	// WarningHigh: 85-95% — orange, urgent /compact recommendation.
	WarningHigh
	// WarningCritical: 95%+ — red, blinking, /compact NOW.
	WarningCritical
)

// TokenWarning is a small immutable snapshot of token usage and the
// matching warning level. It is created by ComputeTokenWarning and
// rendered with FormatWarningBanner.
type TokenWarning struct {
	Used    int
	Max     int
	Percent float64
	Level   WarningLevel
}

// ComputeTokenWarning derives a TokenWarning from the raw used/max
// token counts. When max is zero or used is negative the level is
// WarningNone (no banner).
func ComputeTokenWarning(used, max int) TokenWarning {
	w := TokenWarning{Used: used, Max: max}
	if max <= 0 || used < 0 {
		w.Level = WarningNone
		return w
	}
	pct := float64(used) / float64(max)
	w.Percent = pct
	switch {
	case pct >= 0.95:
		w.Level = WarningCritical
	case pct >= 0.85:
		w.Level = WarningHigh
	case pct >= 0.70:
		w.Level = WarningCaution
	case pct >= 0.50:
		w.Level = WarningInfo
	default:
		w.Level = WarningNone
	}
	return w
}

// warningColor returns the foreground color associated with a warning
// level.
func warningColor(level WarningLevel) lipgloss.Color {
	switch level {
	case WarningInfo:
		return lipgloss.Color("#57C7FF") // blue
	case WarningCaution:
		return lipgloss.Color("#F3F99D") // yellow
	case WarningHigh:
		return lipgloss.Color("#FF8800") // orange
	case WarningCritical:
		return lipgloss.Color("#FF0000") // red
	}
	return lipgloss.Color("#FFFFFF")
}

// warningBgColor returns the background color associated with a warning
// level. Backgrounds are darker variants of the foreground hue so the
// banner is legible while clearly distinct from the chat.
func warningBgColor(level WarningLevel) lipgloss.Color {
	switch level {
	case WarningInfo:
		return lipgloss.Color("#0F2A4E")
	case WarningCaution:
		return lipgloss.Color("#3E3A0E")
	case WarningHigh:
		return lipgloss.Color("#4E2400")
	case WarningCritical:
		return lipgloss.Color("#4E0000")
	}
	return lipgloss.Color("#1A1A2E")
}

// warningIcon returns the leading icon glyph for a warning level.
func warningIcon(level WarningLevel) string {
	switch level {
	case WarningInfo:
		return "ℹ"
	case WarningCaution:
		return "⚠"
	case WarningHigh:
		return "🔶"
	case WarningCritical:
		return "🚨"
	}
	return ""
}

// warningMessage returns the human-readable suffix shown after the
// "Context: X/Y (Z%)" prefix, varying by severity.
func warningMessage(level WarningLevel) string {
	switch level {
	case WarningInfo:
		return "Approaching limits"
	case WarningCaution:
		return "Consider /compact soon"
	case WarningHigh:
		return "/compact recomendado urgentemente"
	case WarningCritical:
		return "LIMIT IMMINENT - /compact NOW"
	}
	return ""
}

// FormatWarningBanner renders a single full-width banner line for the
// given warning. Returns "" when no banner should be displayed
// (WarningNone). The banner is padded to width with the warning's
// background color so it visually spans the whole row.
func FormatWarningBanner(w TokenWarning, width int) string {
	if w.Level == WarningNone {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	icon := warningIcon(w.Level)
	pct := int(w.Percent * 100)
	usedStr := formatTokenShort(w.Used)
	maxStr := formatTokenShort(w.Max)
	msg := warningMessage(w.Level)

	text := fmt.Sprintf(" %s Context: %s/%s (%d%%) - %s ",
		icon, usedStr, maxStr, pct, msg)

	// Truncate if narrower than the message itself.
	textLen := visibleRuneLen(text)
	if textLen > width {
		runes := []rune(text)
		if width-1 > 0 && width-1 < len(runes) {
			text = string(runes[:width-1])
			textLen = visibleRuneLen(text)
		}
	}

	// Pad with spaces so the background extends across the full width.
	if textLen < width {
		text += strings.Repeat(" ", width-textLen)
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(warningBgColor(w.Level)).
		Bold(true)

	// Use the level's foreground color for the icon + text on
	// critical/high so it pops; on lower severities the white-on-dark
	// reads better.
	if w.Level == WarningCritical {
		style = style.Foreground(warningColor(w.Level)).Blink(true)
	} else if w.Level == WarningHigh {
		style = style.Foreground(warningColor(w.Level))
	}

	return style.Render(text)
}
