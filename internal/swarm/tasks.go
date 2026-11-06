package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/costa/polypod/internal/types"
)

// TaskState tracks the lifecycle of a swarm task.
type TaskState string

const (
	TaskStateQueued    TaskState = "queued"
	TaskStateRunning   TaskState = "running"
	TaskStateWaiting   TaskState = "waiting" // waiting for permission/input
	TaskStateCompleted TaskState = "completed"
	TaskStateFailed    TaskState = "failed"
	TaskStateCancelled TaskState = "cancelled"
)

// IsTerminal returns true if the task will not transition further.
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed || s == TaskStateCancelled
}

// InProcessTeammateTask manages the lifecycle of a teammate running as a
// background goroutine. It owns the TeammateRunner, tracks state transitions,
// streams output, and handles cancellation.
//
// Equivalent to Claude Code's InProcessTeammateTask component
// (InProcessTeammateTask.tsx) + the InProcessTeammateTaskState type.
type InProcessTeammateTask struct {
	mu sync.RWMutex

	// Identity.
	ID       types.TaskID     `json:"id"`
	Identity TeammateIdentity `json:"identity"`

	// State tracking.
	State       TaskState  `json:"state"`
	Description string     `json:"description"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	Error       string     `json:"error,omitempty"`

	// Execution.
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
	Runner *TeammateRunner

	// Progress.
	TurnCount    int    `json:"turn_count"`
	TokensUsed   int64  `json:"tokens_used"`
	IsIdle       bool   `json:"is_idle"`
	LastActivity string `json:"last_activity,omitempty"`

	// Output streaming.
	outputMu  sync.Mutex
	outputs   []string      // accumulated turn outputs
	outputSub []chan string // subscribers for streaming

	// Shutdown tracking.
	ShutdownRequested int32 // atomic

	// Lifecycle.
	cancel context.CancelFunc
	ctx    context.Context
	done   chan struct{}
	logger *slog.Logger

	// Callbacks.
	onCompleteCbs []func(*InProcessTeammateTask)
	onCompleteMu  sync.Mutex
}

// InProcessTaskConfig configures an in-process teammate task.
type InProcessTaskConfig struct {
	Identity    TeammateIdentity
	Prompt      string
	Description string
	Model       string
	Executor    TeammateExecutor
	TokenBudget int
	TurnTimeout time.Duration
	Logger      *slog.Logger
}

var taskCounter uint64

// NewInProcessTeammateTask creates a new task but does not start it.
func NewInProcessTeammateTask(parentCtx context.Context, cfg InProcessTaskConfig) *InProcessTeammateTask {
	taskID := types.NewTaskID(types.TaskPrefixTeammate)
	ctx, cancel := context.WithCancel(parentCtx)

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Description == "" {
		desc := cfg.Prompt
		if len(desc) > 50 {
			desc = desc[:50] + "..."
		}
		cfg.Description = fmt.Sprintf("%s: %s", cfg.Identity.AgentName, desc)
	}

	mb := NewMailbox(cfg.Identity.AgentName, cfg.Logger)

	runnerCfg := RunnerConfig{
		Identity:    cfg.Identity,
		Mailbox:     mb,
		Executor:    cfg.Executor,
		TokenBudget: cfg.TokenBudget,
		TurnTimeout: cfg.TurnTimeout,
		Logger:      cfg.Logger,
	}

	runner := NewTeammateRunner(ctx, runnerCfg)

	task := &InProcessTeammateTask{
		ID:          taskID,
		Identity:    cfg.Identity,
		State:       TaskStateQueued,
		Description: cfg.Description,
		StartTime:   time.Now(),
		Prompt:      cfg.Prompt,
		Model:       cfg.Model,
		Runner:      runner,
		cancel:      cancel,
		ctx:         ctx,
		done:        make(chan struct{}),
		logger:      cfg.Logger,
	}

	return task
}

// Start launches the task's runner goroutine and feeds it the initial prompt.
func (t *InProcessTeammateTask) Start() {
	t.mu.Lock()
	if t.State != TaskStateQueued {
		t.mu.Unlock()
		return
	}
	t.State = TaskStateRunning
	t.mu.Unlock()

	// Deliver the initial prompt to the runner's mailbox.
	initialMsg := NewMessage(MessageTaskAssignment, TeamLeaderName, t.Identity.AgentName, t.Prompt)
	initialMsg.Summary = t.Description
	if err := t.Runner.cfg.Mailbox.Deliver(initialMsg); err != nil {
		t.mu.Lock()
		t.State = TaskStateFailed
		t.Error = fmt.Sprintf("failed to deliver initial prompt: %v", err)
		now := time.Now()
		t.EndTime = &now
		t.mu.Unlock()
		close(t.done)
		return
	}

	// Start the runner.
	t.Runner.Start()

	// Monitor goroutine: waits for the runner to finish, then finalizes.
	go func() {
		defer close(t.done)

		t.Runner.Wait()

		t.mu.Lock()
		if !t.State.IsTerminal() {
			if t.ctx.Err() != nil {
				t.State = TaskStateCancelled
			} else {
				t.State = TaskStateCompleted
			}
		}
		now := time.Now()
		t.EndTime = &now
		t.TurnCount = t.Runner.TurnCount()
		t.TokensUsed = t.Runner.TokensUsed()
		t.mu.Unlock()

		t.fireCompletionCallbacks()

		t.logger.Info("in-process teammate task finished",
			"task_id", t.ID,
			"agent_name", t.Identity.AgentName,
			"state", t.State,
			"turns", t.TurnCount,
			"tokens", t.TokensUsed,
		)
	}()

	t.logger.Info("in-process teammate task started",
		"task_id", t.ID,
		"agent_name", t.Identity.AgentName,
		"prompt_len", len(t.Prompt),
	)
}

// Cancel stops the task. The runner finishes its current turn and exits.
func (t *InProcessTeammateTask) Cancel() {
	t.cancel()
}

// Kill immediately cancels the task and marks it as cancelled.
func (t *InProcessTeammateTask) Kill() {
	t.mu.Lock()
	if t.State.IsTerminal() {
		t.mu.Unlock()
		return
	}
	t.State = TaskStateCancelled
	now := time.Now()
	t.EndTime = &now
	t.mu.Unlock()

	t.cancel()
	t.Runner.cfg.Mailbox.Close()

	t.logger.Info("in-process teammate task killed",
		"task_id", t.ID,
		"agent_name", t.Identity.AgentName,
	)
}

// Wait blocks until the task finishes.
func (t *InProcessTeammateTask) Wait() {
	<-t.done
}

// WaitContext blocks until the task finishes or the context is cancelled.
func (t *InProcessTeammateTask) WaitContext(ctx context.Context) error {
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RequestShutdown marks the task for graceful shutdown.
// The runner will receive a shutdown request and exit after its current turn.
func (t *InProcessTeammateTask) RequestShutdown() {
	if !atomic.CompareAndSwapInt32(&t.ShutdownRequested, 0, 1) {
		return
	}
	shutdownMsg := NewMessage(MessageShutdownRequest, TeamLeaderName, t.Identity.AgentName,
		fmt.Sprintf(`{"type":"shutdown_request","from":"%s","reason":"leader requested shutdown"}`, TeamLeaderName))
	if err := t.Runner.cfg.Mailbox.Deliver(shutdownMsg); err != nil {
		t.logger.Warn("failed to deliver shutdown request",
			"task_id", t.ID,
			"agent_name", t.Identity.AgentName,
			"error", err,
		)
	}
}

// SendMessage delivers a message to the teammate.
func (t *InProcessTeammateTask) SendMessage(from, content, summary string) error {
	msg := NewMessage(MessageChat, from, t.Identity.AgentName, content)
	msg.Summary = summary
	return t.Runner.cfg.Mailbox.Deliver(msg)
}

// GetState returns the current task state.
func (t *InProcessTeammateTask) GetState() TaskState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.State
}

// Snapshot returns a read-only snapshot of the task for display.
func (t *InProcessTeammateTask) Snapshot() InProcessTaskSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return InProcessTaskSnapshot{
		ID:                t.ID,
		Identity:          t.Identity,
		State:             t.State,
		Description:       t.Description,
		StartTime:         t.StartTime,
		EndTime:           t.EndTime,
		Error:             t.Error,
		Prompt:            t.Prompt,
		Model:             t.Model,
		TurnCount:         t.TurnCount,
		TokensUsed:        t.TokensUsed,
		IsIdle:            t.IsIdle,
		ShutdownRequested: atomic.LoadInt32(&t.ShutdownRequested) != 0,
		PendingMessages:   t.Runner.cfg.Mailbox.Pending(),
	}
}

// InProcessTaskSnapshot is a read-only copy of task state for display.
type InProcessTaskSnapshot struct {
	ID                types.TaskID     `json:"id"`
	Identity          TeammateIdentity `json:"identity"`
	State             TaskState        `json:"state"`
	Description       string           `json:"description"`
	StartTime         time.Time        `json:"start_time"`
	EndTime           *time.Time       `json:"end_time,omitempty"`
	Error             string           `json:"error,omitempty"`
	Prompt            string           `json:"prompt"`
	Model             string           `json:"model,omitempty"`
	TurnCount         int              `json:"turn_count"`
	TokensUsed        int64            `json:"tokens_used"`
	IsIdle            bool             `json:"is_idle"`
	ShutdownRequested bool             `json:"shutdown_requested"`
	PendingMessages   int              `json:"pending_messages"`
}

// Outputs returns all accumulated turn outputs.
func (t *InProcessTeammateTask) Outputs() []TeammateOutput {
	return t.Runner.Outputs()
}

// SubscribeOutput returns a channel that receives each new turn output
// as it becomes available. The channel is closed when the task finishes.
// Call Unsubscribe to stop receiving and free resources.
func (t *InProcessTeammateTask) SubscribeOutput() <-chan string {
	ch := make(chan string, 50)
	t.outputMu.Lock()
	t.outputSub = append(t.outputSub, ch)
	t.outputMu.Unlock()
	return ch
}

// broadcastOutput sends an output string to all subscribers.
func (t *InProcessTeammateTask) broadcastOutput(output string) {
	t.outputMu.Lock()
	subs := t.outputSub
	t.outputMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- output:
		default:
			// Subscriber is slow; drop the message to avoid blocking.
		}
	}
}

// closeOutputSubs closes all subscriber channels.
func (t *InProcessTeammateTask) closeOutputSubs() {
	t.outputMu.Lock()
	subs := t.outputSub
	t.outputSub = nil
	t.outputMu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
}

// OnComplete registers a callback invoked when the task reaches a terminal state.
func (t *InProcessTeammateTask) OnComplete(cb func(*InProcessTeammateTask)) {
	t.onCompleteMu.Lock()
	t.onCompleteCbs = append(t.onCompleteCbs, cb)
	t.onCompleteMu.Unlock()
}

func (t *InProcessTeammateTask) fireCompletionCallbacks() {
	t.onCompleteMu.Lock()
	cbs := t.onCompleteCbs
	t.onCompleteCbs = nil
	t.onCompleteMu.Unlock()

	for _, cb := range cbs {
		cb(t)
	}

	t.closeOutputSubs()
}

// --- Task Registry ---

// TaskRegistry tracks all in-process teammate tasks in the process.
// Thread-safe for concurrent access from the orchestrator and UI.
type TaskRegistry struct {
	mu    sync.RWMutex
	tasks map[types.TaskID]*InProcessTeammateTask
}

// NewTaskRegistry creates an empty task registry.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{
		tasks: make(map[types.TaskID]*InProcessTeammateTask),
	}
}

// Register adds a task to the registry.
func (r *TaskRegistry) Register(task *InProcessTeammateTask) {
	r.mu.Lock()
	r.tasks[task.ID] = task
	r.mu.Unlock()
}

// Unregister removes a task from the registry.
func (r *TaskRegistry) Unregister(id types.TaskID) {
	r.mu.Lock()
	delete(r.tasks, id)
	r.mu.Unlock()
}

// Get returns a task by ID.
func (r *TaskRegistry) Get(id types.TaskID) (*InProcessTeammateTask, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tasks[id]
	return t, ok
}

// FindByAgentID returns the task for a given agent ID, preferring running tasks.
// Mirrors Claude Code's findTeammateTaskByAgentId().
func (r *TaskRegistry) FindByAgentID(agentID string) *InProcessTeammateTask {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var fallback *InProcessTeammateTask
	for _, t := range r.tasks {
		if t.Identity.AgentID == agentID {
			if t.State == TaskStateRunning {
				return t
			}
			if fallback == nil {
				fallback = t
			}
		}
	}
	return fallback
}

// FindByAgentName returns the task for a given agent name.
func (r *TaskRegistry) FindByAgentName(name string) *InProcessTeammateTask {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, t := range r.tasks {
		if t.Identity.AgentName == name && t.State == TaskStateRunning {
			return t
		}
	}
	return nil
}

// All returns all registered tasks.
func (r *TaskRegistry) All() []*InProcessTeammateTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*InProcessTeammateTask, 0, len(r.tasks))
	for _, t := range r.tasks {
		result = append(result, t)
	}
	return result
}

// Running returns all tasks in the running state.
func (r *TaskRegistry) Running() []*InProcessTeammateTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*InProcessTeammateTask
	for _, t := range r.tasks {
		if t.State == TaskStateRunning || t.State == TaskStateWaiting {
			result = append(result, t)
		}
	}
	return result
}

// KillAll cancels all running tasks.
func (r *TaskRegistry) KillAll() {
	r.mu.RLock()
	tasks := make([]*InProcessTeammateTask, 0)
	for _, t := range r.tasks {
		if !t.State.IsTerminal() {
			tasks = append(tasks, t)
		}
	}
	r.mu.RUnlock()

	for _, t := range tasks {
		t.Kill()
	}
}

// Cleanup removes terminal tasks older than maxAge from the registry.
func (r *TaskRegistry) Cleanup(maxAge time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	removed := 0
	for id, t := range r.tasks {
		if t.State.IsTerminal() && t.EndTime != nil && now.Sub(*t.EndTime) > maxAge {
			delete(r.tasks, id)
			removed++
		}
	}
	return removed
}

// Stats returns summary statistics.
func (r *TaskRegistry) Stats() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := map[string]int{
		"total":     len(r.tasks),
		"queued":    0,
		"running":   0,
		"waiting":   0,
		"completed": 0,
		"failed":    0,
		"cancelled": 0,
	}
	for _, t := range r.tasks {
		switch t.State {
		case TaskStateQueued:
			stats["queued"]++
		case TaskStateRunning:
			stats["running"]++
		case TaskStateWaiting:
			stats["waiting"]++
		case TaskStateCompleted:
			stats["completed"]++
		case TaskStateFailed:
			stats["failed"]++
		case TaskStateCancelled:
			stats["cancelled"]++
		}
	}
	return stats
}
