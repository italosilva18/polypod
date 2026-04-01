package types

import "time"

// TaskType categorizes background tasks.
type TaskType string

const (
	TaskTypeLocalBash   TaskType = "local_bash"
	TaskTypeLocalAgent  TaskType = "local_agent"
	TaskTypeRemoteAgent TaskType = "remote_agent"
	TaskTypeTeammate    TaskType = "in_process_teammate"
	TaskTypeWorkflow    TaskType = "local_workflow"
	TaskTypeMonitorMCP  TaskType = "monitor_mcp"
)

// TaskStatus tracks the lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusKilled    TaskStatus = "killed"
)

// IsTerminal returns true if the task will not transition further.
func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusCompleted || s == TaskStatusFailed || s == TaskStatusKilled
}

// TaskStateBase holds common fields for all task states.
type TaskStateBase struct {
	ID            TaskID     `json:"id"`
	Type          TaskType   `json:"type"`
	Status        TaskStatus `json:"status"`
	Description   string     `json:"description"`
	ToolUseID     string     `json:"tool_use_id,omitempty"`
	StartTime     time.Time  `json:"start_time"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	TotalPausedMs int64      `json:"total_paused_ms,omitempty"`
	OutputFile    string     `json:"output_file"`
	OutputOffset  int        `json:"output_offset"`
	Notified      bool       `json:"notified"`
}

// NewTaskState creates a new task state in pending status.
func NewTaskState(taskType TaskType, description string, outputFile string) TaskStateBase {
	return TaskStateBase{
		ID:          NewTaskID(taskIDPrefix(taskType)),
		Type:        taskType,
		Status:      TaskStatusPending,
		Description: description,
		StartTime:   time.Now(),
		OutputFile:  outputFile,
	}
}

func taskIDPrefix(t TaskType) string {
	switch t {
	case TaskTypeLocalBash:
		return TaskPrefixBash
	case TaskTypeLocalAgent:
		return TaskPrefixAgent
	case TaskTypeRemoteAgent:
		return TaskPrefixRemote
	case TaskTypeTeammate:
		return TaskPrefixTeammate
	case TaskTypeWorkflow:
		return TaskPrefixWorkflow
	case TaskTypeMonitorMCP:
		return TaskPrefixMonitor
	default:
		return "x"
	}
}
