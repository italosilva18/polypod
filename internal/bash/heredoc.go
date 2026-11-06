package bash

import (
	"regexp"
	"strings"
)

// HeredocInfo holds information about a parsed heredoc.
type HeredocInfo struct {
	// Delimiter is the heredoc delimiter word (without quotes).
	Delimiter string
	// Body is the content between the opening and closing delimiters.
	Body string
	// IsQuoted indicates whether the delimiter was quoted ('EOF', "EOF", \EOF).
	// Quoted heredocs do NOT undergo shell expansion — body is literal text.
	IsQuoted bool
	// StripTabs indicates <<- variant which strips leading tabs from body.
	StripTabs bool
	// Operator is "<<" or "<<-".
	Operator string
	// BodyStart is the byte offset where the body begins.
	BodyStart int
	// BodyEnd is the byte offset where the body ends (exclusive).
	BodyEnd int
}

// heredocStartPattern matches heredoc start syntax.
// Matches: <<EOF, <<'EOF', <<"EOF", <<-EOF, <<-'EOF', <<\EOF
//
// Capture groups:
//
//	1: dash indicator ("-" if <<- variant)
//	2: single-quoted delimiter contents
//	3: double-quoted delimiter contents
//	4: unquoted delimiter (optionally backslash-escaped)
//
// Note: RE2 (Go's regex engine) does not support backreferences, so we use
// three alternatives instead of a single quoted-group with `\2`.
var heredocStartPattern = regexp.MustCompile(`(?:^|[^<])<<(-)?[ \t]*(?:'(\\?\w+)'|"(\\?\w+)"|\\?(\w+))`)

// ParseHeredoc detects and parses heredoc syntax from a command string.
// Returns nil if no heredoc is found.
func ParseHeredoc(command string) *HeredocInfo {
	if !strings.Contains(command, "<<") {
		return nil
	}

	m := heredocStartPattern.FindStringSubmatchIndex(command)
	if m == nil {
		return nil
	}

	// Extract match groups
	fullMatch := command[m[0]:m[1]]
	isDash := m[2] >= 0 && m[3] >= 0 && command[m[2]:m[3]] == "-"
	var delimiter string
	var isQuoted bool

	// Group 2 = single-quoted delimiter, Group 3 = double-quoted delimiter,
	// Group 4 = unquoted delimiter (optionally backslash-escaped).
	if m[4] >= 0 && m[5] >= 0 {
		delimiter = strings.TrimPrefix(command[m[4]:m[5]], "\\")
		isQuoted = true
	} else if m[6] >= 0 && m[7] >= 0 {
		delimiter = strings.TrimPrefix(command[m[6]:m[7]], "\\")
		isQuoted = true
	} else if m[8] >= 0 && m[9] >= 0 {
		delimiter = command[m[8]:m[9]]
		isQuoted = strings.Contains(fullMatch, "\\")
	}

	if delimiter == "" {
		return nil
	}

	// Find content start (next newline after operator)
	operatorEnd := m[1]
	nlIdx := strings.Index(command[operatorEnd:], "\n")
	if nlIdx < 0 {
		return nil
	}
	contentStart := operatorEnd + nlIdx + 1

	// Find closing delimiter
	lines := strings.Split(command[contentStart:], "\n")
	bodyEnd := -1
	for i, line := range lines {
		checkLine := line
		if isDash {
			checkLine = strings.TrimLeft(line, "\t")
		}
		if checkLine == delimiter {
			// Calculate body end position
			bodyLen := 0
			for j := 0; j < i; j++ {
				bodyLen += len(lines[j]) + 1
			}
			bodyEnd = contentStart + bodyLen
			break
		}
	}

	if bodyEnd < 0 {
		return nil
	}

	body := command[contentStart:bodyEnd]
	if isDash {
		body = StripLeadingTabs(body)
	}

	op := "<<"
	if isDash {
		op = "<<-"
	}

	return &HeredocInfo{
		Delimiter: delimiter,
		Body:      body,
		IsQuoted:  isQuoted,
		StripTabs: isDash,
		Operator:  op,
		BodyStart: contentStart,
		BodyEnd:   bodyEnd,
	}
}

// ExtractHeredocContent extracts the content between heredoc delimiters.
// It takes the full command text and the delimiter to search for.
func ExtractHeredocContent(command, delimiter string) string {
	info := ParseHeredoc(command)
	if info == nil || info.Delimiter != delimiter {
		// Try manual extraction
		return extractHeredocManual(command, delimiter)
	}
	return info.Body
}

func extractHeredocManual(command, delimiter string) string {
	// Find the opening <<[DELIMITER] pattern
	patterns := []string{
		"<<" + delimiter,
		"<<'" + delimiter + "'",
		"<<\"" + delimiter + "\"",
		"<<\\" + delimiter,
		"<<-" + delimiter,
		"<<-'" + delimiter + "'",
		"<<-\"" + delimiter + "\"",
		"<<-\\" + delimiter,
	}

	startIdx := -1
	isDash := false
	for _, pat := range patterns {
		idx := strings.Index(command, pat)
		if idx >= 0 {
			startIdx = idx + len(pat)
			isDash = strings.Contains(pat, "<<-")
			break
		}
	}

	if startIdx < 0 {
		return ""
	}

	// Find next newline
	nlIdx := strings.Index(command[startIdx:], "\n")
	if nlIdx < 0 {
		return ""
	}
	contentStart := startIdx + nlIdx + 1

	// Find closing delimiter
	rest := command[contentStart:]
	lines := strings.Split(rest, "\n")
	var bodyLines []string
	for _, line := range lines {
		checkLine := line
		if isDash {
			checkLine = strings.TrimLeft(line, "\t")
		}
		if checkLine == delimiter {
			break
		}
		bodyLines = append(bodyLines, line)
	}

	body := strings.Join(bodyLines, "\n")
	if isDash {
		body = StripLeadingTabs(body)
	}
	return body
}

// IsQuotedHeredoc checks if a heredoc delimiter is quoted (no expansion).
// In bash, quoted delimiters (<<'EOF', <<"EOF", <<\EOF) suppress all
// expansion in the heredoc body — content is literal text.
func IsQuotedHeredoc(delimiterSpec string) bool {
	d := strings.TrimSpace(delimiterSpec)
	if len(d) == 0 {
		return false
	}
	// Single-quoted: 'EOF'
	if d[0] == '\'' && d[len(d)-1] == '\'' {
		return true
	}
	// Double-quoted: "EOF"
	if d[0] == '"' && d[len(d)-1] == '"' {
		return true
	}
	// Backslash-escaped: \EOF
	if d[0] == '\\' {
		return true
	}
	return false
}

// StripLeadingTabs removes leading tab characters from each line.
// This implements the <<- heredoc behavior.
func StripLeadingTabs(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, "\t")
	}
	return strings.Join(lines, "\n")
}

// ContainsHeredoc checks if a command string contains heredoc syntax.
func ContainsHeredocSyntax(command string) bool {
	if !strings.Contains(command, "<<") {
		return false
	}
	return heredocStartPattern.MatchString(command)
}

// ExtractAllHeredocs finds all heredoc occurrences in a command.
func ExtractAllHeredocs(command string) []HeredocInfo {
	if !strings.Contains(command, "<<") {
		return nil
	}

	var results []HeredocInfo
	remaining := command
	offset := 0

	for {
		m := heredocStartPattern.FindStringSubmatchIndex(remaining)
		if m == nil {
			break
		}

		isDash := m[2] >= 0 && m[3] >= 0 && remaining[m[2]:m[3]] == "-"
		fullMatch := remaining[m[0]:m[1]]
		var delimiter string
		var isQuoted bool

		if m[4] >= 0 && m[5] >= 0 {
			delimiter = strings.TrimPrefix(remaining[m[4]:m[5]], "\\")
			isQuoted = true
		} else if m[6] >= 0 && m[7] >= 0 {
			delimiter = strings.TrimPrefix(remaining[m[6]:m[7]], "\\")
			isQuoted = true
		} else if m[8] >= 0 && m[9] >= 0 {
			delimiter = remaining[m[8]:m[9]]
			isQuoted = strings.Contains(fullMatch, "\\")
		}

		if delimiter == "" {
			remaining = remaining[m[1]:]
			offset += m[1]
			continue
		}

		operatorEnd := m[1]
		nlIdx := strings.Index(remaining[operatorEnd:], "\n")
		if nlIdx < 0 {
			break
		}
		contentStart := operatorEnd + nlIdx + 1

		lines := strings.Split(remaining[contentStart:], "\n")
		found := false
		bodyEnd := contentStart
		for i, line := range lines {
			checkLine := line
			if isDash {
				checkLine = strings.TrimLeft(line, "\t")
			}
			if checkLine == delimiter {
				bodyLen := 0
				for j := 0; j < i; j++ {
					bodyLen += len(lines[j]) + 1
				}
				bodyEnd = contentStart + bodyLen
				found = true
				break
			}
		}

		if !found {
			remaining = remaining[m[1]:]
			offset += m[1]
			continue
		}

		body := remaining[contentStart:bodyEnd]
		if isDash {
			body = StripLeadingTabs(body)
		}

		op := "<<"
		if isDash {
			op = "<<-"
		}

		results = append(results, HeredocInfo{
			Delimiter: delimiter,
			Body:      body,
			IsQuoted:  isQuoted,
			StripTabs: isDash,
			Operator:  op,
			BodyStart: offset + contentStart,
			BodyEnd:   offset + bodyEnd,
		})

		// Move past this heredoc
		skipTo := bodyEnd + len(delimiter) + 1
		if skipTo >= len(remaining) {
			break
		}
		offset += skipTo
		remaining = remaining[skipTo:]
	}

	return results
}

// stdinRedirectPattern matches a stdin redirect operator preceded by start of
// string or a shell separator. We then manually exclude `<<` (heredoc) and
// `<(` (process substitution) by inspecting the character after `<`, since
// RE2 does not support negative lookahead.
var stdinRedirectPattern = regexp.MustCompile(`(?:^|[\s;&|])<`)

// HasStdinRedirect checks if a command already has a stdin redirect.
func HasStdinRedirect(command string) bool {
	for _, m := range stdinRedirectPattern.FindAllStringIndex(command, -1) {
		// m[1] is the position immediately after the `<` operator.
		if m[1] < len(command) {
			next := command[m[1]]
			if next == '<' || next == '(' {
				continue
			}
		}
		// Require at least one non-space target after optional whitespace.
		rest := strings.TrimLeft(command[m[1]:], " \t")
		if len(rest) > 0 && rest[0] != '\n' {
			return true
		}
	}
	return false
}

// ShouldAddStdinRedirect determines if adding < /dev/null is appropriate.
func ShouldAddStdinRedirect(command string) bool {
	if ContainsHeredocSyntax(command) {
		return false
	}
	if HasStdinRedirect(command) {
		return false
	}
	return true
}
