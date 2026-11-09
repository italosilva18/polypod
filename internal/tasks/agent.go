package tasks

import (
	"context"
	"errors"
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
	cancel          context.CancelFunc
}

// AgentRunFunc runs an agent loop and reports progress without coupling tasks
// to a particular model provider or subagent implementation.
type AgentRunFunc func(context.Context, string, string, *AgentCheckpoint, func(AgentProgress), func(*AgentCheckpoint) error) (string, error)

// SetAgentRunner connects background agent tasks to the active agent runtime.
func (r *TaskRegistry) SetAgentRunner(run AgentRunFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentRunner = run
}

func (r *TaskRegistry) getAgentRunner() AgentRunFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agentRunner
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

// GetAgentState returns a snapshot of agent-specific task state.
func GetAgentState(taskID string) (LocalAgentState, bool) {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	state, ok := agentStates[taskID]
	if !ok {
		return LocalAgentState{}, false
	}
	snapshot := *state
	snapshot.cancel = nil
	snapshot.PendingMessages = append([]string(nil), state.PendingMessages...)
	if state.Progress != nil {
		progress := *state.Progress
		progress.RecentActivities = append([]ToolActivity(nil), progress.RecentActivities...)
		snapshot.Progress = &progress
	}
	return snapshot, true
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
	var stopped bool
	if !registry.Update(taskID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		now := time.Now()
		t.Status = StatusKilled
		t.EndTime = &now
		t.Notified = true
		t.Error = "Execucao cancelada pelo usuario."
		stopped = true
	}) {
		return fmt.Errorf("agent task %s not found", taskID)
	}
	if !stopped {
		return nil
	}
	agentStatesMu.Lock()
	state := agentStates[taskID]
	var cancel context.CancelFunc
	if state != nil {
		cancel = state.cancel
		state.cancel = nil
	}
	agentStatesMu.Unlock()
	if cancel != nil {
		cancel()
	}

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
	return registerLocalAgent(registry, agentID, description, prompt, agentType, toolUseID, "", nil, nil, logger)
}

func registerLocalAgent(
	registry *TaskRegistry,
	agentID string,
	description string,
	prompt string,
	agentType string,
	toolUseID string,
	outputPath string,
	cancel context.CancelFunc,
	checkpoint *AgentCheckpoint,
	logger *slog.Logger,
) *TaskState {
	state := NewTaskState(TaskTypeAgent, description)
	state.ID = agentID // Use agentID as taskID for consistency
	state.ToolUseID = toolUseID
	state.Status = StatusRunning
	state.input = prompt
	state.AgentName = agentType
	state.Checkpoint = cloneAgentCheckpoint(checkpoint)
	if outputPath != "" {
		state.OutputFile = outputPath
	}

	agentState := &LocalAgentState{
		AgentID:        agentID,
		Prompt:         prompt,
		AgentType:      agentType,
		IsBackgrounded: true,
		cancel:         cancel,
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

// SpawnAgentTask starts a real background agent loop. It retains values from
// the initiating context for audit and permissions while giving the task an
// independent cancellation lifetime.
func SpawnAgentTask(
	parent context.Context,
	registry *TaskRegistry,
	description string,
	prompt string,
	agentType string,
	logger *slog.Logger,
) (*TaskState, error) {
	return spawnAgentTask(parent, registry, description, prompt, agentType, nil, logger)
}

func spawnAgentTask(
	parent context.Context,
	registry *TaskRegistry,
	description string,
	prompt string,
	agentType string,
	checkpoint *AgentCheckpoint,
	logger *slog.Logger,
) (*TaskState, error) {
	if registry == nil {
		return nil, errors.New("task registry is not configured")
	}
	run := registry.getAgentRunner()
	if run == nil {
		return nil, errors.New("agent executor is not configured")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if agentType == "" {
		agentType = "general-purpose"
	}
	id := GenerateTaskID(TaskTypeAgent)
	output, err := registry.CreateOutputFile(TaskTypeAgent, id)
	if err != nil {
		return nil, fmt.Errorf("creating agent output: %w", err)
	}
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	runCtx, cancel := context.WithCancel(base)
	state := registerLocalAgent(registry, id, description, prompt, agentType, "", output.Name(), cancel, checkpoint, logger)

	go func() {
		defer cancel()
		defer func() {
			if closeErr := output.Close(); closeErr != nil {
				logger.Warn("closing agent output failed", "task_id", id, "error", closeErr)
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				message := fmt.Sprintf("falha interna no agente: %v", recovered)
				if _, writeErr := fmt.Fprintln(output, message); writeErr != nil {
					logger.Warn("writing agent failure failed", "task_id", id, "error", writeErr)
				}
				FailAgent(registry, id, message)
				logger.Error("background agent panic", "task_id", id, "panic", recovered)
			}
		}()
		report := func(progress AgentProgress) {
			UpdateAgentProgress(registry, id, progress)
			if progress.LastActivity != nil {
				if _, writeErr := fmt.Fprintf(output, "Ferramenta: %s; tokens: %d\n", progress.LastActivity.ToolName, progress.TokenCount); writeErr != nil {
					logger.Warn("writing agent progress failed", "task_id", id, "error", writeErr)
				}
			}
		}
		result, runErr := run(runCtx, agentType, prompt, cloneAgentCheckpoint(checkpoint), report,
			func(next *AgentCheckpoint) error { return registry.SaveAgentCheckpoint(id, next) })
		if runErr != nil {
			if _, writeErr := fmt.Fprintf(output, "Erro: %v\n", runErr); writeErr != nil {
				logger.Warn("writing agent error failed", "task_id", id, "error", writeErr)
			}
			FailAgent(registry, id, runErr.Error())
			return
		}
		if _, writeErr := fmt.Fprintln(output, result); writeErr != nil {
			FailAgent(registry, id, fmt.Sprintf("writing agent result: %v", writeErr))
			return
		}
		CompleteAgent(registry, id, result)
	}()
	return state, nil
}

// CompleteAgent marks an agent task as completed with result.
func CompleteAgent(registry *TaskRegistry, agentID string, result string) {
	now := time.Now()
	var completed bool
	registry.Update(agentID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusCompleted
		t.EndTime = &now
		t.Result = result
		t.Error = ""
		completed = true
	})
	if completed {
		agentStatesMu.Lock()
		if state := agentStates[agentID]; state != nil {
			state.Result = result
			state.cancel = nil
		}
		agentStatesMu.Unlock()
	}
}

// FailAgent marks an agent task as failed with error.
func FailAgent(registry *TaskRegistry, agentID string, errMsg string) {
	now := time.Now()
	var failed bool
	registry.Update(agentID, func(t *TaskState) {
		if t.Status != StatusRunning {
			return
		}
		t.Status = StatusFailed
		t.EndTime = &now
		t.Error = errMsg
		failed = true
	})
	if failed {
		agentStatesMu.Lock()
		if state := agentStates[agentID]; state != nil {
			state.Error = errMsg
			state.cancel = nil
		}
		agentStatesMu.Unlock()
	}
}

// UpdateAgentProgress updates progress metrics for an agent task.
func UpdateAgentProgress(registry *TaskRegistry, agentID string, progress AgentProgress) {
	registry.Update(agentID, func(state *TaskState) {
		if state.Status == StatusRunning {
			copy := progress
			copy.RecentActivities = append([]ToolActivity(nil), progress.RecentActivities...)
			state.Progress = &copy
		}
	})
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	if state := agentStates[agentID]; state != nil {
		state.Progress = &progress
	}
}

// QueuePendingMessage adds a message to an agent's pending queue.
func QueuePendingMessage(agentID string, msg string) {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	if state := agentStates[agentID]; state != nil {
		state.PendingMessages = append(state.PendingMessages, msg)
	}
}

// DrainPendingMessages returns and clears all pending messages for an agent.
func DrainPendingMessages(agentID string) []string {
	agentStatesMu.Lock()
	defer agentStatesMu.Unlock()
	state := agentStates[agentID]
	if state == nil || len(state.PendingMessages) == 0 {
		return nil
	}
	msgs := state.PendingMessages
	state.PendingMessages = nil
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
