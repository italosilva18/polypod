package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/workdir"
)

// maxConcurrentReadOnly is the maximum number of read-only tools that can
// execute simultaneously within a single streaming tool batch.
const maxConcurrentReadOnly = 5

// OrchestratorExecutor is the minimal surface required by StreamingToolManager
// to route tool calls through the full orchestration pipeline (plan-mode
// simulation, permission enforcement, hooks, execution, post-hooks, and
// auto-memory extraction).
//
// This interface is defined here (rather than imported from the orchestrator
// package) to break the import cycle: ai → orchestrator → ai. The concrete
// *orchestrator.Orchestrator type satisfies this interface via duck typing.
type OrchestratorExecutor interface {
	ExecuteTool(ctx context.Context, toolName string, args map[string]any) (types.ToolResult, error)
}

// ToolCallInfo describes a single tool invocation extracted from the AI stream.
type ToolCallInfo struct {
	ID        string
	Name      string
	Arguments string // raw JSON
	// Prepared while Chat Completions was still streaming. It is used only
	// after EOF confirms that the final arguments match this JSON object.
	preparedArgs map[string]any
}

// ToolCallResult holds the outcome of executing a single tool call.
type ToolCallResult struct {
	Images  []types.ToolImage
	ID      string
	Name    string
	Content string
	IsError bool
}

// ToolCallProgress tracks the current state of an in-flight tool call.
type ToolCallProgress struct {
	ID     string
	Name   string
	Status ToolCallStatus
}

// ToolCallStatus represents the lifecycle state of a tool call within the
// streaming manager.
type ToolCallStatus string

const (
	CallStatusPending   ToolCallStatus = "pending"
	CallStatusRunning   ToolCallStatus = "running"
	CallStatusCompleted ToolCallStatus = "completed"
	CallStatusFailed    ToolCallStatus = "failed"
)

// StreamingToolManager orchestrates tool execution during streaming responses.
// It partitions tool calls into concurrent (read-only) and serial (write)
// batches, executes read-only tools in parallel with bounded concurrency,
// and runs write tools serially to preserve correctness.
//
// When an OrchestratorExecutor is supplied, every tool call is routed through
// the full orchestration pipeline (plan simulation, permission enforcement,
// PreToolUse/PostToolUse hooks, and auto-memory extraction). When the
// executor is nil, the manager falls back to direct registry execution
// (used only by tests that do not need the full pipeline).
type StreamingToolManager struct {
	registry *tool.Registry
	orchExec OrchestratorExecutor
	logger   *slog.Logger

	// mu protects progress tracking.
	mu       sync.Mutex
	progress map[string]*ToolCallProgress
	total    atomic.Int64
	done     atomic.Int64
}

// NewStreamingToolManager creates a manager that bridges the tool system
// into the AI client's streaming tool-calling loop.
//
// The orchExec parameter MUST be supplied in production wiring so that
// permission checks, hooks, plan-mode simulation and memory extraction are
// applied to every tool call. Passing nil bypasses the orchestration
// pipeline and should only be used in unit tests.
func NewStreamingToolManager(
	reg *tool.Registry,
	orchExec OrchestratorExecutor,
	logger *slog.Logger,
) *StreamingToolManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &StreamingToolManager{
		registry: reg,
		orchExec: orchExec,
		logger:   logger,
		progress: make(map[string]*ToolCallProgress),
	}
}

// PartitionCalls splits tool calls into two groups:
//   - concurrent: tools where both IsReadOnly and IsConcurrencySafe are true
//   - serial: everything else (write tools, destructive tools, unknown tools)
//
// PartitionCalls is a classification helper. ExecuteToolCalls preserves the
// original call order by treating serial calls as barriers.
func (m *StreamingToolManager) PartitionCalls(calls []ToolCallInfo) (concurrent []ToolCallInfo, serial []ToolCallInfo) {
	for _, call := range calls {
		args := m.argsFor(call)
		t, ok := m.registry.Get(call.Name)
		if !ok {
			// Unknown tools are treated as serial for safety.
			serial = append(serial, call)
			continue
		}

		isSafe := m.checkSafe(t, args, call.Name)
		isReadOnly := m.checkReadOnly(t, args, call.Name)

		if isSafe && isReadOnly {
			concurrent = append(concurrent, call)
		} else {
			serial = append(serial, call)
		}
	}
	return concurrent, serial
}

// ExecuteToolCalls executes a batch of tool calls with intelligent parallelism.
// Consecutive read-only, concurrency-safe tools run in parallel (up to
// maxConcurrentReadOnly). Other tools act as barriers and run in call order.
// Results are returned in the same order as the model's tool calls.
func (m *StreamingToolManager) ExecuteToolCalls(ctx context.Context, calls []ToolCallInfo) []ToolCallResult {
	if len(calls) == 0 {
		return nil
	}

	m.total.Store(int64(len(calls)))
	m.done.Store(0)

	// Initialize progress tracking.
	m.mu.Lock()
	m.progress = make(map[string]*ToolCallProgress, len(calls))
	for _, call := range calls {
		m.progress[call.ID] = &ToolCallProgress{
			ID:     call.ID,
			Name:   call.Name,
			Status: CallStatusPending,
		}
	}
	m.mu.Unlock()

	canRunConcurrent := make([]bool, len(calls))
	concurrentCount := 0
	for i, call := range calls {
		args := m.argsFor(call)
		if t, ok := m.registry.GetForContext(ctx, call.Name); ok && m.checkSafe(t, args, call.Name) && m.checkReadOnly(t, args, call.Name) {
			canRunConcurrent[i] = true
			concurrentCount++
		}
	}

	m.logger.Info("streaming tool execution planned",
		"total", len(calls),
		"concurrent", concurrentCount,
		"serial", len(calls)-concurrentCount,
	)

	results := make([]ToolCallResult, 0, len(calls))
	readBatch := make([]ToolCallInfo, 0, len(calls))
	flushReads := func() {
		if len(readBatch) == 0 {
			return
		}
		results = append(results, m.executeConcurrent(ctx, readBatch)...)
		readBatch = readBatch[:0]
	}
	for i, call := range calls {
		if canRunConcurrent[i] {
			readBatch = append(readBatch, call)
			continue
		}
		flushReads()
		results = append(results, m.executeSerial(ctx, []ToolCallInfo{call})...)
	}
	flushReads()

	return results
}

// executeConcurrent runs read-only tools in parallel with bounded concurrency.
func (m *StreamingToolManager) executeConcurrent(ctx context.Context, calls []ToolCallInfo) []ToolCallResult {
	results := make([]ToolCallResult, len(calls))
	sem := make(chan struct{}, maxConcurrentReadOnly)
	var wg sync.WaitGroup

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c ToolCallInfo) {
			defer wg.Done()

			// Acquire semaphore.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = ToolCallResult{
					ID:      c.ID,
					Name:    c.Name,
					Content: "cancelled: context done",
					IsError: true,
				}
				m.setProgress(c.ID, CallStatusFailed)
				m.done.Add(1)
				return
			}

			results[idx] = m.executeSingle(ctx, c)
		}(i, call)
	}

	wg.Wait()
	return results
}

// executeSerial runs tools one at a time, in order.
func (m *StreamingToolManager) executeSerial(ctx context.Context, calls []ToolCallInfo) []ToolCallResult {
	results := make([]ToolCallResult, 0, len(calls))
	for _, call := range calls {
		if ctx.Err() != nil {
			results = append(results, ToolCallResult{
				ID:      call.ID,
				Name:    call.Name,
				Content: "cancelled: context done",
				IsError: true,
			})
			m.setProgress(call.ID, CallStatusFailed)
			m.done.Add(1)
			continue
		}
		results = append(results, m.executeSingle(ctx, call))
	}
	return results
}

// executeSingle runs a single tool call. When an OrchestratorExecutor is
// configured, the call is routed through the full orchestration pipeline
// (plan simulation → permission enforcer → PreToolUse hooks → tool execute
// → PostToolUse hooks). Conversation-backed memory extraction runs separately.
// Otherwise it falls back
// to direct registry execution, which performs only validation and the
// per-tool CheckPermissions hook.
func (m *StreamingToolManager) executeSingle(ctx context.Context, call ToolCallInfo) ToolCallResult {
	m.setProgress(call.ID, CallStatusRunning)

	args := m.argsFor(call)

	var (
		result types.ToolResult
		err    error
	)

	if m.orchExec != nil {
		// Full pipeline: enforcer → hooks → execute → post-hooks → memory.
		result, err = m.orchExec.ExecuteTool(ctx, call.Name, args)
	} else {
		// Fallback: direct registry execution. Used only when no
		// orchestrator is wired (tests). NOTE: this path skips permission
		// enforcement, hooks, plan-mode simulation, and memory extraction.
		toolCtx := types.ToolContext{
			Context:    ctx,
			WorkingDir: workdir.Dir(ctx, "."),
		}
		result, err = m.registry.Execute(call.Name, args, toolCtx)
	}

	m.done.Add(1)

	if err != nil {
		m.logger.Error("tool execution failed",
			"tool", call.Name,
			"id", call.ID,
			"error", err,
		)
		m.setProgress(call.ID, CallStatusFailed)
		return ToolCallResult{
			ID:      call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Error executing %s: %v", call.Name, err),
			IsError: true,
		}
	}

	if result.IsError {
		m.setProgress(call.ID, CallStatusFailed)
	} else {
		m.setProgress(call.ID, CallStatusCompleted)
		if m.orchExec == nil {
			// Production orchestration observes its own tool execution.
			evolution.ObserveTool(ctx, call.Name)
		}
	}

	m.logger.Debug("tool executed",
		"tool", call.Name,
		"id", call.ID,
		"is_error", result.IsError,
		"result_bytes", len(result.Content),
	)

	return ToolCallResult{
		ID:      call.ID,
		Name:    call.Name,
		Content: appendProjectInstructions(ctx, call.Name, args, truncateToolResult(result.Content), m.logger),
		Images:  result.Images,
		IsError: result.IsError,
	}
}

// Progress returns a snapshot of all tracked tool call progress.
func (m *StreamingToolManager) Progress() []ToolCallProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := make([]ToolCallProgress, 0, len(m.progress))
	for _, p := range m.progress {
		snap = append(snap, *p)
	}
	return snap
}

// Stats returns total and completed tool call counts.
func (m *StreamingToolManager) Stats() (total, completed int64) {
	return m.total.Load(), m.done.Load()
}

// setProgress updates the status of a tracked tool call.
func (m *StreamingToolManager) setProgress(id string, status ToolCallStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.progress[id]; ok {
		p.Status = status
	}
}

// parseArgs deserializes a JSON argument string into a map.
// Returns an empty map on parse failure so callers always get a non-nil map.
func (m *StreamingToolManager) parseArgs(argsJSON string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		m.logger.Warn("failed to parse tool arguments", "args", argsJSON, "error", err)
		return make(map[string]any)
	}
	return args
}

func (m *StreamingToolManager) argsFor(call ToolCallInfo) map[string]any {
	if call.preparedArgs == nil {
		return m.parseArgs(call.Arguments)
	}
	return clonePreparedArgs(call.preparedArgs)
}

// checkSafe wraps IsConcurrencySafe with panic recovery.
func (m *StreamingToolManager) checkSafe(t types.Tool, args map[string]any, name string) bool {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Warn("IsConcurrencySafe panicked", "tool", name, "panic", r)
		}
	}()
	return t.IsConcurrencySafe(args)
}

// checkReadOnly wraps IsReadOnly with panic recovery.
func (m *StreamingToolManager) checkReadOnly(t types.Tool, args map[string]any, name string) bool {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Warn("IsReadOnly panicked", "tool", name, "panic", r)
		}
	}()
	return t.IsReadOnly(args)
}
