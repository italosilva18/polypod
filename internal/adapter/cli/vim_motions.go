package cli

import "unicode"

// ── Motion interface ────────────────────────────────────────────────────────

// Motion describes a cursor movement within a rune slice.
type Motion interface {
	// Apply returns the new cursor position after executing the motion.
	Apply(content []rune, cursor int) (newCursor int)
}

// ── Concrete motions ────────────────────────────────────────────────────────

// WordForward moves the cursor to the start of the next word.
type WordForward struct{}

func (WordForward) Apply(content []rune, cursor int) int {
	n := len(content)
	if cursor >= n {
		return cursor
	}
	i := cursor
	// Skip current word characters.
	if isWord(content[i]) {
		for i < n && isWord(content[i]) {
			i++
		}
	} else if !unicode.IsSpace(content[i]) {
		// Non-word, non-space (punctuation block).
		for i < n && !isWord(content[i]) && !unicode.IsSpace(content[i]) {
			i++
		}
	}
	// Skip whitespace.
	for i < n && unicode.IsSpace(content[i]) {
		i++
	}
	if i > n {
		i = n
	}
	return i
}

// WordBackward moves the cursor to the start of the previous word.
type WordBackward struct{}

func (WordBackward) Apply(content []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	i := cursor
	// Skip whitespace backwards.
	for i > 0 && unicode.IsSpace(content[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	// Determine the class of the character just before the cursor.
	if isWord(content[i-1]) {
		for i > 0 && isWord(content[i-1]) {
			i--
		}
	} else {
		for i > 0 && !isWord(content[i-1]) && !unicode.IsSpace(content[i-1]) {
			i--
		}
	}
	return i
}

// WordEnd moves the cursor to the end of the current/next word (the 'e' motion).
type WordEnd struct{}

func (WordEnd) Apply(content []rune, cursor int) int {
	n := len(content)
	if cursor >= n-1 {
		return max(n-1, 0)
	}
	i := cursor + 1
	// Skip whitespace.
	for i < n && unicode.IsSpace(content[i]) {
		i++
	}
	if i >= n {
		return n - 1
	}
	// Move to end of word or punctuation block.
	if isWord(content[i]) {
		for i+1 < n && isWord(content[i+1]) {
			i++
		}
	} else {
		for i+1 < n && !isWord(content[i+1]) && !unicode.IsSpace(content[i+1]) {
			i++
		}
	}
	return i
}

// LineStart moves the cursor to the beginning of the current line (0).
type LineStart struct{}

func (LineStart) Apply(content []rune, cursor int) int {
	i := cursor
	for i > 0 && content[i-1] != '\n' {
		i--
	}
	return i
}

// LineEnd moves the cursor to the last character of the current line ($).
type LineEnd struct{}

func (LineEnd) Apply(content []rune, cursor int) int {
	n := len(content)
	if n == 0 {
		return 0
	}
	i := cursor
	for i < n && content[i] != '\n' {
		i++
	}
	// Vim $ lands on the last char, not past it (unless the line is empty).
	if i > cursor {
		return i - 1
	}
	return cursor
}

// FindChar implements the f{char} motion — move to the next occurrence of ch
// on the current line.
type FindChar struct{ Ch rune }

func (f FindChar) Apply(content []rune, cursor int) int {
	n := len(content)
	for i := cursor + 1; i < n; i++ {
		if content[i] == '\n' {
			break
		}
		if content[i] == f.Ch {
			return i
		}
	}
	return cursor // not found — stay put
}

// TillChar implements the t{char} motion — move to one position before the
// next occurrence of ch on the current line.
type TillChar struct{ Ch rune }

func (t TillChar) Apply(content []rune, cursor int) int {
	n := len(content)
	for i := cursor + 1; i < n; i++ {
		if content[i] == '\n' {
			break
		}
		if content[i] == t.Ch {
			if i-1 > cursor {
				return i - 1
			}
			return cursor
		}
	}
	return cursor
}

// ParagraphForward moves the cursor to the next blank line (}).
type ParagraphForward struct{}

func (ParagraphForward) Apply(content []rune, cursor int) int {
	n := len(content)
	i := cursor
	// Skip current non-blank line.
	for i < n && content[i] != '\n' {
		i++
	}
	// Move past blank lines and then find the next blank line.
	for i < n {
		i++ // skip the newline
		if i >= n {
			return n
		}
		if content[i] == '\n' {
			return i
		}
		// Skip this non-blank line.
		for i < n && content[i] != '\n' {
			i++
		}
	}
	return n
}

// ParagraphBackward moves the cursor to the previous blank line ({).
type ParagraphBackward struct{}

func (ParagraphBackward) Apply(content []rune, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	i := cursor
	// Move to start of current line.
	for i > 0 && content[i-1] != '\n' {
		i--
	}
	if i > 0 {
		i-- // skip newline before current line
	}
	// Scan backwards for a blank line (\n\n sequence or start of buffer).
	for i > 0 {
		// Find start of this line.
		lineStart := i
		for lineStart > 0 && content[lineStart-1] != '\n' {
			lineStart--
		}
		// Check if this line is blank (lineStart == i means the line is just
		// a newline).
		if lineStart == i && content[i] == '\n' {
			return i
		}
		if lineStart == 0 {
			return 0
		}
		i = lineStart - 1
	}
	return 0
}

// ── Operator interface ──────────────────────────────────────────────────────

// Operator modifies the content between two cursor positions.
type Operator interface {
	// Apply returns the new content after operating on content[start:end].
	// The range is inclusive of start and exclusive of end.
	Apply(content []rune, start, end int) []rune
}

// DeleteOp deletes the text in the given range.
type DeleteOp struct{}

func (DeleteOp) Apply(content []rune, start, end int) []rune {
	start, end = clampRange(content, start, end)
	out := make([]rune, 0, len(content)-(end-start))
	out = append(out, content[:start]...)
	out = append(out, content[end:]...)
	return out
}

// ChangeOp deletes the text in the given range (the caller is expected to
// switch the model to insert mode afterwards).
type ChangeOp struct{}

func (ChangeOp) Apply(content []rune, start, end int) []rune {
	// Change behaves like delete — the mode switch to INSERT is handled
	// externally by the Update loop.
	return DeleteOp{}.Apply(content, start, end)
}

// YankOp copies the text in the given range into the provided buffer pointer
// without modifying the content.
type YankOp struct {
	Buffer *string
}

func (y YankOp) Apply(content []rune, start, end int) []rune {
	start, end = clampRange(content, start, end)
	if y.Buffer != nil {
		*y.Buffer = string(content[start:end])
	}
	return content // content unchanged
}

// ── TextObject interface ────────────────────────────────────────────────────

// TextObject describes a range of text around the cursor (e.g. a word, a
// parenthesised expression, a quoted string).
type TextObject interface {
	// Range returns the start (inclusive) and end (exclusive) of the text
	// object around cursor.
	Range(content []rune, cursor int) (start, end int)
}

// InnerWord selects the current word without surrounding whitespace (iw).
type InnerWord struct{}

func (InnerWord) Range(content []rune, cursor int) (int, int) {
	n := len(content)
	if n == 0 || cursor >= n {
		return cursor, cursor
	}
	start, end := cursor, cursor
	if isWord(content[cursor]) {
		for start > 0 && isWord(content[start-1]) {
			start--
		}
		end = cursor
		for end < n && isWord(content[end]) {
			end++
		}
	} else if unicode.IsSpace(content[cursor]) {
		for start > 0 && unicode.IsSpace(content[start-1]) {
			start--
		}
		end = cursor
		for end < n && unicode.IsSpace(content[end]) {
			end++
		}
	} else {
		// Punctuation block.
		for start > 0 && !isWord(content[start-1]) && !unicode.IsSpace(content[start-1]) {
			start--
		}
		end = cursor
		for end < n && !isWord(content[end]) && !unicode.IsSpace(content[end]) {
			end++
		}
	}
	return start, end
}

// AWord selects the current word plus trailing (or leading) whitespace (aw).
type AWord struct{}

func (AWord) Range(content []rune, cursor int) (int, int) {
	start, end := InnerWord{}.Range(content, cursor)
	n := len(content)
	// Extend to include trailing whitespace first; if none, include leading.
	if end < n && unicode.IsSpace(content[end]) {
		for end < n && unicode.IsSpace(content[end]) {
			end++
		}
	} else if start > 0 && unicode.IsSpace(content[start-1]) {
		for start > 0 && unicode.IsSpace(content[start-1]) {
			start--
		}
	}
	return start, end
}

// InnerParen selects the content inside the innermost matching parentheses,
// not including the parens themselves (i( / i)).
type InnerParen struct{}

func (InnerParen) Range(content []rune, cursor int) (int, int) {
	open, close := findMatchingPair(content, cursor, '(', ')')
	if open < 0 {
		return cursor, cursor
	}
	return open + 1, close
}

// AParen selects the content including the matching parentheses (a( / a)).
type AParen struct{}

func (AParen) Range(content []rune, cursor int) (int, int) {
	open, close := findMatchingPair(content, cursor, '(', ')')
	if open < 0 {
		return cursor, cursor
	}
	return open, close + 1
}

// InnerQuote selects the content inside the nearest double-quote pair (i").
type InnerQuote struct{}

func (InnerQuote) Range(content []rune, cursor int) (int, int) {
	open, close := findQuotePair(content, cursor, '"')
	if open < 0 {
		return cursor, cursor
	}
	return open + 1, close
}

// AQuote selects the content including the double quotes (a").
type AQuote struct{}

func (AQuote) Range(content []rune, cursor int) (int, int) {
	open, close := findQuotePair(content, cursor, '"')
	if open < 0 {
		return cursor, cursor
	}
	return open, close + 1
}

// ── Parsers ─────────────────────────────────────────────────────────────────

// ParseMotion maps a key sequence to a Motion. For motions that require a
// following character (f, t) the second rune is expected as key[1:].
// Returns nil if the key does not match any known motion.
func ParseMotion(key string) Motion {
	if len(key) == 0 {
		return nil
	}
	runes := []rune(key)
	switch runes[0] {
	case 'w':
		return WordForward{}
	case 'b':
		return WordBackward{}
	case 'e':
		return WordEnd{}
	case '0':
		return LineStart{}
	case '$':
		return LineEnd{}
	case '{':
		return ParagraphBackward{}
	case '}':
		return ParagraphForward{}
	case 'f':
		if len(runes) >= 2 {
			return FindChar{Ch: runes[1]}
		}
		return nil // incomplete
	case 't':
		if len(runes) >= 2 {
			return TillChar{Ch: runes[1]}
		}
		return nil // incomplete
	}
	return nil
}

// ParseTextObject maps a two-character key sequence to a TextObject.
// Expected patterns: iw, aw, i(, a(, i), a), i", a".
// Returns nil if the keys do not match any known text object.
func ParseTextObject(keys string) TextObject {
	if len(keys) < 2 {
		return nil
	}
	runes := []rune(keys)
	prefix := runes[0] // 'i' or 'a'
	target := runes[1]

	switch {
	case prefix == 'i' && target == 'w':
		return InnerWord{}
	case prefix == 'a' && target == 'w':
		return AWord{}
	case prefix == 'i' && (target == '(' || target == ')'):
		return InnerParen{}
	case prefix == 'a' && (target == '(' || target == ')'):
		return AParen{}
	case prefix == 'i' && target == '"':
		return InnerQuote{}
	case prefix == 'a' && target == '"':
		return AQuote{}
	}
	return nil
}

// ── Operator + Motion combiner ──────────────────────────────────────────────

// ApplyOperatorMotion executes an operator over the range defined by a motion
// starting at cursor. It returns the new content and the new cursor position.
func ApplyOperatorMotion(op Operator, motion Motion, content []rune, cursor int) ([]rune, int) {
	target := motion.Apply(content, cursor)

	start, end := cursor, target
	if start > end {
		start, end = end, start
	}
	// Make end exclusive — the motion target is inclusive in vim.
	if end < len(content) {
		end++
	}

	newContent := op.Apply(content, start, end)

	// Cursor lands at the start of the operated range.
	newCursor := start
	if newCursor > len(newContent) {
		newCursor = len(newContent)
	}
	if newCursor > 0 && newCursor == len(newContent) {
		newCursor = len(newContent) - 1
	}
	return newContent, newCursor
}

// ApplyOperatorTextObject executes an operator over the range defined by a
// text object. Returns the new content and cursor position.
func ApplyOperatorTextObject(op Operator, obj TextObject, content []rune, cursor int) ([]rune, int) {
	start, end := obj.Range(content, cursor)
	newContent := op.Apply(content, start, end)

	newCursor := start
	if newCursor > len(newContent) {
		newCursor = len(newContent)
	}
	if newCursor > 0 && newCursor == len(newContent) {
		newCursor = len(newContent) - 1
	}
	return newContent, newCursor
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// isWord returns true for characters that vim considers part of a "word"
// (letters, digits, and underscore).
func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// clampRange ensures start and end are within bounds and start <= end.
func clampRange(content []rune, start, end int) (int, int) {
	n := len(content)
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start > end {
		start, end = end, start
	}
	return start, end
}

// findMatchingPair finds the innermost matching pair of open/close characters
// around cursor. Returns (-1, -1) if no match is found.
func findMatchingPair(content []rune, cursor int, open, close rune) (int, int) {
	n := len(content)
	// Search backwards for the opening character, respecting nesting.
	depth := 0
	openPos := -1
	for i := cursor; i >= 0; i-- {
		if content[i] == close && i != cursor {
			depth++
		}
		if content[i] == open {
			if depth == 0 {
				openPos = i
				break
			}
			depth--
		}
	}
	if openPos < 0 {
		return -1, -1
	}
	// Search forwards from the opening character for the matching close.
	depth = 0
	for i := openPos; i < n; i++ {
		if content[i] == open {
			depth++
		}
		if content[i] == close {
			depth--
			if depth == 0 {
				return openPos, i
			}
		}
	}
	return -1, -1
}

// findQuotePair finds the nearest pair of quote characters surrounding cursor.
// Returns (-1, -1) if no pair is found.
func findQuotePair(content []rune, cursor int, quote rune) (int, int) {
	n := len(content)

	// If cursor is on a quote, determine direction.
	// Strategy: find the quote pair on the current line that contains cursor.
	// Collect all quote positions on the current line.
	lineStart := cursor
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := cursor
	for lineEnd < n && content[lineEnd] != '\n' {
		lineEnd++
	}

	// Gather quote positions within the line.
	var positions []int
	for i := lineStart; i < lineEnd; i++ {
		if content[i] == quote {
			positions = append(positions, i)
		}
	}
	// Find the pair that contains cursor.
	for i := 0; i+1 < len(positions); i += 2 {
		if positions[i] <= cursor && cursor <= positions[i+1] {
			return positions[i], positions[i+1]
		}
	}
	// Fallback: find the next pair after cursor.
	for i := 0; i+1 < len(positions); i += 2 {
		if positions[i] >= cursor {
			return positions[i], positions[i+1]
		}
	}
	return -1, -1
}
