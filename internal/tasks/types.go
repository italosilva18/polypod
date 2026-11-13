package tasks

import (
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"time"
)

// Task type definitions migrated from Claude Code TypeScript source.
// Source: Task.ts, tasks.ts, tasks/types.ts

// ---------------------------------------------------------------------------
// TaskType enum
// ---------------------------------------------------------------------------

// TaskType categorizes background tasks.
type TaskType string

const (
	TaskTypeShell    TaskType = "local_bash"
	TaskTypeAgent    TaskType = "local_agent"
	TaskTypeRemote   TaskType = "remote_agent"
	TaskTypeTeammate TaskType = "in_process_teammate"
	TaskTypeWorkflow TaskType = "local_workflow"
	TaskTypeMonitor  TaskType = "monitor_mcp"
	TaskTypeDream    TaskType = "dream"
	// TaskTypeCLITurn observes a detached live CLI turn. It is never restarted
	// from its prompt because the live model/tool transcript is not replayable.
	TaskTypeCLITurn TaskType = "cli_turn"
)

// ---------------------------------------------------------------------------
// TaskStatus enum with lifecycle
// ---------------------------------------------------------------------------

// TaskStatus tracks the lifecycle state of a task.
type TaskStatus string

const (
	StatusPending     TaskStatus = "pending"
	StatusRunning     TaskStatus = "running"
	StatusCompleted   TaskStatus = "completed"
	StatusFailed      TaskStatus = "failed"
	StatusKilled      TaskStatus = "killed"
	StatusInterrupted TaskStatus = "interrupted"
)

// IsTerminal returns true if the task will not transition further.
func (s TaskStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusKilled || s == StatusInterrupted
}

// ---------------------------------------------------------------------------
// TaskConfig per type
// ---------------------------------------------------------------------------

// TaskConfig holds per-task-type configuration.
type TaskConfig struct {
	// MaxConcurrent is the max number of concurrent tasks of this type.
	MaxConcurrent int
	// DefaultTimeout is the default timeout for tasks of this type.
	DefaultTimeout time.Duration
	// CanBackground is true if tasks of this type can be backgrounded.
	CanBackground bool
}

// DefaultTaskConfigs maps task types to their default configurations.
var DefaultTaskConfigs = map[TaskType]TaskConfig{
	TaskTypeShell:    {MaxConcurrent: 10, DefaultTimeout: 0, CanBackground: true},
	TaskTypeAgent:    {MaxConcurrent: 20, DefaultTimeout: 0, CanBackground: true},
	TaskTypeRemote:   {MaxConcurrent: 5, DefaultTimeout: 30 * time.Minute, CanBackground: true},
	TaskTypeTeammate: {MaxConcurrent: 10, DefaultTimeout: 0, CanBackground: true},
	TaskTypeWorkflow: {MaxConcurrent: 5, DefaultTimeout: 0, CanBackground: true},
	TaskTypeMonitor:  {MaxConcurrent: 10, DefaultTimeout: 0, CanBackground: true},
	TaskTypeDream:    {MaxConcurrent: 1, DefaultTimeout: 10 * time.Minute, CanBackground: true},
	TaskTypeCLITurn:  {MaxConcurrent: 1, DefaultTimeout: 0, CanBackground: true},
}

// ---------------------------------------------------------------------------
// TaskID generation
// ---------------------------------------------------------------------------

// Task ID prefixes per type.
var taskIDPrefixes = map[TaskType]string{
	TaskTypeShell:    "b",
	TaskTypeAgent:    "a",
	TaskTypeRemote:   "r",
	TaskTypeTeammate: "t",
	TaskTypeWorkflow: "w",
	TaskTypeMonitor:  "m",
	TaskTypeDream:    "d",
	TaskTypeCLITurn:  "c",
}

const taskIDAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// GenerateTaskID creates a new unique task ID with a type prefix.
func GenerateTaskID(taskType TaskType) string {
	prefix := taskIDPrefixes[taskType]
	if prefix == "" {
		prefix = "x"
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := prefix
	for i := 0; i < 8; i++ {
		id += string(taskIDAlphabet[b[i]%byte(len(taskIDAlphabet))])
	}
	return id
}

// ---------------------------------------------------------------------------
// TaskHandle interface
// ---------------------------------------------------------------------------

// TaskHandle is a reference to a running task with optional cleanup.
type TaskHandle struct {
	TaskID  string
	Cleanup func()
}

// ---------------------------------------------------------------------------
// TaskState — base fields shared by all task states
// ---------------------------------------------------------------------------

// TaskState holds the common fields for all task states.
type TaskState struct {
	ID           string           `json:"id"`
	Type         TaskType         `json:"type"`
	Status       TaskStatus       `json:"status"`
	Description  string           `json:"description"`
	ToolUseID    string           `json:"tool_use_id,omitempty"`
	StartTime    time.Time        `json:"start_time"`
	EndTime      *time.Time       `json:"end_time,omitempty"`
	OutputFile   string           `json:"output_file"`
	OutputOffset int              `json:"output_offset"`
	Notified     bool             `json:"notified"`
	AgentName    string           `json:"agent_name,omitempty"`
	Workspace    string           `json:"workspace,omitempty"`
	CostUSD      *float64         `json:"cost_usd,omitempty"`
	Progress     *AgentProgress   `json:"progress,omitempty"`
	Checkpoint   *AgentCheckpoint `json:"checkpoint,omitempty"`
	Result       string           `json:"result,omitempty"`
	Error        string           `json:"error,omitempty"`
	ExitCode     *int             `json:"exit_code,omitempty"`
	ProcessID    int              `json:"process_id,omitempty"`
	Attempt      int              `json:"attempt,omitempty"`
	RestartOf    string           `json:"restart_of,omitempty"`
	// Input is written only to the private task state file for explicit restarts.
	input string
}

// NewTaskState creates a new task state in pending status.
func NewTaskState(taskType TaskType, description string) *TaskState {
	return &TaskState{
		ID:          GenerateTaskID(taskType),
		Type:        taskType,
		Status:      StatusPending,
		Description: description,
		StartTime:   time.Now(),
		OutputFile:  fmt.Sprintf("/tmp/polypod_task_%s.log", GenerateTaskID(taskType)),
		Attempt:     1,
	}
}

// ---------------------------------------------------------------------------
// TaskKiller interface — what task implementations must provide
// ---------------------------------------------------------------------------

// TaskKiller is implemented by each concrete task type to handle kill.
type TaskKiller interface {
	Name() string
	Type() TaskType
	Kill(taskID string, registry *TaskRegistry) error
}

// ---------------------------------------------------------------------------
// TaskRegistry — centralized task state management
// ---------------------------------------------------------------------------

// TaskRegistry manages all task states.
type TaskRegistry struct {
	mu          sync.RWMutex
	tasks       map[string]*TaskState
	killers     map[TaskType]TaskKiller
	agentRunner AgentRunFunc
	persistPath string
	outputDir   string
	persistErr  error
	recoveryErr error
	lockFile    *os.File
	// subscribers is guarded by mu; each subscriber owns an independent queue.
	subscribers map[*taskSubscriber]struct{}
}

// NewTaskRegistry creates a new task registry.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{
		tasks:       make(map[string]*TaskState),
		killers:     make(map[TaskType]TaskKiller),
		subscribers: make(map[*taskSubscriber]struct{}),
	}
}

// RegisterKiller registers a TaskKiller for a task type.
func (r *TaskRegistry) RegisterKiller(k TaskKiller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killers[k.Type()] = k
}

// Register adds a task state to the registry.
func (r *TaskRegistry) Register(state *TaskState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[state.ID] = state
	r.persistLocked()
	if state.Status.IsTerminal() {
		r.publishTerminalLocked(state)
	}
}

func snapshotTaskState(state *TaskState) *TaskState {
	snapshot := *state
	if state.EndTime != nil {
		end := *state.EndTime
		snapshot.EndTime = &end
	}
	if state.ExitCode != nil {
		exitCode := *state.ExitCode
		snapshot.ExitCode = &exitCode
	}
	if state.CostUSD != nil {
		cost := *state.CostUSD
		snapshot.CostUSD = &cost
	}
	if state.Progress != nil {
		progress := *state.Progress
		if state.Progress.CostUSD != nil {
			cost := *state.Progress.CostUSD
			progress.CostUSD = &cost
		}
		progress.RecentActivities = append([]ToolActivity(nil), progress.RecentActivities...)
		snapshot.Progress = &progress
	}
	snapshot.Checkpoint = cloneAgentCheckpoint(state.Checkpoint)
	return &snapshot
}

// Get returns a task state snapshot by ID.
func (r *TaskRegistry) Get(id string) (*TaskState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	if !ok {
		return nil, false
	}
	return snapshotTaskState(t), true
}

// Update atomically updates a task state.
func (r *TaskRegistry) Update(id string, fn func(*TaskState)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	if !ok {
		return false
	}
	previousStatus := t.Status
	fn(t)
	r.persistLocked()
	if t.Status.IsTerminal() && t.Status != previousStatus {
		r.publishTerminalLocked(t)
	}
	return true
}

// List returns all tasks.
func (r *TaskRegistry) List() []*TaskState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*TaskState, 0, len(r.tasks))
	for _, t := range r.tasks {
		result = append(result, snapshotTaskState(t))
	}
	return result
}

// ListByStatus returns tasks filtered by status.
func (r *TaskRegistry) ListByStatus(status TaskStatus) []*TaskState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*TaskState
	for _, t := range r.tasks {
		if t.Status == status {
			result = append(result, snapshotTaskState(t))
		}
	}
	return result
}

// ListByType returns tasks filtered by type.
func (r *TaskRegistry) ListByType(taskType TaskType) []*TaskState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*TaskState
	for _, t := range r.tasks {
		if t.Type == taskType {
			result = append(result, snapshotTaskState(t))
		}
	}
	return result
}

// Kill stops a task by ID using the registered killer for its type.
func (r *TaskRegistry) Kill(id string) error {
	r.mu.RLock()
	t, ok := r.tasks[id]
	if !ok {
		r.mu.RUnlock()
		return fmt.Errorf("task %s not found", id)
	}
	if t.Status.IsTerminal() {
		r.mu.RUnlock()
		return fmt.Errorf("task %s already in terminal state: %s", id, t.Status)
	}
	killer, hasKiller := r.killers[t.Type]
	r.mu.RUnlock()

	if !hasKiller {
		return fmt.Errorf("no killer registered for task type: %s", t.Type)
	}
	return killer.Kill(id, r)
}

// Remove deletes a task from the registry.
func (r *TaskRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	r.persistLocked()
}

// RunningCount returns the number of running tasks.
func (r *TaskRegistry) RunningCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, t := range r.tasks {
		if t.Status == StatusRunning {
			count++
		}
	}
	return count
}

// IsBackgroundTask returns true if a task should be shown in the background
// tasks indicator (running or pending).
func IsBackgroundTask(t *TaskState) bool {
	return t.Status == StatusRunning || t.Status == StatusPending
}
