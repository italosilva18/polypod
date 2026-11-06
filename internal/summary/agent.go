package summary

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AgentRunSummary captures everything we want to remember about a single
// completed run of a sub-agent (via /agent or AgentTool).
type AgentRunSummary struct {
	AgentName    string
	StartTime    time.Time
	Duration     time.Duration
	ToolCalls    int
	InputTokens  int
	OutputTokens int
	Cost         float64
	Status       string   // "success", "error", "cancelled"
	KeyActions   []string // What the agent did (high-level bullets)
	Result       string   // Brief outcome
}

// AgentSummaryService keeps a bounded history of recent sub-agent runs so the
// CLI can render an "agent activity" panel.
type AgentSummaryService struct {
	mu        sync.Mutex
	summaries []AgentRunSummary
	maxKeep   int
}

// NewAgentSummaryService returns a service keeping at most maxKeep runs.
// A non-positive value is treated as 50.
func NewAgentSummaryService(maxKeep int) *AgentSummaryService {
	if maxKeep <= 0 {
		maxKeep = 50
	}
	return &AgentSummaryService{maxKeep: maxKeep}
}

// RecordRun saves a summary of an agent run, evicting the oldest entry once
// the buffer is full.
func (s *AgentSummaryService) RecordRun(summary AgentRunSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.summaries = append(s.summaries, summary)
	if len(s.summaries) > s.maxKeep {
		s.summaries = s.summaries[len(s.summaries)-s.maxKeep:]
	}
}

// Recent returns the N most recent summaries (newest last). If n <= 0 or
// larger than the buffer, all stored summaries are returned.
func (s *AgentSummaryService) Recent(n int) []AgentRunSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n <= 0 || n > len(s.summaries) {
		out := make([]AgentRunSummary, len(s.summaries))
		copy(out, s.summaries)
		return out
	}
	out := make([]AgentRunSummary, n)
	copy(out, s.summaries[len(s.summaries)-n:])
	return out
}

// Count returns the number of stored summaries.
func (s *AgentSummaryService) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.summaries)
}

// GenerateAgentSummary creates an AgentRunSummary from raw execution data.
// It is a thin builder that also derives a default Status when none is
// provided and condenses tool calls into key actions when KeyActions is empty.
func GenerateAgentSummary(
	agentName string,
	duration time.Duration,
	toolCalls []string,
	inputTokens, outputTokens int,
	cost float64,
	finalResult string,
) AgentRunSummary {
	status := "success"
	if strings.Contains(strings.ToLower(finalResult), "error") ||
		strings.Contains(strings.ToLower(finalResult), "failed") {
		status = "error"
	}

	actions := summariseToolCalls(toolCalls)

	return AgentRunSummary{
		AgentName:    agentName,
		StartTime:    time.Now().Add(-duration),
		Duration:     duration,
		ToolCalls:    len(toolCalls),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Cost:         cost,
		Status:       status,
		KeyActions:   actions,
		Result:       truncateLine(finalResult, 200),
	}
}

// summariseToolCalls collapses a flat tool-name list into 2-3 high-level
// action descriptions ("read 5 files", "ran 2 commands", ...).
func summariseToolCalls(calls []string) []string {
	if len(calls) == 0 {
		return nil
	}

	counts := make(map[string]int)
	order := make([]string, 0)
	for _, c := range calls {
		if _, ok := counts[c]; !ok {
			order = append(order, c)
		}
		counts[c]++
	}

	var actions []string
	for _, name := range order {
		count := counts[name]
		actions = append(actions, describeToolBatch(name, count))
	}
	return actions
}

func describeToolBatch(name string, count int) string {
	verb := "used"
	noun := name
	switch strings.ToLower(name) {
	case "read", "read_file":
		verb = "read"
		noun = pluralise("file", count)
	case "write", "file_write":
		verb = "wrote"
		noun = pluralise("file", count)
	case "edit", "file_edit":
		verb = "edited"
		noun = pluralise("file", count)
	case "bash", "run_command":
		verb = "ran"
		noun = pluralise("command", count)
	case "grep":
		verb = "ran"
		noun = pluralise("search", count)
	case "glob", "search_files":
		verb = "scanned"
		noun = pluralise("path glob", count)
	case "list_directory":
		verb = "listed"
		noun = pluralise("directory", count)
	}
	if count <= 1 {
		return fmt.Sprintf("%s %s", verb, noun)
	}
	return fmt.Sprintf("%s %d %s", verb, count, noun)
}

func pluralise(word string, count int) string {
	if count == 1 {
		return word
	}
	if strings.HasSuffix(word, "y") {
		return strings.TrimSuffix(word, "y") + "ies"
	}
	if strings.HasSuffix(word, "s") {
		return word
	}
	return word + "s"
}

// FormatAgentSummary renders a single AgentRunSummary as a tree-style block.
//
// Example:
//
//	task agent (1m 23s)
//	   tool calls: 12
//	   tokens: 4.2k in / 1.8k out ($0.0123)
//	   status: success
//	   actions: read 5 files, ran tests, found 3 bugs
func FormatAgentSummary(s AgentRunSummary) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s agent (%s)\n", s.AgentName, formatDuration(s.Duration))
	fmt.Fprintf(&b, "   |- %d tool calls\n", s.ToolCalls)
	fmt.Fprintf(&b, "   |- %s in / %s out tokens ($%.4f)\n",
		formatTokenCount(s.InputTokens),
		formatTokenCount(s.OutputTokens),
		s.Cost,
	)
	fmt.Fprintf(&b, "   |- Status: %s %s\n", statusGlyph(s.Status), s.Status)

	actions := s.KeyActions
	if len(actions) == 0 && s.Result != "" {
		actions = []string{s.Result}
	}
	if len(actions) > 0 {
		fmt.Fprintf(&b, "   `- Acoes: %s", strings.Join(actions, ", "))
	} else {
		// Strip the trailing newline from the previous line for a clean tail.
		out := strings.TrimRight(b.String(), "\n")
		return out
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatAgentSummaryList renders multiple summaries separated by blank lines.
func FormatAgentSummaryList(summaries []AgentRunSummary) string {
	if len(summaries) == 0 {
		return "(no agent runs recorded)"
	}
	parts := make([]string, 0, len(summaries))
	for _, s := range summaries {
		parts = append(parts, FormatAgentSummary(s))
	}
	return strings.Join(parts, "\n\n")
}

// --- helpers ---

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) - mins*60
	return fmt.Sprintf("%dm %ds", mins, secs)
}

func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func statusGlyph(status string) string {
	switch strings.ToLower(status) {
	case "success", "ok", "done":
		return "[ok]"
	case "error", "failed", "failure":
		return "[x]"
	case "cancelled", "canceled":
		return "[!]"
	default:
		return "[?]"
	}
}

func truncateLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
