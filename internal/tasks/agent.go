package tasks

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Agent task migrated from Claude Code TypeScript source.
// Source: tasks/LocalAgentTask/LocalAgentTask.tsx, tasks/RemoteAgentTask/RemoteAgentTask.tsx

// ---------------------------------------------------------------------------
// AgentProgress — token budget & tool use tracking
// ---------------------------------------------------------------------------

// ToolActivity records a single tool use by an agent.
type ToolActivity struct {
	ToolName            string         `json:"tool_name"`
	Input               map[string]any `json:"input,omitempty"`
	ActivityDescription string         `json:"activity_description,omitempty"`
	IsSearch            bool           `json:"is_search,omitempty"`
	IsRead              bool           `json:"is_read,omitempty"`
}

// AgentProgress tracks cumulative agent progress.
type AgentProgress struct {
	ToolUseCount     int            `json:"tool_use_count"`
	TokenCount       int            `json:"token_count"`
	LastActivity     *ToolActivity  `json:"last_activity,omitempty"`
	RecentActivities []ToolActivity `json:"recent_activities,omitempty"`
	Summary          string         `json:"summary,omitempty"`
}

// ProgressTracker tracks per-agent progress metrics.
type ProgressTracker struct {
	ToolUseCount           int
	LatestInputTokens      int
	CumulativeOutputTokens int
	RecentActivities       []ToolActivity
	maxRecent              int
}

// NewProgressTracker creates a new progress tracker.
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{maxRecent: 5}
}

// RecordToolUse records a tool use event.
func (pt *ProgressTracker) RecordToolUse(activity ToolActivity) {
	pt.ToolUseCount++
	pt.RecentActivities = append(pt.RecentActivities, activity)
	if len(pt.RecentActivities) > pt.maxRecent {
		pt.RecentActivities = pt.RecentActivities[1:]
	}
}

// RecordTokens updates token counts from API usage.
func (pt *ProgressTracker) RecordTokens(inputTokens, outputTokens int) {
	pt.LatestInputTokens = inputTokens
	pt.CumulativeOutputTokens += outputTokens
}

// TotalTokens returns the total token count estimate.
func (pt *ProgressTracker) TotalTokens() int {
	return pt.LatestInputTokens + pt.CumulativeOutputTokens
}

// GetProgress returns the current progress snapshot.
func (pt *ProgressTracker) GetProgress() AgentProgress {
	recent := make([]ToolActivity, len(pt.RecentActivities))
	copy(recent, pt.RecentActivities)

	var last *ToolActivity
	if len(recent) > 0 {
		last = &recent[len(recent)-1]
	}

	return AgentProgress{
		ToolUseCount:     pt.ToolUseCount,
		TokenCount:       pt.TotalTokens(),
		LastActivity:     last,
		RecentActivities: recent,
	}
}

// ---------------------------------------------------------------------------
// LocalAgentTask — run agent as background task
// ---------------------------------------------------------------------------

// LocalAgentState extends TaskState with agent-specific fields.
type LocalAgentState struct {
	AgentID         string         `json:"agent_id"`
	Prompt          string         `json:"prompt"`
	AgentType       string         `json:"agent_type"`
	Model           string         `json:"model,omitempty"`
	Error           string         `json:"error,omitempty"`
	Result          string         `json:"result,omitempty"`
	Progress        *AgentProgress `json:"progress,omitempty"`
	IsBackgrounded  bool           `json:"is_backgrounded"`
	PendingMessages []string       `json:"pending_messages,omitempty"`
}

var (
	agentStates   = make(map[string]*LocalAgentState)
	agentStatesMu sync.Mutex
)

func setAgentState(taskID string, state *LocalAgentState) {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	agentStates[taskID] = state
}

func getAgentState(taskID string) *LocalAgentState {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	return agentStates[taskID]
}

func removeAgentState(taskID string) {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	delete(agentStates, taskID)
}

// LocalAgentTaskKiller implements TaskKiller for local agent tasks.
type LocalAgentTaskKiller struct {
	logger *slog.Logger
}

// NewLocalAgentTaskKiller creates a killer for local agent tasks.
func NewLocalAgentTaskKiller(logger *slog.Logger) *LocalAgentTaskKiller {
	return &LocalAgentTaskKiller{logger: logger}
}

func (k *LocalAgentTaskKiller) Name() string   { return "LocalAgentTask" }
func (k *LocalAgentTaskKiller) Type() TaskType { return TaskTypeAgent }

// Kill aborts a running local agent task.
func (k *LocalAgentTaskKiller) Kill(taskID string, registry *TaskRegistry) error {
	state, ok := registry.Get(taskID)
	if !ok {
		return fmt.Errorf("agent task %s not found", taskID)
	}
	if state.Status != StatusRunning {
		return nil
	}

	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusKilled
		t.EndTime = &now
		t.Notified = true
	})

	k.logger.Info("agent task killed", "task_id", taskID)
	return nil
}

// RegisterLocalAgent registers a new local agent task.
func RegisterLocalAgent(
	registry *TaskRegistry,
	agentID string,
	description string,
	prompt string,
	agentType string,
	toolUseID string,
	logger *slog.Logger,
) *TaskState {
	state := NewTaskState(TaskTypeAgent, description)
	state.ID = agentID // Use agentID as taskID for consistency
	state.ToolUseID = toolUseID
	state.Status = StatusRunning

	agentState := &LocalAgentState{
		AgentID:        agentID,
		Prompt:         prompt,
		AgentType:      agentType,
		IsBackgrounded: true,
	}
	setAgentState(agentID, agentState)

	registry.Register(state)

	logger.Info("local agent task registered",
		"agent_id", agentID,
		"agent_type", agentType,
		"description", description,
	)
	return state
}

// CompleteAgent marks an agent task as completed with result.
func CompleteAgent(registry *TaskRegistry, agentID string, result string) {
	now := time.Now()
	agentState := getAgentState(agentID)
	if agentState != nil {
		agentState.Result = result
	}
	registry.Update(agentID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusCompleted
		t.EndTime = &now
	})
}

// FailAgent marks an agent task as failed with error.
func FailAgent(registry *TaskRegistry, agentID string, errMsg string) {
	now := time.Now()
	agentState := getAgentState(agentID)
	if agentState != nil {
		agentState.Error = errMsg
	}
	registry.Update(agentID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusFailed
		t.EndTime = &now
	})
}

// UpdateAgentProgress updates progress metrics for an agent task.
func UpdateAgentProgress(registry *TaskRegistry, agentID string, progress AgentProgress) {
	agentState := getAgentState(agentID)
	if agentState == nil {
		return
	}
	agentState.Progress = &progress
}

// QueuePendingMessage adds a message to an agent's pending queue.
func QueuePendingMessage(agentID string, msg string) {
	agentState := getAgentState(agentID)
	if agentState == nil {
		return
	}
	agentState.PendingMessages = append(agentState.PendingMessages, msg)
}

// DrainPendingMessages returns and clears all pending messages for an agent.
func DrainPendingMessages(agentID string) []string {
	agentState := getAgentState(agentID)
	if agentState == nil || len(agentState.PendingMessages) == 0 {
		return nil
	}
	msgs := agentState.PendingMessages
	agentState.PendingMessages = nil
	return msgs
}

// ---------------------------------------------------------------------------
// RemoteAgentTask — run agent on remote machine
// ---------------------------------------------------------------------------

// RemoteTaskType categorizes remote task variants.
type RemoteTaskType string

const (
	RemoteTaskAgent        RemoteTaskType = "remote-agent"
	RemoteTaskUltraplan    RemoteTaskType = "ultraplan"
	RemoteTaskUltrareview  RemoteTaskType = "ultrareview"
	RemoteTaskAutofixPR    RemoteTaskType = "autofix-pr"
	RemoteTaskBackgroundPR RemoteTaskType = "background-pr"
)

// RemoteAgentState extends TaskState with remote-specific fields.
type RemoteAgentState struct {
	RemoteTaskType RemoteTaskType `json:"remote_task_type"`
	SessionID      string         `json:"session_id"`
	Command        string         `json:"command"`
	Title          string         `json:"title"`
	IsLongRunning  bool           `json:"is_long_running,omitempty"`
	PollStartedAt  time.Time      `json:"poll_started_at"`
	IsRemoteReview bool           `json:"is_remote_review,omitempty"`
	IsUltraplan    bool           `json:"is_ultraplan,omitempty"`
}

var (
	remoteStates   = make(map[string]*RemoteAgentState)
	remoteStatesMu sync.Mutex
)

func setRemoteState(taskID string, state *RemoteAgentState) {
	remoteStatesMu.Lock()
	defer remoteStatesMu.Unlock()
	remoteStates[taskID] = state
}

func getRemoteState(taskID string) *RemoteAgentState {
	remoteStatesMu.Lock()
	defer remoteStatesMu.Unlock()
	return remoteStates[taskID]
}

func removeRemoteState(taskID string) {
	remoteStatesMu.Lock()
	defer remoteStatesMu.Unlock()
	delete(remoteStates, taskID)
}

// RemoteAgentTaskKiller implements TaskKiller for remote agent tasks.
type RemoteAgentTaskKiller struct {
	logger *slog.Logger
}

// NewRemoteAgentTaskKiller creates a killer for remote agent tasks.
func NewRemoteAgentTaskKiller(logger *slog.Logger) *RemoteAgentTaskKiller {
	return &RemoteAgentTaskKiller{logger: logger}
}

func (k *RemoteAgentTaskKiller) Name() string   { return "RemoteAgentTask" }
func (k *RemoteAgentTaskKiller) Type() TaskType { return TaskTypeRemote }

// Kill stops a remote agent task.
func (k *RemoteAgentTaskKiller) Kill(taskID string, registry *TaskRegistry) error {
	state, ok := registry.Get(taskID)
	if !ok {
		return fmt.Errorf("remote agent task %s not found", taskID)
	}
	if state.Status != StatusRunning {
		return nil
	}

	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusKilled
		t.EndTime = &now
		t.Notified = true
	})

	k.logger.Info("remote agent task killed", "task_id", taskID)
	removeRemoteState(taskID)
	return nil
}

// RegisterRemoteAgent registers a new remote agent task.
func RegisterRemoteAgent(
	registry *TaskRegistry,
	remoteType RemoteTaskType,
	sessionID string,
	title string,
	command string,
	toolUseID string,
	logger *slog.Logger,
) *TaskState {
	state := NewTaskState(TaskTypeRemote, title)
	state.ToolUseID = toolUseID
	state.Status = StatusRunning

	remoteState := &RemoteAgentState{
		RemoteTaskType: remoteType,
		SessionID:      sessionID,
		Command:        command,
		Title:          title,
		PollStartedAt:  time.Now(),
		IsUltraplan:    remoteType == RemoteTaskUltraplan,
		IsRemoteReview: remoteType == RemoteTaskUltrareview,
	}
	setRemoteState(state.ID, remoteState)

	registry.Register(state)

	logger.Info("remote agent task registered",
		"task_id", state.ID,
		"remote_type", remoteType,
		"session_id", sessionID,
	)
	return state
}

// CompleteRemoteAgent marks a remote agent task as completed.
func CompleteRemoteAgent(registry *TaskRegistry, taskID string) {
	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusCompleted
		t.EndTime = &now
	})
	removeRemoteState(taskID)
}

// FailRemoteAgent marks a remote agent task as failed.
func FailRemoteAgent(registry *TaskRegistry, taskID string) {
	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusFailed
		t.EndTime = &now
	})
	removeRemoteState(taskID)
}
