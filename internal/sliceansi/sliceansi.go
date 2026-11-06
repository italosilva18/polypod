// Package sliceansi provides ANSI-escape-aware string slicing and width calculation.
package sliceansi

import (
	"strings"
	"unicode/utf8"
)

// Slice returns a substring of s based on visible width (excluding ANSI escapes
// and accounting for double-width characters), preserving any ANSI escapes.
// start and end are visible-character offsets (zero-indexed, end-exclusive).
func Slice(s string, start, end int) string {
	if s == "" || start >= end {
		return ""
	}

	var result strings.Builder
	visiblePos := 0

	i := 0
	for i < len(s) {
		// Detect ANSI escape sequence
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// CSI escape: \x1b[...m or other final byte
			end := findCSIEnd(s, i)
			if end > i {
				// Always include escape sequences
				result.WriteString(s[i:end])
				i = end
				continue
			}
		}

		// Detect OSC escape (\x1b]...\x07 or \x1b]...\x1b\\)
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == ']' {
			end := findOSCEnd(s, i)
			if end > i {
				result.WriteString(s[i:end])
				i = end
				continue
			}
		}

		// Decode rune
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}

		rWidth := runeWidth(r)

		// Skip if before start
		if visiblePos+rWidth <= start {
			visiblePos += rWidth
			i += size
			continue
		}

		// Stop if past end
		if visiblePos >= end {
			break
		}

		// Include this rune
		result.WriteString(s[i : i+size])
		visiblePos += rWidth
		i += size
	}

	// Append reset code if we cut in the middle (best-effort)
	return result.String()
}

// VisibleWidth returns the visible character width of s,
// ignoring ANSI escape sequences and accounting for double-width characters.
func VisibleWidth(s string) int {
	width := 0
	i := 0
	for i < len(s) {
		// Skip CSI escape
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			end := findCSIEnd(s, i)
			if end > i {
				i = end
				continue
			}
		}
		// Skip OSC escape
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == ']' {
			end := findOSCEnd(s, i)
			if end > i {
				i = end
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		width += runeWidth(r)
		i += size
	}
	return width
}

// StripANSI returns s with all ANSI escape sequences removed.
func StripANSI(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			end := findCSIEnd(s, i)
			if end > i {
				i = end
				continue
			}
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == ']' {
			end := findOSCEnd(s, i)
			if end > i {
				i = end
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// findCSIEnd returns the index AFTER the end of a CSI escape starting at i.
// CSI: ESC [ params... final-byte (0x40-0x7E)
func findCSIEnd(s string, start int) int {
	// start points to ESC, we need to find the final byte
	i := start + 2 // skip ESC [
	for i < len(s) {
		c := s[i]
		if c >= 0x40 && c <= 0x7E {
			return i + 1
		}
		i++
	}
	return start
}

// findOSCEnd returns the index AFTER the end of an OSC escape starting at i.
// OSC: ESC ] ... ST    (ST = BEL or ESC \)
func findOSCEnd(s string, start int) int {
	i := start + 2 // skip ESC ]
	for i < len(s) {
		// BEL terminator
		if s[i] == 0x07 {
			return i + 1
		}
		// ST terminator (ESC \)
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return start
}

// runeWidth returns the display width of a rune.
// Returns 0 for combining characters, 2 for double-width (CJK), 1 otherwise.
func runeWidth(r rune) int {
	// Control chars
	if r < 0x20 || r == 0x7f {
		return 0
	}
	// ASCII
	if r < 0x7f {
		return 1
	}
	// Combining marks
	if isCombining(r) {
		return 0
	}
	// Wide chars (CJK, emoji, etc.)
	if isWide(r) {
		return 2
	}
	return 1
}

// isCombining returns true for combining/zero-width characters.
func isCombining(r rune) bool {
	// Simplified ranges of combining marks
	switch {
	case r >= 0x0300 && r <= 0x036F: // Combining Diacritical Marks
		return true
	case r >= 0x1AB0 && r <= 0x1AFF: // Combining Diacritical Marks Extended
		return true
	case r >= 0x1DC0 && r <= 0x1DFF: // Combining Diacritical Marks Supplement
		return true
	case r >= 0x20D0 && r <= 0x20FF: // Combining Diacritical Marks for Symbols
		return true
	case r >= 0xFE20 && r <= 0xFE2F: // Combining Half Marks
		return true
	case r == 0x200B: // ZERO WIDTH SPACE
		return true
	case r == 0x200C: // ZERO WIDTH NON-JOINER
		return true
	case r == 0x200D: // ZERO WIDTH JOINER
		return true
	case r == 0xFEFF: // ZERO WIDTH NO-BREAK SPACE
		return true
	}
	return false
}

// isWide returns true for double-width characters (CJK, emoji).
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0x303E: // CJK Radicals Supplement, Kangxi Radicals
		return true
	case r >= 0x3041 && r <= 0x33FF: // Hiragana, Katakana, CJK Symbols
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0xA000 && r <= 0xA4CF: // Yi Syllables
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul Syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	case r >= 0xFE30 && r <= 0xFE4F: // CJK Compatibility Forms
		return true
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth Forms (most)
		return true
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth Signs
		return true
	case r >= 0x1F300 && r <= 0x1F9FF: // Misc Symbols and Pictographs, Emoji
		return true
	case r >= 0x20000 && r <= 0x2FFFF: // CJK Extensions B-F
		return true
	case r >= 0x30000 && r <= 0x3FFFF: // CJK Extension G
		return true
	}
	return false
}

// PadRight pads s on the right with spaces to reach the given visible width.
func PadRight(s string, width int) string {
	current := VisibleWidth(s)
	if current >= width {
		return s
	}
	return s + strings.Repeat(" ", width-current)
}

// PadLeft pads s on the left with spaces to reach the given visible width.
func PadLeft(s string, width int) string {
	current := VisibleWidth(s)
	if current >= width {
		return s
	}
	return strings.Repeat(" ", width-current) + s
}

// Truncate truncates s to the given visible width, optionally appending an ellipsis.
func Truncate(s string, width int, ellipsis string) string {
	current := VisibleWidth(s)
	if current <= width {
		return s
	}

	ellipsisWidth := VisibleWidth(ellipsis)
	if ellipsisWidth >= width {
		return Slice(s, 0, width)
	}

	return Slice(s, 0, width-ellipsisWidth) + ellipsis
}

// Wrap wraps s to lines no wider than maxWidth (visible width).
// Splits on existing newlines and word boundaries.
func Wrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}

	var result []string
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if VisibleWidth(line) <= maxWidth {
			result = append(result, line)
			continue
		}
		// Word-wrap
		words := strings.Fields(line)
		var current string
		for _, w := range words {
			if current == "" {
				current = w
				continue
			}
			if VisibleWidth(current)+1+VisibleWidth(w) > maxWidth {
				result = append(result, current)
				current = w
			} else {
				current = current + " " + w
			}
		}
		if current != "" {
			result = append(result, current)
		}
	}
	return result
}
