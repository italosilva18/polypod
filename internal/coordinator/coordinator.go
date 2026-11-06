package coordinator

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- Mode Detection ---

// IsCoordinatorMode returns true if coordinator mode is active.
func IsCoordinatorMode() bool {
	return isEnvTruthy(os.Getenv("POLYPOD_COORDINATOR_MODE"))
}

// --- Session Mode Matching ---

// MatchSessionMode ensures the current mode matches the session's stored mode.
// Returns a warning message if the mode was switched, or "" if no switch needed.
func MatchSessionMode(sessionMode string) string {
	if sessionMode == "" {
		return ""
	}

	currentIsCoordinator := IsCoordinatorMode()
	sessionIsCoordinator := sessionMode == "coordinator"

	if currentIsCoordinator == sessionIsCoordinator {
		return ""
	}

	// Flip the env var
	if sessionIsCoordinator {
		os.Setenv("POLYPOD_COORDINATOR_MODE", "1")
	} else {
		os.Unsetenv("POLYPOD_COORDINATOR_MODE")
	}

	if sessionIsCoordinator {
		return "Entered coordinator mode to match resumed session."
	}
	return "Exited coordinator mode to match resumed session."
}

// --- Worker State ---

// WorkerStatus tracks the current status of a worker.
type WorkerStatus string

const (
	WorkerStatusRunning   WorkerStatus = "running"
	WorkerStatusCompleted WorkerStatus = "completed"
	WorkerStatusFailed    WorkerStatus = "failed"
	WorkerStatusStopped   WorkerStatus = "stopped"
)

// Worker represents a spawned worker agent.
type Worker struct {
	ID          string
	Description string
	Status      WorkerStatus
	StartedAt   time.Time
	FinishedAt  *time.Time
	Result      string
	Error       string
	ToolUses    int
	TotalTokens int
	DurationMs  int64
}

// TeamState tracks all active workers and shared context.
type TeamState struct {
	mu            sync.RWMutex
	workers       map[string]*Worker
	scratchpadDir string
}

// NewTeamState creates a new team state tracker.
func NewTeamState() *TeamState {
	return &TeamState{
		workers: make(map[string]*Worker),
	}
}

// SetScratchpadDir sets the scratchpad directory for cross-worker knowledge.
func (ts *TeamState) SetScratchpadDir(dir string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.scratchpadDir = dir
}

// ScratchpadDir returns the scratchpad directory.
func (ts *TeamState) ScratchpadDir() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.scratchpadDir
}

// AddWorker registers a new worker.
func (ts *TeamState) AddWorker(id, description string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.workers[id] = &Worker{
		ID:          id,
		Description: description,
		Status:      WorkerStatusRunning,
		StartedAt:   time.Now(),
	}
}

// UpdateWorkerStatus updates a worker's status and result.
func (ts *TeamState) UpdateWorkerStatus(id string, status WorkerStatus, result, errMsg string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	w, ok := ts.workers[id]
	if !ok {
		return
	}
	w.Status = status
	w.Result = result
	w.Error = errMsg
	now := time.Now()
	w.FinishedAt = &now
	w.DurationMs = now.Sub(w.StartedAt).Milliseconds()
}

// UpdateWorkerUsage updates token/tool usage.
func (ts *TeamState) UpdateWorkerUsage(id string, totalTokens, toolUses int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	w, ok := ts.workers[id]
	if !ok {
		return
	}
	w.TotalTokens = totalTokens
	w.ToolUses = toolUses
}

// GetWorker returns a worker by ID.
func (ts *TeamState) GetWorker(id string) *Worker {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	w, ok := ts.workers[id]
	if !ok {
		return nil
	}
	// Return a copy
	copy := *w
	return &copy
}

// RunningWorkers returns all workers currently running.
func (ts *TeamState) RunningWorkers() []*Worker {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	var result []*Worker
	for _, w := range ts.workers {
		if w.Status == WorkerStatusRunning {
			copy := *w
			result = append(result, &copy)
		}
	}
	return result
}

// AllWorkers returns all tracked workers.
func (ts *TeamState) AllWorkers() []*Worker {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	result := make([]*Worker, 0, len(ts.workers))
	for _, w := range ts.workers {
		copy := *w
		result = append(result, &copy)
	}
	return result
}

// RemoveWorker removes a worker from tracking.
func (ts *TeamState) RemoveWorker(id string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.workers, id)
}

// --- Coordinator Mode ---

// CoordinatorMode manages the coordinator-worker orchestration.
type CoordinatorMode struct {
	mu     sync.Mutex
	active bool
	team   *TeamState
	// Tool names the coordinator exposes
	agentToolName    string
	sendMsgToolName  string
	taskStopToolName string
}

// NewCoordinatorMode creates the coordinator mode handler.
func NewCoordinatorMode() *CoordinatorMode {
	return &CoordinatorMode{
		team:             NewTeamState(),
		agentToolName:    "agent_run",
		sendMsgToolName:  "send_message",
		taskStopToolName: "task_stop",
	}
}

// IsActive returns whether coordinator mode is currently active.
func (cm *CoordinatorMode) IsActive() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.active || IsCoordinatorMode()
}

// Activate enables coordinator mode.
func (cm *CoordinatorMode) Activate() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.active = true
	os.Setenv("POLYPOD_COORDINATOR_MODE", "1")
}

// Deactivate disables coordinator mode.
func (cm *CoordinatorMode) Deactivate() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.active = false
	os.Unsetenv("POLYPOD_COORDINATOR_MODE")
}

// Team returns the team state.
func (cm *CoordinatorMode) Team() *TeamState {
	return cm.team
}

// TransformSystemPrompt returns the coordinator system prompt.
// This replaces/augments the normal system prompt when in coordinator mode.
func (cm *CoordinatorMode) TransformSystemPrompt(workerTools []string, mcpServers []string, scratchpadDir string) string {
	sort.Strings(workerTools)
	workerCapStr := "Workers have access to standard tools, MCP tools from configured MCP servers, and project skills via the Skill tool. Delegate skill invocations to workers."

	if len(workerTools) <= 3 {
		workerCapStr = fmt.Sprintf("Workers have access to these tools: %s, plus MCP tools from configured MCP servers.",
			strings.Join(workerTools, ", "))
	}

	prompt := fmt.Sprintf(`You are Polypod, an AI assistant that orchestrates software engineering tasks across multiple workers.

## 1. Your Role

You are a **coordinator**. Your job is to:
- Help the user achieve their goal
- Direct workers to research, implement and verify code changes
- Synthesize results and communicate with the user
- Answer questions directly when possible — don't delegate work that you can handle without tools

Every message you send is to the user. Worker results and system notifications are internal signals — never thank or acknowledge them. Summarize new information for the user as it arrives.

## 2. Your Tools

- **%s** - Spawn a new worker
- **%s** - Continue an existing worker (send a follow-up)
- **%s** - Stop a running worker

When calling %s:
- Do not use one worker to check on another. Workers will notify you when they are done.
- Do not use workers to trivially report file contents or run commands. Give them higher-level tasks.
- Continue workers whose work is complete via %s to take advantage of their loaded context.
- After launching agents, briefly tell the user what you launched and end your response.

### Worker Results

Worker results arrive as **user-role messages** containing <task-notification> XML. Distinguish them by the <task-notification> opening tag.

## 3. Workers

%s

## 4. Task Workflow

### Phases

| Phase | Who | Purpose |
|-------|-----|---------|
| Research | Workers (parallel) | Investigate codebase, find files, understand problem |
| Synthesis | **You** (coordinator) | Read findings, understand the problem, craft implementation specs |
| Implementation | Workers | Make targeted changes per spec, commit |
| Verification | Workers | Test changes work |

### Concurrency

**Parallelism is your superpower. Launch independent workers concurrently whenever possible.**

- **Read-only tasks** (research) — run in parallel freely
- **Write-heavy tasks** (implementation) — one at a time per set of files
- **Verification** can sometimes run alongside implementation on different file areas

## 5. Writing Worker Prompts

**Workers can't see your conversation.** Every prompt must be self-contained.

When workers report research findings, **you must understand them before directing follow-up work**. Read the findings. Identify the approach. Then write a prompt with specific file paths, line numbers, and exactly what to change.

Never write "based on your findings" — synthesize the findings yourself.`,
		cm.agentToolName, cm.sendMsgToolName, cm.taskStopToolName,
		cm.agentToolName, cm.sendMsgToolName,
		workerCapStr)

	// Add MCP server info
	if len(mcpServers) > 0 {
		prompt += fmt.Sprintf("\n\nWorkers also have access to MCP tools from: %s", strings.Join(mcpServers, ", "))
	}

	// Add scratchpad info
	if scratchpadDir != "" {
		prompt += fmt.Sprintf("\n\nScratchpad directory: %s\nWorkers can read and write here without permission prompts. Use this for durable cross-worker knowledge.", scratchpadDir)
	}

	return prompt
}

// FilterTools returns the set of tools available to the coordinator.
// The coordinator gets team management tools instead of direct file editing tools.
func (cm *CoordinatorMode) FilterTools(allTools []string) []string {
	// Coordinator always has access to these
	coordinatorTools := map[string]bool{
		cm.agentToolName:    true,
		cm.sendMsgToolName:  true,
		cm.taskStopToolName: true,
	}

	var result []string
	for _, t := range allTools {
		if coordinatorTools[t] {
			result = append(result, t)
		}
	}
	return result
}

// AugmentWithTeamStatus injects current team state into the prompt.
func (cm *CoordinatorMode) AugmentWithTeamStatus() string {
	workers := cm.team.AllWorkers()
	if len(workers) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Current Team Status\n\n")

	running := 0
	completed := 0
	failed := 0

	for _, w := range workers {
		switch w.Status {
		case WorkerStatusRunning:
			running++
		case WorkerStatusCompleted:
			completed++
		case WorkerStatusFailed:
			failed++
		}
	}

	sb.WriteString(fmt.Sprintf("Workers: %d running, %d completed, %d failed\n\n",
		running, completed, failed))

	for _, w := range workers {
		status := string(w.Status)
		duration := ""
		if w.FinishedAt != nil {
			duration = fmt.Sprintf(" (%dms)", w.DurationMs)
		} else {
			elapsed := time.Since(w.StartedAt).Milliseconds()
			duration = fmt.Sprintf(" (running %dms)", elapsed)
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s%s\n", w.ID, w.Description, status, duration))
	}

	return sb.String()
}

// DelegateTask creates a worker for a specific task and returns its ID.
func (cm *CoordinatorMode) DelegateTask(description, prompt string) string {
	id := fmt.Sprintf("worker-%d", time.Now().UnixNano()%100000)
	cm.team.AddWorker(id, description)
	// The actual agent spawning is handled by the agent_run tool.
	// We just track it here.
	return id
}

// CollectResults gathers outputs from all completed workers.
func (cm *CoordinatorMode) CollectResults() []WorkerResult {
	workers := cm.team.AllWorkers()
	var results []WorkerResult
	for _, w := range workers {
		if w.Status == WorkerStatusCompleted || w.Status == WorkerStatusFailed {
			results = append(results, WorkerResult{
				WorkerID:    w.ID,
				Description: w.Description,
				Status:      w.Status,
				Result:      w.Result,
				Error:       w.Error,
				DurationMs:  w.DurationMs,
				TotalTokens: w.TotalTokens,
				ToolUses:    w.ToolUses,
			})
		}
	}
	return results
}

// WorkerResult is a collected output from a completed worker.
type WorkerResult struct {
	WorkerID    string
	Description string
	Status      WorkerStatus
	Result      string
	Error       string
	DurationMs  int64
	TotalTokens int
	ToolUses    int
}

// FormatWorkerResult formats a worker result as a task-notification XML block.
func FormatWorkerResult(r WorkerResult) string {
	status := string(r.Status)
	summary := fmt.Sprintf("Agent %q %s", r.Description, status)
	if r.Error != "" {
		summary = fmt.Sprintf("Agent %q failed: %s", r.Description, r.Error)
	}

	var sb strings.Builder
	sb.WriteString("<task-notification>\n")
	fmt.Fprintf(&sb, "<task-id>%s</task-id>\n", r.WorkerID)
	fmt.Fprintf(&sb, "<status>%s</status>\n", status)
	fmt.Fprintf(&sb, "<summary>%s</summary>\n", summary)
	if r.Result != "" {
		fmt.Fprintf(&sb, "<result>%s</result>\n", r.Result)
	}
	fmt.Fprintf(&sb, "<usage>\n")
	fmt.Fprintf(&sb, "  <total_tokens>%d</total_tokens>\n", r.TotalTokens)
	fmt.Fprintf(&sb, "  <tool_uses>%d</tool_uses>\n", r.ToolUses)
	fmt.Fprintf(&sb, "  <duration_ms>%d</duration_ms>\n", r.DurationMs)
	fmt.Fprintf(&sb, "</usage>\n")
	sb.WriteString("</task-notification>")
	return sb.String()
}

// --- User Context ---

// GetCoordinatorUserContext returns additional context to inject into the system prompt
// when in coordinator mode. Returns nil if not in coordinator mode.
func GetCoordinatorUserContext(workerTools []string, mcpClients []string, scratchpadDir string) map[string]string {
	if !IsCoordinatorMode() {
		return nil
	}

	// Internal-only tools that workers use internally
	internalTools := map[string]bool{
		"team_create":      true,
		"team_delete":      true,
		"send_message":     true,
		"synthetic_output": true,
	}

	// Filter to visible worker tools
	var visibleTools []string
	for _, t := range workerTools {
		if !internalTools[t] {
			visibleTools = append(visibleTools, t)
		}
	}
	sort.Strings(visibleTools)

	content := fmt.Sprintf("Workers spawned via the agent_run tool have access to these tools: %s",
		strings.Join(visibleTools, ", "))

	if len(mcpClients) > 0 {
		content += fmt.Sprintf("\n\nWorkers also have access to MCP tools from connected MCP servers: %s",
			strings.Join(mcpClients, ", "))
	}

	if scratchpadDir != "" {
		content += fmt.Sprintf("\n\nScratchpad directory: %s\nWorkers can read and write here without permission prompts. Use this for durable cross-worker knowledge — structure files however fits the work.",
			scratchpadDir)
	}

	return map[string]string{
		"workerToolsContext": content,
	}
}

// --- Helpers ---

func isEnvTruthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
