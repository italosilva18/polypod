// Package utils — format utilities extracted from Claude Code
// Source: src/utils/format.ts (~309 linhas TypeScript)

package utils

import (
	"fmt"
	"math"
	"time"
)

// FormatFileSize formats bytes to human-readable string (KB, MB, GB)
// Example: 1536 → "1.5KB"
func FormatFileSize(sizeInBytes int64) string {
	kb := float64(sizeInBytes) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", sizeInBytes)
	}
	if kb < 1024 {
		return fmt.Sprintf("%.1fKB", kb)
	}
	mb := kb / 1024
	if mb < 1024 {
		return fmt.Sprintf("%.1fMB", mb)
	}
	gb := mb / 1024
	return fmt.Sprintf("%.1fGB", gb)
}

// FormatSecondsShort formats milliseconds as seconds with 1 decimal
// Example: 1234 → "1.2s"
func FormatSecondsShort(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// FormatDuration formats milliseconds to human-readable duration
func FormatDuration(ms int64) string {
	if ms < 60000 {
		if ms == 0 {
			return "0s"
		}
		if ms < 1000 {
			return fmt.Sprintf("%.1fs", float64(ms)/1000)
		}
		return fmt.Sprintf("%ds", ms/1000)
	}

	days := ms / 86400000
	hours := (ms % 86400000) / 3600000
	minutes := (ms % 3600000) / 60000
	seconds := (ms % 60000) / 1000

	// Handle rounding carry-over
	if seconds == 60 {
		seconds = 0
		minutes++
	}
	if minutes == 60 {
		minutes = 0
		hours++
	}
	if hours == 24 {
		hours = 0
		days++
	}

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// FormatNumber formats number with compact notation (1.3K, 1.5M, etc.)
func FormatNumber(number int64) string {
	if number < 1000 {
		return fmt.Sprintf("%d", number)
	}
	if number < 1000000 {
		return fmt.Sprintf("%.1fk", float64(number)/1000)
	}
	if number < 1000000000 {
		return fmt.Sprintf("%.1fm", float64(number)/1000000)
	}
	return fmt.Sprintf("%.1fb", float64(number)/1000000000)
}

// FormatTokens formats token count with compact notation
func FormatTokens(count int64) string {
	return FormatNumber(count)
}

// FormatRelativeTime formats date as relative time ("2h ago", "in 3d", etc.)
func FormatRelativeTime(date time.Time, now time.Time) string {
	diff := date.Sub(now)
	diffSeconds := int64(diff.Seconds())

	intervals := []struct {
		unit   string
		seconds int64
		short   string
	}{
		{"year", 31536000, "y"},
		{"month", 2592000, "mo"},
		{"week", 604800, "w"},
		{"day", 86400, "d"},
		{"hour", 3600, "h"},
		{"minute", 60, "m"},
		{"second", 1, "s"},
	}

	absDiff := int64(math.Abs(float64(diffSeconds)))

	for _, interval := range intervals {
		if absDiff >= interval.seconds {
			value := absDiff / interval.seconds
			if diffSeconds < 0 {
				return fmt.Sprintf("%d%s ago", value, interval.short)
			}
			return fmt.Sprintf("in %d%s", value, interval.short)
		}
	}

	// Less than 1 second
	if diffSeconds <= 0 {
		return "0s ago"
	}
	return "in 0s"
}

// FormatRelativeTimeAgo formats date with "ago" suffix
func FormatRelativeTimeAgo(date time.Time, now time.Time) string {
	if date.After(now) {
		return FormatRelativeTime(date, now)
	}
	return FormatRelativeTime(date, now)
}

// TruncateToWidth truncates string to max width
func TruncateToWidth(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 3 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

// TruncatePathMiddle truncates path in the middle with "..."
func TruncatePathMiddle(path string, width int) string {
	if len(path) <= width {
		return path
	}
	if width <= 6 {
		return path[:width]
	}
	halfWidth := (width - 3) / 2
	return path[:halfWidth] + "..." + path[len(path)-halfWidth:]
}

// HighlightMatch highlights matched text (placeholder for TUI)
func HighlightMatch(text, query string) string {
	// Simple implementation - TUI will handle actual highlighting
	return text
}
