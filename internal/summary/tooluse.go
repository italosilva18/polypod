package summary

// Deterministic, rule-based tool-use compaction.
//
// This file provides a pure, offline compressor for old tool outputs. It is
// used by the compaction path to reduce the size of historical tool results
// without calling the model. Given a tool name, its input map, and its raw
// output string, it produces a CompactToolUse value that can be rendered as
// a one-line placeholder inside the compacted conversation.
//
// NOTE: the name CompactToolUse is deliberately distinct from the existing
// ToolUseSummary struct in tool.go (which aggregates call statistics). The
// task brief asks for a type named ToolUseSummary, but that identifier is
// already taken in this package, so we use CompactToolUse instead and keep
// the task's semantics (per-tool compact one-paragraph summary).

import (
	"fmt"
	"strings"
)

// CompactToolUse is the deterministic per-tool-call summary used by the
// compaction path. Contains digested, length-bounded views of the tool input
// and output plus cheap statistics about the raw output.
type CompactToolUse struct {
	ToolName     string
	InputDigest  string
	OutputDigest string
	LineCount    int
	ByteCount    int
	WasError     bool
}

// SummarizeToolUse returns a deterministic, rule-based summary of a single
// tool call. It never calls an LLM, touches the filesystem, or performs any
// network I/O. Given identical inputs it always returns an identical result.
func SummarizeToolUse(toolName string, input map[string]any, output string) CompactToolUse {
	lineCount := countLines(output)
	byteCount := len(output)
	wasError := DetectError(output)

	var inputDigest, outputDigest string

	switch normalizeToolName(toolName) {
	case "bash":
		inputDigest = bashInputDigest(input)
		outputDigest = bashOutputDigest(output, lineCount)
	case "fileread", "read":
		inputDigest = fileReadInputDigest(input)
		outputDigest = fmt.Sprintf("read %d lines (%d bytes)", lineCount, byteCount)
	case "fileedit", "edit":
		inputDigest = fileEditInputDigest(input)
		outputDigest = "modified"
	case "filewrite", "write":
		inputDigest = fileEditInputDigest(input)
		outputDigest = "created"
	case "glob", "grep":
		inputDigest = patternInputDigest(input)
		outputDigest = fmt.Sprintf("%d matches", countMatchLines(output))
	case "webfetch":
		inputDigest = webInputDigest(input, "url")
		outputDigest = firstCharsEllipsis(output, 200)
	case "websearch":
		inputDigest = webInputDigest(input, "query")
		outputDigest = firstCharsEllipsis(output, 200)
	default:
		inputDigest = defaultInputDigest(input)
		outputDigest = firstCharsEllipsis(output, 100)
	}

	return CompactToolUse{
		ToolName:     toolName,
		InputDigest:  inputDigest,
		OutputDigest: outputDigest,
		LineCount:    lineCount,
		ByteCount:    byteCount,
		WasError:     wasError,
	}
}

// String returns a human-readable one-liner describing the tool call.
func (s CompactToolUse) String() string {
	status := ""
	if s.WasError {
		status = " [error]"
	}
	return fmt.Sprintf("%s: %s -> %s (%d lines, %d bytes)%s",
		s.ToolName, s.InputDigest, s.OutputDigest, s.LineCount, s.ByteCount, status)
}

// ToCompactBlock formats the summary for insertion into a compacted
// conversation as a replacement for the original tool output block.
//
// Example outputs:
//
//	[tool:bash] git status (0 exit, 3 lines)
//	[tool:Read] /tmp/foo.go (read 42 lines (1234 bytes))
//	[tool:Grep] pattern=func (5 matches)
func (s CompactToolUse) ToCompactBlock() string {
	marker := "tool"
	if s.WasError {
		marker = "tool-error"
	}
	return fmt.Sprintf("[%s:%s] %s (%s)", marker, s.ToolName, s.InputDigest, s.OutputDigest)
}

// DetectError applies a set of heuristics to the raw output to decide
// whether the tool call likely failed. It is intentionally conservative and
// looks for well-known error prefixes or the canonical Python traceback
// header.
func DetectError(output string) bool {
	trimmed := strings.TrimLeft(output, " \t\r\n")
	if trimmed == "" {
		return false
	}
	prefixes := []string{
		"Error:",
		"error:",
		"ERROR:",
		"panic:",
		"Panic:",
		"Exception",
		"Traceback (most recent call last)",
		"fatal:",
		"Fatal:",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	if strings.Contains(output, "Traceback (most recent call last)") {
		return true
	}
	return false
}

// --- internal helpers ---------------------------------------------------

func normalizeToolName(name string) string {
	lower := strings.ToLower(name)
	lower = strings.ReplaceAll(lower, "_", "")
	lower = strings.ReplaceAll(lower, "-", "")
	return lower
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func countMatchLines(s string) int {
	if s == "" {
		return 0
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	count := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			count++
		}
	}
	return count
}

func firstLine(s string) string {
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func firstCharsEllipsis(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func stringFromInput(input map[string]any, keys ...string) string {
	if input == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := input[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func intFromInput(input map[string]any, keys ...string) (int, bool) {
	if input == nil {
		return 0, false
	}
	for _, k := range keys {
		if v, ok := input[k]; ok {
			switch n := v.(type) {
			case int:
				return n, true
			case int64:
				return int(n), true
			case float64:
				return int(n), true
			}
		}
	}
	return 0, false
}

// --- per-tool input/output digests --------------------------------------

func bashInputDigest(input map[string]any) string {
	cmd := stringFromInput(input, "command", "cmd")
	return truncateString(cmd, 60)
}

func bashOutputDigest(output string, lineCount int) string {
	exit, hasExit := extractExitCode(output)
	line := firstLine(output)
	line = truncateString(line, 80)

	var parts []string
	if line != "" {
		parts = append(parts, line)
	}
	if lineCount > 1 {
		parts = append(parts, fmt.Sprintf("(%d lines of output)", lineCount))
	}
	if hasExit {
		parts = append(parts, fmt.Sprintf("exit=%d", exit))
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, " ")
}

// extractExitCode scans the output for a common "exit code" marker emitted
// by shell wrappers. Supported forms (case-insensitive):
//
//	exit code: N
//	exit: N
//	exit=N
//	(exit N)
func extractExitCode(output string) (int, bool) {
	lower := strings.ToLower(output)
	markers := []string{"exit code:", "exit:", "exit=", "(exit "}
	for _, m := range markers {
		idx := strings.Index(lower, m)
		if idx < 0 {
			continue
		}
		rest := output[idx+len(m):]
		rest = strings.TrimLeft(rest, " \t")
		// read leading digits
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end == 0 {
			continue
		}
		var n int
		for i := 0; i < end; i++ {
			n = n*10 + int(rest[i]-'0')
		}
		return n, true
	}
	return 0, false
}

func fileReadInputDigest(input map[string]any) string {
	path := stringFromInput(input, "file_path", "path", "file", "filename")
	if path == "" {
		return "(no path)"
	}
	offset, hasOffset := intFromInput(input, "offset", "start", "start_line")
	limit, hasLimit := intFromInput(input, "limit", "count", "lines")
	switch {
	case hasOffset && hasLimit:
		return fmt.Sprintf("%s [%d:%d]", path, offset, offset+limit)
	case hasOffset:
		return fmt.Sprintf("%s [%d:]", path, offset)
	case hasLimit:
		return fmt.Sprintf("%s [:%d]", path, limit)
	default:
		return path
	}
}

func fileEditInputDigest(input map[string]any) string {
	path := stringFromInput(input, "file_path", "path", "file", "filename")
	if path == "" {
		return "(no path)"
	}
	return path
}

func patternInputDigest(input map[string]any) string {
	pattern := stringFromInput(input, "pattern", "query", "search", "glob")
	if pattern == "" {
		return "(no pattern)"
	}
	return truncateString(pattern, 60)
}

func webInputDigest(input map[string]any, primaryKey string) string {
	v := stringFromInput(input, primaryKey, "url", "query", "q")
	if v == "" {
		return "(no " + primaryKey + ")"
	}
	return truncateString(v, 80)
}

func defaultInputDigest(input map[string]any) string {
	return fmt.Sprintf("{...} %d keys", len(input))
}
