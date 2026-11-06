// Package bash provides a sed command parser for detecting file modifications.
// This file parses sed expressions to extract target files and detect in-place
// editing operations. Migrated from Claude Code's sedParser.ts and sedValidation.ts.
package bash

import (
	"regexp"
	"strings"
)

// SedCommand represents a parsed sed command.
type SedCommand struct {
	// InPlace is true if the -i flag is present (modifies files in place).
	InPlace bool

	// BackupSuffix is the backup extension from -i (e.g., ".bak").
	// Empty string means -i with no backup.
	BackupSuffix string

	// Expressions are the sed script expressions (-e or inline).
	Expressions []string

	// Files are the target files sed will operate on.
	Files []string

	// ScriptFile is the -f script file path (if used).
	ScriptFile string

	// IsReadOnly is true if the sed command only reads (no -i, output to stdout).
	IsReadOnly bool

	// HasSubstitution is true if any expression contains s/.../.../ patterns.
	HasSubstitution bool

	// HasDeletion is true if any expression contains d commands.
	HasDeletion bool

	// HasAppend is true if any expression contains a/i/c commands.
	HasAppend bool

	// HasWrite is true if any expression contains w command (write to file).
	HasWrite bool
}

// ParseSedCommand parses a sed command string into its components.
// Returns nil if the command is not a sed command.
func ParseSedCommand(command string) *SedCommand {
	stripped := StripSafeWrappers(strings.TrimSpace(command))
	tokens := tokenizeCommand(stripped)
	if len(tokens) == 0 {
		return nil
	}

	// Check if this is a sed command
	if tokens[0] != "sed" && tokens[0] != "gsed" {
		return nil
	}

	result := &SedCommand{}
	args := tokens[1:]

	i := 0
	for i < len(args) {
		arg := args[i]

		switch {
		case arg == "-i" || arg == "--in-place":
			result.InPlace = true
			i++

		case strings.HasPrefix(arg, "-i"):
			result.InPlace = true
			result.BackupSuffix = arg[2:]
			i++

		case strings.HasPrefix(arg, "--in-place="):
			result.InPlace = true
			result.BackupSuffix = strings.TrimPrefix(arg, "--in-place=")
			i++

		case arg == "-e" || arg == "--expression":
			if i+1 < len(args) {
				result.Expressions = append(result.Expressions, args[i+1])
				i += 2
			} else {
				i++
			}

		case arg == "-f" || arg == "--file":
			if i+1 < len(args) {
				result.ScriptFile = args[i+1]
				i += 2
			} else {
				i++
			}

		case strings.HasPrefix(arg, "-"):
			// Other flags: -n, -E, -r, etc.
			i++

		default:
			// First non-flag argument is either the script (if no -e) or a file
			if len(result.Expressions) == 0 && result.ScriptFile == "" {
				result.Expressions = append(result.Expressions, arg)
			} else {
				result.Files = append(result.Files, arg)
			}
			i++
		}
	}

	// Analyze expressions
	for _, expr := range result.Expressions {
		analyzeSedExpression(expr, result)
	}

	// Determine if read-only
	result.IsReadOnly = !result.InPlace && !result.HasWrite

	return result
}

// analyzeSedExpression examines a sed expression and sets flags on the command.
func analyzeSedExpression(expr string, cmd *SedCommand) {
	if sedSubstitutionRe.MatchString(expr) {
		cmd.HasSubstitution = true
	}
	if sedDeleteRe.MatchString(expr) {
		cmd.HasDeletion = true
	}
	if sedAppendRe.MatchString(expr) {
		cmd.HasAppend = true
	}
	if sedWriteRe.MatchString(expr) {
		cmd.HasWrite = true
	}
}

var (
	// sedSubstitutionRe matches s/pattern/replacement/ commands.
	sedSubstitutionRe = regexp.MustCompile(`(?:^|\n|;)\s*(?:\d+(?:,\d+)?\s*)?s[/|#@!]`)

	// sedDeleteRe matches d (delete) commands.
	sedDeleteRe = regexp.MustCompile(`(?:^|\n|;)\s*(?:\d+(?:,\d+)?\s*)?d\s*(?:$|;|\n)`)

	// sedAppendRe matches a\ (append), i\ (insert), c\ (change) commands.
	sedAppendRe = regexp.MustCompile(`(?:^|\n|;)\s*(?:\d+(?:,\d+)?\s*)?[aic]\\`)

	// sedWriteRe matches w (write to file) command.
	sedWriteRe = regexp.MustCompile(`(?:^|\n|;)\s*(?:\d+(?:,\d+)?\s*)?w\s+\S`)
)

// SedEditInfo contains information about a sed in-place edit for permission display.
type SedEditInfo struct {
	FilePath    string
	Expressions []string
	IsInPlace   bool
}

// ParseSedEditCommand extracts sed edit information for permission UI.
// Returns nil if the command is not an in-place sed edit.
func ParseSedEditCommand(command string) *SedEditInfo {
	parsed := ParseSedCommand(command)
	if parsed == nil {
		return nil
	}
	if !parsed.InPlace || len(parsed.Files) == 0 {
		return nil
	}

	return &SedEditInfo{
		FilePath:    parsed.Files[0],
		Expressions: parsed.Expressions,
		IsInPlace:   true,
	}
}

// SedExpressionParts breaks a sed substitution expression into its components.
type SedExpressionParts struct {
	// Address is the line address or range (e.g., "1,5" or "/pattern/").
	Address string

	// Command is the sed command (s, d, a, i, c, y, etc.).
	Command string

	// Pattern is the search pattern (for s and y commands).
	Pattern string

	// Replacement is the replacement string (for s and y commands).
	Replacement string

	// Flags are the command flags (g, p, I, etc.).
	Flags string

	// WriteFile is the file target for w flag.
	WriteFile string
}

// ParseSedExpression parses a single sed expression into its parts.
// Returns nil if the expression can't be parsed.
func ParseSedExpression(expr string) *SedExpressionParts {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	result := &SedExpressionParts{}

	// Extract address prefix
	remaining := extractSedAddress(expr, result)

	if len(remaining) == 0 {
		return nil
	}

	// Parse the command
	result.Command = string(remaining[0])
	remaining = remaining[1:]

	switch result.Command {
	case "s", "y":
		// s/pattern/replacement/flags or y/from/to/
		return parseSedSubstitution(remaining, result)
	case "d", "D", "p", "P", "q", "Q", "h", "H", "g", "G", "x", "l", "n", "N", "z":
		// Single-character commands with optional flags
		result.Flags = strings.TrimSpace(remaining)
		return result
	case "a", "i", "c":
		// Text insertion commands
		result.Replacement = strings.TrimLeft(remaining, "\\")
		result.Replacement = strings.TrimSpace(result.Replacement)
		return result
	case "w":
		// Write to file
		result.WriteFile = strings.TrimSpace(remaining)
		return result
	}

	return result
}

// extractSedAddress extracts the address prefix from a sed expression.
func extractSedAddress(expr string, parts *SedExpressionParts) string {
	if len(expr) == 0 {
		return ""
	}

	// Numeric address: 5 or 5,10 or $
	if expr[0] >= '0' && expr[0] <= '9' || expr[0] == '$' {
		end := 0
		for end < len(expr) && (expr[end] >= '0' && expr[end] <= '9' || expr[end] == ',' || expr[end] == '$' || expr[end] == ' ') {
			end++
		}
		parts.Address = strings.TrimSpace(expr[:end])
		return expr[end:]
	}

	// Regex address: /pattern/ or /pattern/,/pattern2/
	if expr[0] == '/' {
		end := findMatchingDelimiter(expr, 1, '/')
		if end < 0 {
			return expr
		}
		remaining := expr[end+1:]
		// Check for range: ,/pattern2/
		if len(remaining) > 0 && remaining[0] == ',' {
			remaining = remaining[1:]
			if len(remaining) > 0 && remaining[0] == '/' {
				end2 := findMatchingDelimiter(remaining, 1, '/')
				if end2 >= 0 {
					parts.Address = expr[:end+1+1+end2+1]
					return remaining[end2+1:]
				}
			}
		}
		parts.Address = expr[:end+1]
		return remaining
	}

	return expr
}

// parseSedSubstitution parses the s/pattern/replacement/flags part.
func parseSedSubstitution(remaining string, parts *SedExpressionParts) *SedExpressionParts {
	if len(remaining) == 0 {
		return nil
	}

	delim := remaining[0]
	remaining = remaining[1:]

	// Extract pattern
	patEnd := findMatchingDelimiter(remaining, 0, delim)
	if patEnd < 0 {
		parts.Pattern = remaining
		return parts
	}
	parts.Pattern = remaining[:patEnd]
	remaining = remaining[patEnd+1:]

	// Extract replacement
	repEnd := findMatchingDelimiter(remaining, 0, delim)
	if repEnd < 0 {
		parts.Replacement = remaining
		return parts
	}
	parts.Replacement = remaining[:repEnd]
	remaining = remaining[repEnd+1:]

	// Flags
	parts.Flags = remaining

	// Check for w flag with file
	if strings.Contains(parts.Flags, "w") {
		idx := strings.Index(parts.Flags, "w")
		parts.WriteFile = strings.TrimSpace(parts.Flags[idx+1:])
		parts.Flags = parts.Flags[:idx+1]
	}

	return parts
}

// findMatchingDelimiter finds the matching closing delimiter, handling backslash escapes.
func findMatchingDelimiter(s string, start int, delim byte) int {
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == delim {
			return i
		}
	}
	return -1
}

// IsSedReadOnly checks if a sed command only reads data (no -i flag, no w command).
func IsSedReadOnly(command string) bool {
	parsed := ParseSedCommand(command)
	if parsed == nil {
		return true // not a sed command
	}
	return parsed.IsReadOnly
}

// SedTargetFiles returns the files that a sed command will modify.
// Returns nil if the command is read-only.
func SedTargetFiles(command string) []string {
	parsed := ParseSedCommand(command)
	if parsed == nil {
		return nil
	}
	if !parsed.InPlace {
		return nil
	}
	return parsed.Files
}

// ValidateSedConstraints checks if a sed command should be allowed.
// Returns true if the command is safe, with an optional message.
func ValidateSedConstraints(command string) (bool, string) {
	parsed := ParseSedCommand(command)
	if parsed == nil {
		return true, "" // not a sed command
	}

	// Read-only sed is always safe
	if parsed.IsReadOnly {
		return true, ""
	}

	// Check for w (write) command which can write to arbitrary files
	if parsed.HasWrite {
		return false, "sed command contains w (write) which can write to arbitrary files"
	}

	// In-place edits need file path validation
	if parsed.InPlace && len(parsed.Files) > 0 {
		// The caller should validate the file paths
		return true, ""
	}

	return true, ""
}
