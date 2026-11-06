package cli

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// ── Paste detection ─────────────────────────────────────────────────────────

// PasteType describes how a large paste should be treated by the CLI.
type PasteType int

const (
	PasteText PasteType = iota
	PasteCode
	PasteURL
	PasteFilePath
	PasteJSON
	PasteImage
)

// String gives a human-readable label for a PasteType (useful for status).
func (p PasteType) String() string {
	switch p {
	case PasteCode:
		return "code"
	case PasteURL:
		return "url"
	case PasteFilePath:
		return "path"
	case PasteJSON:
		return "json"
	case PasteImage:
		return "image"
	default:
		return "text"
	}
}

// pasteThreshold is the number of characters above which a keystroke burst is
// treated as a "paste" rather than normal typing.
const pasteThreshold = 100

// dataURLRegex matches a base64 data URL for an image.
var dataURLRegex = regexp.MustCompile(`^data:image/([a-zA-Z0-9+.-]+);base64,(.+)$`)

// urlRegex is a permissive http(s) URL matcher.
var urlRegex = regexp.MustCompile(`^https?://\S+$`)

// codeKeywordRegex matches common programming keywords/syntax hints.
var codeKeywordRegex = regexp.MustCompile(
	`\b(func|func |def |class |import |package |return|if |else|for |while |const |let |var |public |private |fn |use )\b`,
)

// DetectPasteType analyzes a large paste and returns the best format.
// It is intentionally conservative: when in doubt it returns PasteText so
// the input is inserted literally.
func DetectPasteType(content string) PasteType {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return PasteText
	}

	// 1. Image data URL — highest priority, very distinctive.
	if dataURLRegex.MatchString(trimmed) {
		return PasteImage
	}

	// 2. Plain URL — only if the whole thing is a single URL.
	if !strings.ContainsAny(trimmed, " \n\t") && urlRegex.MatchString(trimmed) {
		return PasteURL
	}

	// 3. File path — single line, starts with /, ~ or ./ and contains a
	//    separator. Avoid matching URLs (handled above) and JSON.
	if !strings.ContainsRune(trimmed, '\n') && looksLikeFilePath(trimmed) {
		return PasteFilePath
	}

	// 4. JSON — starts and ends with matching bracket/brace and parses.
	if looksLikeJSON(trimmed) {
		return PasteJSON
	}

	// 5. Code — multi-line with indentation or keywords/brackets.
	if looksLikeCode(content) {
		return PasteCode
	}

	return PasteText
}

func looksLikeFilePath(s string) bool {
	if len(s) < 2 {
		return false
	}
	if !(strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") ||
		strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")) {
		return false
	}
	// Must contain a path separator beyond the prefix.
	if !strings.ContainsRune(s[1:], '/') && !strings.HasPrefix(s, "~/") {
		// Allow bare "/etc" style as well — still has a slash.
		if !strings.ContainsRune(s, '/') {
			return false
		}
	}
	// Reject anything that looks like JSON or code by containing obvious
	// syntax that would never appear in a normal path.
	if strings.ContainsAny(s, "{}()<>\"'`") {
		return false
	}
	return true
}

func looksLikeJSON(s string) bool {
	if len(s) < 2 {
		return false
	}
	first := s[0]
	last := s[len(s)-1]
	if !((first == '{' && last == '}') || (first == '[' && last == ']')) {
		return false
	}
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}

func looksLikeCode(content string) bool {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		// Single-line: only consider it code if it has obvious syntax.
		return strings.ContainsAny(content, "{};") && codeKeywordRegex.MatchString(content)
	}

	indented := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "\t") {
			indented++
		}
	}

	if indented >= 1 {
		return true
	}
	// Lots of brackets/braces on multiple lines is also a strong signal.
	if strings.Count(content, "{") >= 2 || strings.Count(content, "}") >= 2 {
		return true
	}
	if codeKeywordRegex.MatchString(content) {
		return true
	}
	return false
}

// FormatPaste wraps the paste in appropriate formatting for display and
// forwarding to the AI. For PasteImage the caller should use
// SaveImageFromDataURL instead — FormatPaste will pass the raw content
// through unchanged in that case.
func FormatPaste(content string, pasteType PasteType) string {
	switch pasteType {
	case PasteCode:
		// Guess the language from the first line (shebang/keyword).
		lang := guessLanguage(content)
		return fmt.Sprintf("```%s\n%s\n```", lang, strings.TrimRight(content, "\n"))
	case PasteJSON:
		// Pretty-print if compact, otherwise keep as-is.
		pretty := content
		var v any
		if err := json.Unmarshal([]byte(content), &v); err == nil {
			if out, err := json.MarshalIndent(v, "", "  "); err == nil {
				pretty = string(out)
			}
		}
		return fmt.Sprintf("```json\n%s\n```", pretty)
	case PasteFilePath:
		// Suggest @file mention syntax so the AI can open it.
		return "@" + strings.TrimSpace(content)
	case PasteURL, PasteText:
		return content
	case PasteImage:
		return content
	default:
		return content
	}
}

func guessLanguage(content string) string {
	head := content
	if idx := strings.Index(head, "\n"); idx > 0 && idx < 200 {
		head = head[:idx]
	}
	switch {
	case strings.Contains(head, "#!/bin/bash"), strings.Contains(head, "#!/bin/sh"):
		return "bash"
	case strings.Contains(head, "#!/usr/bin/env python"), strings.Contains(content, "def ") && strings.Contains(content, ":"):
		return "python"
	case strings.Contains(content, "package ") && strings.Contains(content, "func "):
		return "go"
	case strings.Contains(content, "function ") || strings.Contains(content, "const ") || strings.Contains(content, "=> {"):
		return "javascript"
	case strings.Contains(content, "fn ") && strings.Contains(content, "->"):
		return "rust"
	case strings.Contains(content, "public class "):
		return "java"
	}
	return ""
}

// ── Code block / submit detection ───────────────────────────────────────────

// IsInUnfinishedCodeBlock returns true if the input has an opening ``` fence
// without a matching closing fence.
func IsInUnfinishedCodeBlock(input string) bool {
	// Count ``` occurrences. If odd, we're inside a fence.
	count := strings.Count(input, "```")
	return count%2 == 1
}

// ShouldSubmitOnEnter returns true if Enter should submit the current input
// versus adding a newline. It considers:
//   - empty input → false (no-op, do not submit)
//   - unfinished ``` code block → false
//   - trailing backslash continuation → false
//   - unclosed brackets ({, [, () → false
func ShouldSubmitOnEnter(input string) bool {
	trimmed := strings.TrimRight(input, " \t")
	if strings.TrimSpace(trimmed) == "" {
		return false
	}
	if IsInUnfinishedCodeBlock(trimmed) {
		return false
	}
	if strings.HasSuffix(trimmed, "\\") {
		return false
	}
	if hasUnclosedBrackets(trimmed) {
		return false
	}
	return true
}

// hasUnclosedBrackets walks the input and returns true if any of (), [], {}
// remain unclosed, ignoring characters inside single/double/backtick quoted
// strings.
func hasUnclosedBrackets(input string) bool {
	var stack []rune
	var quote rune
	escaped := false
	for _, r := range input {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case '(', '[', '{':
			stack = append(stack, r)
		case ')', ']', '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return len(stack) > 0
}

// CountLines returns the number of lines in the input (minimum 1).
func CountLines(input string) int {
	if input == "" {
		return 1
	}
	return strings.Count(input, "\n") + 1
}

// ── Bracket matching ────────────────────────────────────────────────────────

// MatchBracket returns the closing char for an opening bracket/quote.
// Returns 0 if no auto-close should happen.
func MatchBracket(r rune) rune {
	switch r {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	case '"':
		return '"'
	case '\'':
		return '\''
	}
	return 0
}

// ── Image paste ─────────────────────────────────────────────────────────────

// SaveImageFromDataURL extracts base64 image data from a data URL and writes
// it to a file under <dataDir>/pasted-images/. Returns the absolute file path.
//
// The filename is content-addressed (first 8 hex chars of the SHA1 of the
// base64 payload) so repeated pastes of the same image collapse to one file.
func SaveImageFromDataURL(dataURL, dataDir string) (string, error) {
	m := dataURLRegex.FindStringSubmatch(strings.TrimSpace(dataURL))
	if m == nil {
		return "", fmt.Errorf("not a data:image/... URL")
	}
	ext := strings.ToLower(m[1])
	payload := m[2]

	// Normalise some extensions for common mime types.
	switch ext {
	case "jpeg":
		ext = "jpg"
	case "svg+xml":
		ext = "svg"
	}

	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	dir := filepath.Join(dataDir, "pasted-images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	sum := sha1.Sum(raw)
	name := fmt.Sprintf("pasted-%s-%s.%s",
		time.Now().Format("20060102-150405"),
		hex.EncodeToString(sum[:4]),
		ext,
	)
	full := filepath.Join(dir, name)

	if err := os.WriteFile(full, raw, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", full, err)
	}
	return full, nil
}

// ── Line editing helpers ────────────────────────────────────────────────────

// DeleteWordBackward removes the word immediately before the cursor (readline
// ^W semantics: trim trailing whitespace, then trim one run of non-whitespace).
// Returns the new input and the new cursor position, both in rune units.
func DeleteWordBackward(input string, pos int) (string, int) {
	runes := []rune(input)
	if pos <= 0 || pos > len(runes) {
		if pos > len(runes) {
			pos = len(runes)
		}
		return input, pos
	}

	end := pos
	i := pos

	// Skip trailing whitespace.
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	// Then skip the word characters.
	for i > 0 && !unicode.IsSpace(runes[i-1]) {
		i--
	}

	if i == end {
		return input, pos
	}
	out := append([]rune{}, runes[:i]...)
	out = append(out, runes[end:]...)
	return string(out), i
}

// DeleteToEndOfLine removes everything from the cursor to the end of the
// current visual line (readline ^K). If the cursor is already at the end of
// the line the line break itself is removed (joining with the next line).
func DeleteToEndOfLine(input string, pos int) (string, int) {
	runes := []rune(input)
	if pos < 0 {
		pos = 0
	}
	if pos >= len(runes) {
		return input, pos
	}
	end := pos
	for end < len(runes) && runes[end] != '\n' {
		end++
	}
	if end == pos && end < len(runes) && runes[end] == '\n' {
		end++ // at end-of-line: also swallow the newline
	}
	out := append([]rune{}, runes[:pos]...)
	out = append(out, runes[end:]...)
	return string(out), pos
}
