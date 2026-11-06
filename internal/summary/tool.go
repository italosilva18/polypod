package summary

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ToolUseSummary aggregates statistics about repeated calls of a single tool.
//
// NOTE: Distinct from the existing ToolUseBatch (in summary.go) which holds a
// human-readable label for an entire batch. This struct is the per-tool view
// produced by SummarizeToolUses.
type ToolUseSummary struct {
	ToolName      string
	CallCount     int
	TotalDuration time.Duration
	Targets       []string // Files / commands acted upon
	SuccessCount  int
	ErrorCount    int
}

// ToolUsePattern is a higher-level interpretation derived from a sequence of
// tool calls (e.g. "Reading files in internal/auth").
type ToolUsePattern struct {
	Description string
	Tools       []string
	Files       []string
	Confidence  float64
}

// ToolCall is the input record consumed by SummarizeToolUses and
// DetectPatterns. It is intentionally untyped on Args so callers can pass any
// shape produced by their tool layer.
type ToolCall struct {
	Name     string
	Args     map[string]any
	Result   string
	Duration time.Duration
	Success  bool
}

// SummarizeToolUses takes a sequence of tool calls and groups consecutive
// invocations of the same tool into a per-tool ToolUseSummary.
func SummarizeToolUses(calls []ToolCall) []ToolUseSummary {
	if len(calls) == 0 {
		return nil
	}

	var summaries []ToolUseSummary
	var current *ToolUseSummary

	flush := func() {
		if current != nil {
			summaries = append(summaries, *current)
			current = nil
		}
	}

	for _, c := range calls {
		if current == nil || current.ToolName != c.Name {
			flush()
			current = &ToolUseSummary{ToolName: c.Name}
		}
		current.CallCount++
		current.TotalDuration += c.Duration
		if c.Success {
			current.SuccessCount++
		} else {
			current.ErrorCount++
		}
		if t := extractTarget(c); t != "" {
			current.Targets = appendUnique(current.Targets, t)
		}
	}
	flush()

	return summaries
}

// DetectPatterns analyses calls and returns high-level patterns. Order of
// detection matters: more specific multi-tool patterns are checked first so
// that the same calls are not double-counted.
func DetectPatterns(calls []ToolCall) []ToolUsePattern {
	if len(calls) == 0 {
		return nil
	}

	var patterns []ToolUsePattern

	tools := toolNameSet(calls)
	files := allFiles(calls)

	// --- Multi-tool patterns ---

	// file_edit + bash + grep -> Refactoring
	if hasAny(tools, "edit", "file_edit") &&
		hasAny(tools, "bash", "run_command") &&
		hasAny(tools, "grep") {
		patterns = append(patterns, ToolUsePattern{
			Description: refactorDescription(files),
			Tools:       []string{"edit", "bash", "grep"},
			Files:       files,
			Confidence:  0.85,
		})
	}

	// file_write + bash -> Creating new feature
	if hasAny(tools, "write", "file_write") && hasAny(tools, "bash", "run_command") {
		patterns = append(patterns, ToolUsePattern{
			Description: createFeatureDescription(files),
			Tools:       []string{"write", "bash"},
			Files:       files,
			Confidence:  0.7,
		})
	}

	// bash + go test -> Running tests
	if hasAny(tools, "bash", "run_command") && containsTestCommand(calls) {
		patterns = append(patterns, ToolUsePattern{
			Description: "Running tests",
			Tools:       []string{"bash"},
			Confidence:  0.9,
		})
	}

	// grep + read_file -> Searching for X
	if hasAny(tools, "grep") && hasAny(tools, "read", "read_file") {
		needle := extractGrepNeedle(calls)
		desc := "Searching the codebase"
		if needle != "" {
			desc = fmt.Sprintf("Searching for %q", needle)
		}
		patterns = append(patterns, ToolUsePattern{
			Description: desc,
			Tools:       []string{"grep", "read_file"},
			Confidence:  0.75,
		})
	}

	// --- Single-tool dominance patterns ---

	// 3+ read_file calls in same directory -> Reading files in <dir>
	dirs := readDirGroups(calls)
	for dir, count := range dirs {
		if count >= 3 {
			patterns = append(patterns, ToolUsePattern{
				Description: fmt.Sprintf("Reading files in %s", dir),
				Tools:       []string{"read_file"},
				Confidence:  0.8,
			})
		}
	}

	return patterns
}

// FormatToolUseSummary renders a slice of summaries as a compact table-like
// block. Returns an empty string when summaries is empty.
func FormatToolUseSummary(summaries []ToolUseSummary) string {
	if len(summaries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Tool usage:\n")
	for _, s := range summaries {
		ok := s.SuccessCount
		fail := s.ErrorCount
		fmt.Fprintf(&b, "  - %s x%d  (%s)  ok=%d err=%d",
			s.ToolName,
			s.CallCount,
			formatDuration(s.TotalDuration),
			ok,
			fail,
		)
		if len(s.Targets) > 0 {
			targets := s.Targets
			if len(targets) > 3 {
				targets = append(targets[:3], "...")
			}
			fmt.Fprintf(&b, "  -> %s", strings.Join(targets, ", "))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatPatterns renders detected patterns as bullet lines, sorted by
// descending confidence.
func FormatPatterns(patterns []ToolUsePattern) string {
	if len(patterns) == 0 {
		return ""
	}
	sorted := make([]ToolUsePattern, len(patterns))
	copy(sorted, patterns)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Confidence > sorted[j].Confidence
	})

	var b strings.Builder
	b.WriteString("Patterns:\n")
	for _, p := range sorted {
		fmt.Fprintf(&b, "  - %s  (%.0f%%)\n", p.Description, p.Confidence*100)
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- internal helpers ---

func extractTarget(c ToolCall) string {
	if c.Args == nil {
		return ""
	}
	for _, key := range []string{"path", "file", "file_path", "filename", "command", "cmd", "pattern"} {
		if v, ok := c.Args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func toolNameSet(calls []ToolCall) map[string]bool {
	set := make(map[string]bool, len(calls))
	for _, c := range calls {
		set[strings.ToLower(c.Name)] = true
	}
	return set
}

func hasAny(set map[string]bool, names ...string) bool {
	for _, n := range names {
		if set[strings.ToLower(n)] {
			return true
		}
	}
	return false
}

func allFiles(calls []ToolCall) []string {
	var out []string
	for _, c := range calls {
		if t := extractTarget(c); t != "" && looksLikePath(t) {
			out = appendUnique(out, t)
		}
	}
	return out
}

func looksLikePath(s string) bool {
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	if strings.Contains(s, ".") && !strings.Contains(s, " ") {
		return true
	}
	return false
}

func containsTestCommand(calls []ToolCall) bool {
	for _, c := range calls {
		if !strings.EqualFold(c.Name, "bash") && !strings.EqualFold(c.Name, "run_command") {
			continue
		}
		cmd := stringArg(c, "command", "cmd")
		lower := strings.ToLower(cmd)
		if strings.Contains(lower, "go test") ||
			strings.Contains(lower, "pytest") ||
			strings.Contains(lower, "npm test") ||
			strings.Contains(lower, "npm run test") ||
			strings.Contains(lower, "cargo test") {
			return true
		}
	}
	return false
}

func stringArg(c ToolCall, keys ...string) string {
	if c.Args == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := c.Args[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func extractGrepNeedle(calls []ToolCall) string {
	for _, c := range calls {
		if !strings.EqualFold(c.Name, "grep") {
			continue
		}
		if s := stringArg(c, "pattern", "query", "search"); s != "" {
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	return ""
}

func readDirGroups(calls []ToolCall) map[string]int {
	groups := make(map[string]int)
	for _, c := range calls {
		name := strings.ToLower(c.Name)
		if name != "read" && name != "read_file" {
			continue
		}
		path := stringArg(c, "path", "file", "file_path", "filename")
		if path == "" {
			continue
		}
		dir := filepath.Dir(path)
		if dir == "." || dir == "/" {
			continue
		}
		groups[dir]++
	}
	return groups
}

func refactorDescription(files []string) string {
	if len(files) == 0 {
		return "Refactoring code"
	}
	target := files[0]
	if len(files) == 1 {
		return fmt.Sprintf("Refactoring %s", target)
	}
	return fmt.Sprintf("Refactoring %s (+%d more)", target, len(files)-1)
}

func createFeatureDescription(files []string) string {
	if len(files) == 0 {
		return "Creating a new feature"
	}
	first := files[0]
	base := filepath.Base(first)
	if len(files) == 1 {
		return fmt.Sprintf("Creating %s", base)
	}
	return fmt.Sprintf("Creating %s (+%d more)", base, len(files)-1)
}
