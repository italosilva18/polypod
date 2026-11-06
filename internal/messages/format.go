package messages

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Truncate truncates text to maxLen runes, appending "…" if truncated.
// If singleLine is true, also truncates at the first newline.
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return "…"
	}

	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}

	runes := []rune(s)
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

// TruncateSingleLine truncates at the first newline, then at maxLen.
func TruncateSingleLine(s string, maxLen int) string {
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		s = s[:idx]
		if utf8.RuneCountInString(s)+1 > maxLen {
			return Truncate(s, maxLen)
		}
		return s + "…"
	}
	return Truncate(s, maxLen)
}

// TruncateMiddle keeps the start and end of a string, inserting "…" in the middle.
func TruncateMiddle(s string, maxLen int) string {
	if maxLen <= 0 {
		return "…"
	}

	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}

	if maxLen <= 3 {
		return Truncate(s, maxLen)
	}

	runes := []rune(s)
	// Give more space to the end (filename is more informative).
	headLen := (maxLen - 1) / 3
	tailLen := maxLen - 1 - headLen

	return string(runes[:headLen]) + "…" + string(runes[runeCount-tailLen:])
}

// TruncatePathMiddle truncates a file path in the middle to preserve both
// directory context and filename.
func TruncatePathMiddle(path string, maxLen int) string {
	if utf8.RuneCountInString(path) <= maxLen {
		return path
	}
	if maxLen <= 0 {
		return "…"
	}
	if maxLen < 5 {
		return Truncate(path, maxLen)
	}

	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return Truncate(path, maxLen)
	}

	filename := path[lastSlash:] // includes leading "/"
	directory := path[:lastSlash]
	filenameLen := utf8.RuneCountInString(filename)

	if filenameLen >= maxLen-1 {
		return TruncateStart(path, maxLen)
	}

	availableForDir := maxLen - 1 - filenameLen // -1 for ellipsis
	if availableForDir <= 0 {
		return TruncateStart(filename, maxLen)
	}

	dirRunes := []rune(directory)
	if len(dirRunes) > availableForDir {
		dirRunes = dirRunes[:availableForDir]
	}

	return string(dirRunes) + "…" + filename
}

// TruncateStart truncates from the start of a string, keeping the tail.
func TruncateStart(s string, maxLen int) string {
	runeCount := utf8.RuneCountInString(s)
	if runeCount <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}

	runes := []rune(s)
	return "…" + string(runes[runeCount-maxLen+1:])
}

// FormatJSON pretty-prints a JSON string. Returns the original on parse error.
func FormatJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	formatted, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(formatted)
}

// FormatXML pretty-prints an XML string. Returns the original on parse error.
func FormatXML(s string) string {
	decoder := xml.NewDecoder(strings.NewReader(s))
	var tokens []xml.Token
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		tokens = append(tokens, xml.CopyToken(tok))
	}

	if len(tokens) == 0 {
		return s
	}

	var buf strings.Builder
	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")
	for _, tok := range tokens {
		if err := encoder.EncodeToken(tok); err != nil {
			return s
		}
	}
	if err := encoder.Flush(); err != nil {
		return s
	}
	return buf.String()
}

// ansiPattern matches ANSI escape sequences.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x1b\\|\x1b\][^\x07]*\x07`)

// StripANSI removes ANSI escape codes from a string.
func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// WordWrap wraps text at word boundaries to fit within the given width.
func WordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for lineIdx, line := range lines {
		if lineIdx > 0 {
			result.WriteByte('\n')
		}

		if utf8.RuneCountInString(line) <= width {
			result.WriteString(line)
			continue
		}

		words := strings.Fields(line)
		currentLineLen := 0

		for i, word := range words {
			wordLen := utf8.RuneCountInString(word)

			if currentLineLen == 0 {
				// First word on a new line.
				if wordLen > width {
					// Word itself is longer than width, force it.
					result.WriteString(word)
					currentLineLen = wordLen
				} else {
					result.WriteString(word)
					currentLineLen = wordLen
				}
			} else if currentLineLen+1+wordLen <= width {
				result.WriteByte(' ')
				result.WriteString(word)
				currentLineLen += 1 + wordLen
			} else {
				result.WriteByte('\n')
				result.WriteString(word)
				currentLineLen = wordLen
			}
			_ = i
		}
	}

	return result.String()
}

// EscapeXML escapes XML special characters for safe interpolation into text content.
func EscapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// EscapeXMLAttr escapes for interpolation into a quoted attribute value.
func EscapeXMLAttr(s string) string {
	s = EscapeXML(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// FormatDuration formats milliseconds into a human-readable duration string.
func FormatDuration(ms int64) string {
	if ms < 60000 {
		if ms == 0 {
			return "0s"
		}
		s := ms / 1000
		return fmt.Sprintf("%ds", s)
	}

	days := ms / 86400000
	hours := (ms % 86400000) / 3600000
	minutes := (ms % 3600000) / 60000
	seconds := (ms%60000 + 500) / 1000

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

// FormatNumber formats a number in compact notation (e.g., 1.3k).
func FormatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1fk", float64(n)/1000), ".0k") + ""
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1fm", float64(n)/1000000), ".0m") + ""
}

// FormatTokens formats a token count in compact notation.
func FormatTokens(count int) string {
	return FormatNumber(count)
}
