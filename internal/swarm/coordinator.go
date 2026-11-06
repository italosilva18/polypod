package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CoordinatorMode transforms an agent into a team coordinator that delegates
// work to worker agents and aggregates their results.
//
// Equivalent to Claude Code's coordinatorMode.ts which:
//   - Filters tools so the coordinator gets team management tools
//   - Augments the system prompt with team status
//   - Delegates tasks to workers
//   - Monitors progress across the team
//   - Aggregates results from workers
//   - Detects idle workers and redistributes work
type CoordinatorMode struct {
	mu     sync.RWMutex
	team   *Team
	logger *slog.Logger

	// Configuration
	enableIdleRedistribution bool
	idleCheckInterval        time.Duration
	resultAggregationCh      chan WorkerResult

	// State
	delegatedTasks map[string]*DelegatedTask // taskID → task
	cancel         context.CancelFunc
	ctx            context.Context
	running        bool
}

// DelegatedTask tracks a task assigned to a worker by the coordinator.
type DelegatedTask struct {
	ID          string     `json:"id"`
	WorkerName  string     `json:"worker_name"`
	Description string     `json:"description"`
	Prompt      string     `json:"prompt"`
	Status      string     `json:"status"` // "pending", "running", "completed", "failed"
	AssignedAt  time.Time  `json:"assigned_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// WorkerResult is emitted when a worker completes or fails a delegated task.
type WorkerResult struct {
	TaskID     string
	WorkerName string
	Success    bool
	Result     string
	Error      string
	Timestamp  time.Time
}

// CoordinatorConfig configures the coordinator mode.
type CoordinatorConfig struct {
	Team                     *Team
	Logger                   *slog.Logger
	EnableIdleRedistribution bool
	IdleCheckInterval        time.Duration
}

// NewCoordinatorMode creates a coordinator for the given team.
func NewCoordinatorMode(cfg CoordinatorConfig) *CoordinatorMode {
	if cfg.IdleCheckInterval == 0 {
		cfg.IdleCheckInterval = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &CoordinatorMode{
		team:                     cfg.Team,
		logger:                   cfg.Logger,
		enableIdleRedistribution: cfg.EnableIdleRedistribution,
		idleCheckInterval:        cfg.IdleCheckInterval,
		resultAggregationCh:      make(chan WorkerResult, 100),
		delegatedTasks:           make(map[string]*DelegatedTask),
		cancel:                   cancel,
		ctx:                      ctx,
	}
}

// Start begins the coordinator's background monitoring loops.
func (cm *CoordinatorMode) Start() {
	cm.mu.Lock()
	if cm.running {
		cm.mu.Unlock()
		return
	}
	cm.running = true
	cm.mu.Unlock()

	// Result aggregation goroutine.
	go cm.aggregationLoop()

	// Idle detection goroutine.
	if cm.enableIdleRedistribution {
		go cm.idleDetectionLoop()
	}

	// Permission polling goroutine — polls the bridge and auto-resolves
	// or queues for the leader.
	go cm.permissionPollingLoop()

	cm.logger.Info("coordinator mode started", "team", cm.team.Name)
}

// Stop shuts down the coordinator's background goroutines.
func (cm *CoordinatorMode) Stop() {
	cm.cancel()
	cm.mu.Lock()
	cm.running = false
	cm.mu.Unlock()
	cm.logger.Info("coordinator mode stopped", "team", cm.team.Name)
}

// DelegateTask assigns a task to a specific worker. The worker is sent
// a TaskAssignment message through the team's message router.
func (cm *CoordinatorMode) DelegateTask(workerName, description, prompt string) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Verify the worker exists.
	_, found := cm.team.GetMember(workerName)
	if !found {
		return "", fmt.Errorf("%w: %s", ErrAgentNotFound, workerName)
	}

	taskID := generateTaskDelegationID()
	task := &DelegatedTask{
		ID:          taskID,
		WorkerName:  workerName,
		Description: description,
		Prompt:      prompt,
		Status:      "pending",
		AssignedAt:  time.Now(),
	}
	cm.delegatedTasks[taskID] = task

	// Send the task assignment message.
	msg := NewMessage(MessageTaskAssignment, TeamLeaderName, workerName, prompt)
	msg.Summary = description
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]string)
	}
	msg.Metadata["task_id"] = taskID

	if err := cm.team.Router().SendMessage(msg); err != nil {
		delete(cm.delegatedTasks, taskID)
		return "", fmt.Errorf("failed to send task assignment: %w", err)
	}

	task.Status = "running"

	cm.logger.Info("task delegated",
		"task_id", taskID,
		"worker", workerName,
		"description", description,
		"team", cm.team.Name,
	)
	return taskID, nil
}

// ReportResult should be called when a worker reports task completion.
// It is safe to call from any goroutine.
func (cm *CoordinatorMode) ReportResult(result WorkerResult) {
	result.Timestamp = time.Now()
	select {
	case cm.resultAggregationCh <- result:
	default:
		cm.logger.Warn("result aggregation channel full, dropping result",
			"task_id", result.TaskID,
			"worker", result.WorkerName,
		)
	}
}

// GetTask returns a delegated task by ID.
func (cm *CoordinatorMode) GetTask(taskID string) (*DelegatedTask, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	t, ok := cm.delegatedTasks[taskID]
	return t, ok
}

// ListTasks returns all delegated tasks.
func (cm *CoordinatorMode) ListTasks() []*DelegatedTask {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	tasks := make([]*DelegatedTask, 0, len(cm.delegatedTasks))
	for _, t := range cm.delegatedTasks {
		tasks = append(tasks, t)
	}
	return tasks
}

// TeamStatusSummary returns a text summary of the team for system prompt
// augmentation. Mirrors Claude Code's system prompt augmentation with team
// status that the coordinator sees.
func (cm *CoordinatorMode) TeamStatusSummary() string {
	members := cm.team.ListMembers()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Team: %s (%d members)\n", cm.team.Name, len(members)))
	b.WriteString(fmt.Sprintf("State: %s\n\n", cm.team.State.String()))

	b.WriteString("Members:\n")
	for _, m := range members {
		status := "active"
		if m.IsIdle {
			status = "idle"
		}
		b.WriteString(fmt.Sprintf("  - %s (%s) [%s]", m.Name, m.AgentType, status))
		if m.Model != "" {
			b.WriteString(fmt.Sprintf(" model=%s", m.Model))
		}
		b.WriteString("\n")
	}

	cm.mu.RLock()
	pendingCount := 0
	runningCount := 0
	completedCount := 0
	failedCount := 0
	for _, t := range cm.delegatedTasks {
		switch t.Status {
		case "pending":
			pendingCount++
		case "running":
			runningCount++
		case "completed":
			completedCount++
		case "failed":
			failedCount++
		}
	}
	cm.mu.RUnlock()

	b.WriteString(fmt.Sprintf("\nTasks: %d pending, %d running, %d completed, %d failed\n",
		pendingCount, runningCount, completedCount, failedCount))

	return b.String()
}

// CoordinatorToolFilter returns the set of tool names that a coordinator
// agent should have access to. Workers have different tools.
// Mirrors Claude Code's INTERNAL_WORKER_TOOLS filtering.
func CoordinatorToolFilter() map[string]bool {
	return map[string]bool{
		"TeamCreate":  true,
		"TeamDelete":  true,
		"SendMessage": true,
		"AgentRun":    true,
		"TaskStop":    true,
		"TaskCreate":  true,
		"TaskGet":     true,
		"TaskList":    true,
		"TaskUpdate":  true,
	}
}

// CoordinatorSystemPrompt returns the coordinator's system prompt.
// Mirrors Claude Code's getCoordinatorSystemPrompt().
func CoordinatorSystemPrompt() string {
	return `You are a coordinator that orchestrates software engineering tasks across multiple workers.

## Your Role

You are a **coordinator**. Your job is to:
- Help the user achieve their goal
- Direct workers to research, implement and verify code changes
- Synthesize results and communicate with the user
- Answer questions directly when possible

## Your Tools

- **AgentRun** - Spawn a new worker
- **SendMessage** - Continue an existing worker or send messages to teammates
- **TaskStop** - Stop a running worker
- **TeamCreate** - Create a new team
- **TeamDelete** - Clean up team when done

## Task Workflow

| Phase | Who | Purpose |
|-------|-----|---------|
| Research | Workers (parallel) | Investigate codebase |
| Synthesis | You (coordinator) | Read findings, craft implementation specs |
| Implementation | Workers | Make targeted changes per spec |
| Verification | Workers | Test changes work |

Launch independent workers concurrently. Don't serialize work that can run simultaneously.

## Writing Worker Prompts

Workers can't see your conversation. Every prompt must be self-contained:
- Include file paths, line numbers, error messages
- State what "done" looks like
- Never write "based on your findings" - synthesize them yourself
`
}

// --- Background loops ---

// aggregationLoop processes worker results as they arrive.
func (cm *CoordinatorMode) aggregationLoop() {
	for {
		select {
		case <-cm.ctx.Done():
			return
		case result := <-cm.resultAggregationCh:
			cm.handleWorkerResult(result)
		}
	}
}

func (cm *CoordinatorMode) handleWorkerResult(result WorkerResult) {
	cm.mu.Lock()
	task, ok := cm.delegatedTasks[result.TaskID]
	if ok {
		now := time.Now()
		task.CompletedAt = &now
		if result.Success {
			task.Status = "completed"
			task.Result = result.Result
		} else {
			task.Status = "failed"
			task.Error = result.Error
		}
	}
	cm.mu.Unlock()

	if ok {
		cm.logger.Info("worker result received",
			"task_id", result.TaskID,
			"worker", result.WorkerName,
			"success", result.Success,
			"team", cm.team.Name,
		)
	} else {
		cm.logger.Warn("worker result for unknown task",
			"task_id", result.TaskID,
			"worker", result.WorkerName,
		)
	}
}

// idleDetectionLoop periodically checks for idle workers and can
// redistribute unclaimed tasks.
func (cm *CoordinatorMode) idleDetectionLoop() {
	ticker := time.NewTicker(cm.idleCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			return
		case <-ticker.C:
			cm.checkIdleWorkers()
		}
	}
}

func (cm *CoordinatorMode) checkIdleWorkers() {
	idle := cm.team.IdleTeammates()
	if len(idle) == 0 {
		return
	}

	// Find pending (unassigned) tasks that could be redistributed.
	cm.mu.RLock()
	var pendingTasks []*DelegatedTask
	for _, t := range cm.delegatedTasks {
		if t.Status == "pending" {
			pendingTasks = append(pendingTasks, t)
		}
	}
	cm.mu.RUnlock()

	if len(pendingTasks) == 0 {
		return
	}

	cm.logger.Debug("idle workers detected",
		"idle_count", len(idle),
		"pending_tasks", len(pendingTasks),
		"team", cm.team.Name,
	)

	// Assign one pending task per idle worker.
	for i := 0; i < len(idle) && i < len(pendingTasks); i++ {
		task := pendingTasks[i]
		worker := idle[i]

		msg := NewMessage(MessageTaskAssignment, TeamLeaderName, worker, task.Prompt)
		msg.Summary = task.Description
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]string)
		}
		msg.Metadata["task_id"] = task.ID

		if err := cm.team.Router().SendMessage(msg); err != nil {
			cm.logger.Warn("failed to redistribute task",
				"task_id", task.ID,
				"worker", worker,
				"error", err,
			)
			continue
		}

		cm.mu.Lock()
		task.WorkerName = worker
		task.Status = "running"
		cm.mu.Unlock()

		cm.logger.Info("task redistributed to idle worker",
			"task_id", task.ID,
			"worker", worker,
			"team", cm.team.Name,
		)
	}
}

// permissionPollingLoop watches the permission bridge for pending requests
// and logs them for the coordinator to handle.
func (cm *CoordinatorMode) permissionPollingLoop() {
	bridge := cm.team.PermissionBridgeRef()
	if bridge == nil {
		return
	}

	for {
		err := bridge.WaitForRequest(cm.ctx)
		if err != nil {
			return // context cancelled
		}

		pending := bridge.PendingRequests()
		for _, req := range pending {
			cm.logger.Info("permission request pending",
				"request_id", req.ID,
				"tool", req.ToolName,
				"worker", req.WorkerName,
				"description", req.Description,
				"team", cm.team.Name,
			)
			// In a full implementation, this would notify the UI or
			// auto-resolve based on coordinator policy. For now, requests
			// are left pending for explicit GrantPermission/DenyPermission
			// calls from the leader's tool-use flow.
		}
	}
}

// --- Helpers ---

var taskDelegationCounter uint64

func generateTaskDelegationID() string {
	n := atomic.AddUint64(&taskDelegationCounter, 1)
	return fmt.Sprintf("dtask-%d-%d", time.Now().UnixMilli(), n)
}
