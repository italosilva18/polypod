package bash

import (
	"strings"
	"unicode"
)

// shellSpecialChars are characters that have special meaning in bash
// and require quoting to be treated literally.
const shellSpecialChars = " \t\n\"'\\$`!#&|;(){}[]<>?*~^"

// shellMetachars are characters that separate words in bash.
const shellMetachars = " \t\n|&;()<>"

// Quote safely quotes a string for use in a shell command.
// It uses single quotes which prevent all interpretation, with the
// standard trick of ending the single-quoted string, adding an escaped
// single quote, and reopening the single-quoted string: 'it'\”s'
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if !NeedsQuoting(s) {
		return s
	}
	// Use single quotes — they prevent all expansion.
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b.WriteString("'\\''")
		} else {
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// Unquote removes shell quoting from a word.
// Handles single quotes, double quotes, backslash escapes, and ANSI-C strings.
func Unquote(s string) string {
	if len(s) == 0 {
		return s
	}

	var result strings.Builder
	result.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'':
			// Single-quoted string: everything is literal until closing '
			i++
			for i < len(s) && s[i] != '\'' {
				result.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // skip closing '
			}
		case c == '"':
			// Double-quoted string: backslash escapes work for $ ` " \ \n
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					next := s[i+1]
					if next == '$' || next == '`' || next == '"' || next == '\\' || next == '\n' {
						result.WriteByte(next)
						i += 2
						continue
					}
				}
				result.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // skip closing "
			}
		case c == '\\' && i+1 < len(s):
			// Backslash escape: next char is literal
			result.WriteByte(s[i+1])
			i += 2
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			// ANSI-C string $'...'
			i += 2 // skip $'
			for i < len(s) && s[i] != '\'' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					ch := s[i]
					switch ch {
					case 'n':
						result.WriteByte('\n')
					case 't':
						result.WriteByte('\t')
					case 'r':
						result.WriteByte('\r')
					case 'a':
						result.WriteByte('\a')
					case 'b':
						result.WriteByte('\b')
					case 'e', 'E':
						result.WriteByte(0x1B) // escape
					case 'f':
						result.WriteByte('\f')
					case 'v':
						result.WriteByte('\v')
					case '\\':
						result.WriteByte('\\')
					case '\'':
						result.WriteByte('\'')
					case '"':
						result.WriteByte('"')
					case '0':
						// Octal \0nnn
						val := byte(0)
						for j := 0; j < 3 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7'; j++ {
							i++
							val = val*8 + s[i] - '0'
						}
						result.WriteByte(val)
					case 'x':
						// Hex \xHH
						val := byte(0)
						for j := 0; j < 2 && i+1 < len(s) && isHexDigitByte(s[i+1]); j++ {
							i++
							val = val*16 + hexVal(s[i])
						}
						result.WriteByte(val)
					default:
						result.WriteByte('\\')
						result.WriteByte(ch)
					}
					i++
				} else {
					result.WriteByte(s[i])
					i++
				}
			}
			if i < len(s) {
				i++ // skip closing '
			}
		default:
			result.WriteByte(c)
			i++
		}
	}
	return result.String()
}

func isHexDigitByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// EscapeForDoubleQuotes escapes special characters inside double quotes.
// In double quotes, only $ ` " \ and newline have special meaning.
func EscapeForDoubleQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '$', '`', '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// EscapeForSingleQuotes handles the single quote inside single-quoted strings.
// The only character that needs escaping inside single quotes is the
// single quote itself, done via the 'it'\”s' technique.
func EscapeForSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// NeedsQuoting returns true if the string contains any characters that
// have special meaning in bash and would need quoting to be treated literally.
func NeedsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.ContainsRune(shellSpecialChars, rune(c)) {
			return true
		}
		// Also check for non-ASCII characters
		if c > 127 {
			return true
		}
	}
	// Check if it looks like a keyword
	if isShellKeyword(s) {
		return true
	}
	return false
}

// SplitQuotedWords splits a string into words, respecting shell quoting.
// Quoted sections are not split on whitespace. Backslash escapes are honored.
func SplitQuotedWords(s string) []string {
	var words []string
	var current strings.Builder
	inWord := false
	i := 0

	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			if !inWord {
				inWord = true
			}
			current.WriteByte(s[i+1])
			i += 2

		case c == '\'':
			if !inWord {
				inWord = true
			}
			i++
			for i < len(s) && s[i] != '\'' {
				current.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // skip closing '
			}

		case c == '"':
			if !inWord {
				inWord = true
			}
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					next := s[i+1]
					if next == '$' || next == '`' || next == '"' || next == '\\' {
						current.WriteByte(next)
						i += 2
						continue
					}
				}
				current.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // skip closing "
			}

		case unicode.IsSpace(rune(c)):
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
			i++

		default:
			if !inWord {
				inWord = true
			}
			current.WriteByte(c)
			i++
		}
	}

	if inWord {
		words = append(words, current.String())
	}
	return words
}

// JoinCommand joins command words with proper quoting into a single string.
// Each word is quoted only if it contains special characters.
func JoinCommand(words []string) string {
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = Quote(w)
	}
	return strings.Join(quoted, " ")
}

// QuoteForEval quotes a command for passing to eval or sh -c.
// Uses single quotes with ' → '\” escaping.
func QuoteForEval(command string) string {
	return Quote(command)
}

// IsValidVariableName checks if a string is a valid bash variable name.
func IsValidVariableName(name string) bool {
	if len(name) == 0 {
		return false
	}
	if !isIdentStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isIdentChar(name[i]) {
			return false
		}
	}
	return true
}

// HasUnbalancedQuotes checks if a command has mismatched quotes.
func HasUnbalancedQuotes(command string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\\' && !inSingle:
			i++ // skip escaped character
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		}
	}
	return inSingle || inDouble
}

// StripOuterQuotes removes a single layer of matching quotes from a string.
// Handles both single and double quotes.
func StripOuterQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '\'' && s[len(s)-1] == '\'') ||
		(s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}

// ContainsShellOperator checks if the string contains unquoted shell operators.
func ContainsShellOperator(s string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && !inSingle && i+1 < len(s):
			i++ // skip escaped char
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble:
			if c == '|' || c == '&' || c == ';' || c == '<' || c == '>' ||
				c == '(' || c == ')' {
				return true
			}
		}
	}
	return false
}
