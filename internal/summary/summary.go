// Package summary provides summarisation services for tool executions,
// agent conversations, and away-from-keyboard periods.
//
// Migrated from TypeScript: services/toolUseSummary/, services/AgentSummary/,
// and services/awaySummary.ts
package summary

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Tool use summary
// ---------------------------------------------------------------------------

// ToolInfo describes a single tool execution for summarisation.
type ToolInfo struct {
	Name   string `json:"name"`
	Input  any    `json:"input"`
	Output any    `json:"output"`
}

// ToolUseBatch holds a human-readable summary of a tool batch.
// Renamed from ToolUseSummary to avoid collision with the per-tool
// ToolUseSummary defined in tool.go.
type ToolUseBatch struct {
	Summary   string
	Tools     []ToolInfo
	CreatedAt time.Time
}

// GenerateToolUseSummary produces a short label for a batch of tool executions.
// The label is designed to fit in ~30 characters (like a git commit subject).
// If tools is empty, returns an empty string.
func GenerateToolUseSummary(tools []ToolInfo, lastAssistantText string) string {
	if len(tools) == 0 {
		return ""
	}

	// If there's a single tool, derive a direct summary.
	if len(tools) == 1 {
		return summariseSingleTool(tools[0])
	}

	// For multiple tools, combine into a compact summary.
	names := make(map[string]int)
	for _, t := range tools {
		names[t.Name]++
	}

	var parts []string
	for name, count := range names {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", name, count))
		} else {
			parts = append(parts, name)
		}
	}

	joined := strings.Join(parts, ", ")
	if len(joined) > 50 {
		joined = joined[:47] + "..."
	}
	return fmt.Sprintf("Ran %s", joined)
}

func summariseSingleTool(t ToolInfo) string {
	inputStr := truncateJSON(t.Input, 40)
	switch t.Name {
	case "Read":
		return fmt.Sprintf("Read %s", inputStr)
	case "Write", "Edit":
		return fmt.Sprintf("Edited %s", inputStr)
	case "Bash":
		return fmt.Sprintf("Ran %s", inputStr)
	case "Glob":
		return fmt.Sprintf("Searched files")
	case "Grep":
		return fmt.Sprintf("Searched content")
	default:
		return fmt.Sprintf("Used %s", t.Name)
	}
}

func truncateJSON(v any, maxLen int) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "[unable to serialize]"
	}
	s := string(data)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ---------------------------------------------------------------------------
// Agent summary
// ---------------------------------------------------------------------------

// AgentSummary tracks periodic background summaries of an agent's work.
type AgentSummary struct {
	mu              sync.Mutex
	taskID          string
	agentID         string
	previousSummary string
	interval        time.Duration
	stopped         bool
	stopCh          chan struct{}
}

// NewAgentSummary creates a summariser for a sub-agent.
func NewAgentSummary(taskID, agentID string) *AgentSummary {
	return &AgentSummary{
		taskID:   taskID,
		agentID:  agentID,
		interval: 30 * time.Second,
		stopCh:   make(chan struct{}, 1),
	}
}

// BuildSummaryPrompt generates the prompt sent to the model for summarisation.
func (a *AgentSummary) BuildSummaryPrompt() string {
	a.mu.Lock()
	prev := a.previousSummary
	a.mu.Unlock()

	prevLine := ""
	if prev != "" {
		prevLine = fmt.Sprintf("\nPrevious: %q — say something NEW.\n", prev)
	}

	return fmt.Sprintf(`Describe your most recent action in 3-5 words using present tense (-ing). Name the file or function, not the branch. Do not use tools.
%s
Good: "Reading runAgent.ts"
Good: "Fixing null check in validate.ts"
Good: "Running auth module tests"
Good: "Adding retry logic to fetchUser"

Bad (past tense): "Analyzed the branch diff"
Bad (too vague): "Investigating the issue"
Bad (too long): "Reviewing full branch diff and AgentTool integration"`, prevLine)
}

// SetSummary stores the latest summary text.
func (a *AgentSummary) SetSummary(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.previousSummary = text
}

// GetSummary returns the latest summary text.
func (a *AgentSummary) GetSummary() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.previousSummary
}

// Start begins periodic summarisation. The provided callback is invoked
// with the generated summary text each interval. It runs until Stop is called.
func (a *AgentSummary) Start(generateFn func(prompt string) (string, error)) {
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.mu.Lock()
				if a.stopped {
					a.mu.Unlock()
					return
				}
				a.mu.Unlock()

				prompt := a.BuildSummaryPrompt()
				text, err := generateFn(prompt)
				if err != nil {
					continue
				}
				text = strings.TrimSpace(text)
				if text != "" {
					a.SetSummary(text)
				}
			case <-a.stopCh:
				return
			}
		}
	}()
}

// Stop terminates the periodic summarisation goroutine.
func (a *AgentSummary) Stop() {
	a.mu.Lock()
	a.stopped = true
	a.mu.Unlock()
	select {
	case a.stopCh <- struct{}{}:
	default:
	}
}

// TaskID returns the task identifier.
func (a *AgentSummary) TaskID() string { return a.taskID }

// AgentID returns the agent identifier.
func (a *AgentSummary) AgentID() string { return a.agentID }

// ---------------------------------------------------------------------------
// Away summary
// ---------------------------------------------------------------------------

const recentMessageWindow = 30

// BuildAwaySummaryPrompt generates the system prompt used to produce an
// away summary. sessionMemory may be empty.
func BuildAwaySummaryPrompt(sessionMemory string) string {
	var memBlock string
	if sessionMemory != "" {
		memBlock = fmt.Sprintf("Session memory (broader context):\n%s\n\n", sessionMemory)
	}
	return fmt.Sprintf(
		"%sThe user stepped away and is coming back. Write exactly 1-3 short sentences. "+
			"Start by stating the high-level task — what they are building or debugging, "+
			"not implementation details. Next: the concrete next step. Skip status reports "+
			"and commit recaps.",
		memBlock,
	)
}

// AwaySummaryInput collects the data needed to generate an away summary.
type AwaySummaryInput struct {
	// Messages is the full conversation; only the most recent window is used.
	Messages []string
	// SessionMemory is optional broader context.
	SessionMemory string
}

// TrimMessagesForAway returns the most recent messages up to the window size.
func TrimMessagesForAway(messages []string) []string {
	if len(messages) <= recentMessageWindow {
		return messages
	}
	return messages[len(messages)-recentMessageWindow:]
}

// ---------------------------------------------------------------------------
// FormatSummary renders all summary types into human-readable output.
// ---------------------------------------------------------------------------

// FormatSummary produces a compact, human-readable rendering.
func FormatSummary(tool *ToolUseBatch, agent *AgentSummary, away string) string {
	var b strings.Builder

	if tool != nil && tool.Summary != "" {
		fmt.Fprintf(&b, "Tools: %s\n", tool.Summary)
	}
	if agent != nil {
		if s := agent.GetSummary(); s != "" {
			fmt.Fprintf(&b, "Agent [%s]: %s\n", agent.TaskID(), s)
		}
	}
	if away != "" {
		fmt.Fprintf(&b, "While you were away: %s\n", away)
	}

	return strings.TrimRight(b.String(), "\n")
}
