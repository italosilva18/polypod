// Package agentsummary generates structured summaries of agent execution.
//
// It tracks individual agent runs, tool calls, token usage, and success
// rates, then aggregates them into a Summary report.
package agentsummary

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// ToolCall records a single tool invocation during an agent run.
type ToolCall struct {
	Name     string            `json:"name"`
	Args     map[string]string `json:"args,omitempty"`
	Duration time.Duration     `json:"duration"`
	Success  bool              `json:"success"`
}

// AgentRun records the full lifecycle of one agent execution.
type AgentRun struct {
	AgentName       string     `json:"agent_name"`
	TaskDescription string     `json:"task_description"`
	StartTime       time.Time  `json:"start_time"`
	EndTime         time.Time  `json:"end_time"`
	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	TokensUsed      int        `json:"tokens_used"`
	Success         bool       `json:"success"`
	Error           string     `json:"error,omitempty"`
	Output          string     `json:"output,omitempty"`
}

// Duration returns the elapsed time of the run.
func (r *AgentRun) Duration() time.Duration {
	return r.EndTime.Sub(r.StartTime)
}

// Summary aggregates metrics across all tracked runs.
type Summary struct {
	TotalRuns     int           `json:"total_runs"`
	SuccessRate   float64       `json:"success_rate"`
	AvgDuration   time.Duration `json:"avg_duration"`
	MostUsedTools []string      `json:"most_used_tools,omitempty"`
	TotalTokens   int           `json:"total_tokens"`
	Runs          []AgentRun    `json:"runs,omitempty"`
}

// ---------------------------------------------------------------------------
// RunHandle — per-run recorder
// ---------------------------------------------------------------------------

// RunHandle is returned by Tracker.StartRun and allows the caller to record
// tool calls and mark the run as complete. It is safe for concurrent use.
type RunHandle struct {
	mu  sync.Mutex
	run AgentRun
	t   *Tracker
}

// RecordToolCall appends a tool invocation to the current run.
func (h *RunHandle) RecordToolCall(name string, args map[string]string, dur time.Duration, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.run.ToolCalls = append(h.run.ToolCalls, ToolCall{
		Name:     name,
		Args:     args,
		Duration: dur,
		Success:  ok,
	})
}

// Complete finalises the run, storing its output, token count, and optional
// error, then appends it to the parent Tracker.
func (h *RunHandle) Complete(output string, tokens int, err error) {
	h.mu.Lock()
	h.run.EndTime = time.Now()
	h.run.Output = output
	h.run.TokensUsed = tokens
	if err != nil {
		h.run.Success = false
		h.run.Error = err.Error()
	} else {
		h.run.Success = true
	}
	finished := h.run
	h.mu.Unlock()

	h.t.addRun(finished)
}

// ---------------------------------------------------------------------------
// Tracker — thread-safe run accumulator
// ---------------------------------------------------------------------------

// Tracker records agent executions and produces aggregated summaries.
type Tracker struct {
	mu   sync.Mutex
	runs []AgentRun
}

// NewTracker returns a ready-to-use Tracker.
func NewTracker() *Tracker {
	return &Tracker{}
}

// StartRun begins tracking a new agent execution and returns a RunHandle for
// recording tool calls and completion.
func (t *Tracker) StartRun(agent, task string) *RunHandle {
	return &RunHandle{
		run: AgentRun{
			AgentName:       agent,
			TaskDescription: task,
			StartTime:       time.Now(),
		},
		t: t,
	}
}

// addRun is called internally by RunHandle.Complete.
func (t *Tracker) addRun(r AgentRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs = append(t.runs, r)
}

// Summarize aggregates all recorded runs into a Summary.
func (t *Tracker) Summarize() *Summary {
	t.mu.Lock()
	runs := make([]AgentRun, len(t.runs))
	copy(runs, t.runs)
	t.mu.Unlock()

	s := &Summary{
		TotalRuns: len(runs),
		Runs:      runs,
	}
	if len(runs) == 0 {
		return s
	}

	var successes int
	var totalDur time.Duration
	toolFreq := make(map[string]int)

	for _, r := range runs {
		if r.Success {
			successes++
		}
		s.TotalTokens += r.TokensUsed
		totalDur += r.EndTime.Sub(r.StartTime)
		for _, tc := range r.ToolCalls {
			toolFreq[tc.Name]++
		}
	}

	s.SuccessRate = float64(successes) / float64(len(runs))
	s.AvgDuration = totalDur / time.Duration(len(runs))
	s.MostUsedTools = topTools(toolFreq, 5)

	return s
}

// topTools returns up to n tool names sorted by descending frequency.
func topTools(freq map[string]int, n int) []string {
	type kv struct {
		name  string
		count int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].name < items[j].name
	})
	out := make([]string, 0, n)
	for i := 0; i < len(items) && i < n; i++ {
		out = append(out, items[i].name)
	}
	return out
}

// FormatReport generates a human-readable markdown report in Portuguese.
func (t *Tracker) FormatReport() string {
	s := t.Summarize()

	var b strings.Builder
	b.WriteString("# Relatório de Execução dos Agentes\n\n")

	// Overview
	b.WriteString("## Visão Geral\n\n")
	fmt.Fprintf(&b, "| Métrica | Valor |\n")
	fmt.Fprintf(&b, "|---------|-------|\n")
	fmt.Fprintf(&b, "| Total de execuções | %d |\n", s.TotalRuns)
	fmt.Fprintf(&b, "| Taxa de sucesso | %.1f%% |\n", s.SuccessRate*100)
	fmt.Fprintf(&b, "| Duração média | %s |\n", formatDuration(s.AvgDuration))
	fmt.Fprintf(&b, "| Tokens consumidos | %d |\n", s.TotalTokens)

	if len(s.MostUsedTools) > 0 {
		fmt.Fprintf(&b, "| Ferramentas mais usadas | %s |\n", strings.Join(s.MostUsedTools, ", "))
	}
	b.WriteString("\n")

	// Per-run details
	if len(s.Runs) > 0 {
		b.WriteString("## Execuções\n\n")
		for i, r := range s.Runs {
			status := "✓"
			if !r.Success {
				status = "✗"
			}
			fmt.Fprintf(&b, "### %d. %s %s — %s\n\n", i+1, status, r.AgentName, r.TaskDescription)
			fmt.Fprintf(&b, "- **Início:** %s\n", r.StartTime.Format("15:04:05"))
			fmt.Fprintf(&b, "- **Duração:** %s\n", formatDuration(r.EndTime.Sub(r.StartTime)))
			fmt.Fprintf(&b, "- **Tokens:** %d\n", r.TokensUsed)

			if r.Error != "" {
				fmt.Fprintf(&b, "- **Erro:** %s\n", r.Error)
			}

			if len(r.ToolCalls) > 0 {
				b.WriteString("- **Chamadas de ferramentas:**\n")
				for _, tc := range r.ToolCalls {
					tcStatus := "✓"
					if !tc.Success {
						tcStatus = "✗"
					}
					fmt.Fprintf(&b, "  - %s `%s` (%s) %s\n", tcStatus, tc.Name, formatDuration(tc.Duration), formatArgs(tc.Args))
				}
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// Reset clears all recorded runs.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runs = nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatDuration renders a duration in a compact human-readable form.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
}

// formatArgs renders tool arguments compactly for report display.
func formatArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(args))
	for k, v := range args {
		if len(v) > 30 {
			v = v[:27] + "..."
		}
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, " ")
}
