package tool

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/costa/polypod/internal/types"
)

// ToolStatus tracks the lifecycle state of a tool in the streaming executor.
type ToolStatus string

const (
	StatusQueued    ToolStatus = "queued"
	StatusExecuting ToolStatus = "executing"
	StatusCompleted ToolStatus = "completed"
	StatusYielded   ToolStatus = "yielded"
)

// TrackedTool holds the state for a tool managed by the StreamingToolExecutor.
type TrackedTool struct {
	ID                string
	Call              ToolUseRequest
	Status            ToolStatus
	IsConcurrencySafe bool
	Results           []ExecutionResult
	PendingProgress   []types.ToolProgress
	ContextModifiers  []func(types.ToolContext) types.ToolContext

	// done is closed when the tool finishes executing.
	done chan struct{}
}

// StreamingToolExecutor executes tools as they stream in, with concurrency control.
//
// Concurrency-safe tools can execute in parallel with other concurrency-safe tools.
// Non-concurrent tools must execute alone (exclusive access).
// Results are buffered and emitted in the order tools were received, preserving
// the ordering guarantee the model expects.
//
// Progress messages are forwarded immediately regardless of ordering.
//
// This is the Go equivalent of the TypeScript StreamingToolExecutor class.
type StreamingToolExecutor struct {
	mu        sync.Mutex
	tools     []*TrackedTool
	registry  *Registry
	executor  *ToolExecutor
	toolCtx   types.ToolContext
	logger    *slog.Logger
	config    OrchestratorConfig
	hasError  atomic.Bool
	discarded atomic.Bool

	// batchCancel cancels all sibling tools when a bash error occurs.
	batchCancel context.CancelFunc
	batchCtx    context.Context

	// parentCtx is the caller's context (for user-initiated cancellation).
	parentCtx context.Context

	// progressCh receives progress updates from executing tools.
	// Non-blocking sends; consumers should drain this channel.
	progressCh chan types.ToolProgress

	// wakeUp signals the result consumer that new results or progress are available.
	wakeUp chan struct{}
}

// NewStreamingToolExecutor creates a streaming executor that executes tools
// as they arrive. It starts accepting tools immediately via AddTool.
//
// Call GetResults to consume execution results in order.
// Call Discard to abandon all pending work.
func NewStreamingToolExecutor(
	ctx context.Context,
	registry *Registry,
	executor *ToolExecutor,
	toolCtx types.ToolContext,
	config OrchestratorConfig,
) *StreamingToolExecutor {
	batchCtx, batchCancel := context.WithCancel(ctx)

	return &StreamingToolExecutor{
		registry:    registry,
		executor:    executor,
		toolCtx:     toolCtx,
		logger:      config.Logger,
		config:      config,
		parentCtx:   ctx,
		batchCtx:    batchCtx,
		batchCancel: batchCancel,
		progressCh:  make(chan types.ToolProgress, 64),
		wakeUp:      make(chan struct{}, 1),
	}
}

// AddTool enqueues a tool for execution. It starts executing immediately
// if concurrency conditions allow. This method is safe to call concurrently.
func (s *StreamingToolExecutor) AddTool(req ToolUseRequest) {
	if s.discarded.Load() {
		return
	}

	isSafe := s.checkConcurrencySafe(req)

	tracked := &TrackedTool{
		ID:                req.ID,
		Call:              req,
		Status:            StatusQueued,
		IsConcurrencySafe: isSafe,
		done:              make(chan struct{}),
	}

	s.mu.Lock()
	s.tools = append(s.tools, tracked)
	s.mu.Unlock()

	go s.processQueue()
}

// Discard abandons all pending and in-progress tools.
// Queued tools won't start, and in-progress tools will be cancelled.
func (s *StreamingToolExecutor) Discard() {
	s.discarded.Store(true)
	s.batchCancel()
	s.signal()
}

// ProgressChan returns a channel that receives progress updates from tools.
func (s *StreamingToolExecutor) ProgressChan() <-chan types.ToolProgress {
	return s.progressCh
}

// GetResults returns a channel that yields execution results in the order
// tools were added. It respects ordering: a non-concurrent tool blocks
// yielding until all prior tools have been yielded.
//
// The returned channel is closed when all tools have been yielded or
// the executor is discarded.
func (s *StreamingToolExecutor) GetResults(ctx context.Context) <-chan StreamingResult {
	out := make(chan StreamingResult, 16)

	go func() {
		defer close(out)

		for {
			if s.discarded.Load() {
				return
			}

			// Yield completed results in order.
			yielded := s.yieldCompleted(ctx, out)

			// Check if all tools are done.
			if s.allYielded() {
				return
			}

			// If nothing was yielded, wait for a signal.
			if !yielded {
				select {
				case <-ctx.Done():
					return
				case <-s.wakeUp:
					// New results or progress available.
				}
			}
		}
	}()

	return out
}

// StreamingResult is a single result emitted by the streaming executor.
type StreamingResult struct {
	ExecutionResult ExecutionResult
	ToolCtx         types.ToolContext
}

// GetUpdatedContext returns the current tool context, which may have been
// modified by completed tools' context modifiers.
func (s *StreamingToolExecutor) GetUpdatedContext() types.ToolContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCtx
}

// --- internal methods ---

func (s *StreamingToolExecutor) checkConcurrencySafe(req ToolUseRequest) bool {
	t, ok := s.registry.Get(req.ToolName)
	if !ok {
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn("IsConcurrencySafe panicked", "tool", req.ToolName, "panic", r)
		}
	}()
	return t.IsConcurrencySafe(req.Input)
}

// canExecute determines if a tool can start executing given the current state.
func (s *StreamingToolExecutor) canExecute(isConcurrencySafe bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	var executingCount int
	var allExecutingSafe bool = true
	for _, t := range s.tools {
		if t.Status == StatusExecuting {
			executingCount++
			if !t.IsConcurrencySafe {
				allExecutingSafe = false
			}
		}
	}

	if executingCount == 0 {
		return true
	}
	return isConcurrencySafe && allExecutingSafe
}

// processQueue starts queued tools when concurrency conditions allow.
func (s *StreamingToolExecutor) processQueue() {
	s.mu.Lock()
	var toStart []*TrackedTool
	for _, t := range s.tools {
		if t.Status != StatusQueued {
			continue
		}
		if s.discarded.Load() || s.hasError.Load() {
			break
		}
		if s.canExecuteUnlocked(t.IsConcurrencySafe) {
			t.Status = StatusExecuting
			toStart = append(toStart, t)
		} else if !t.IsConcurrencySafe {
			// Non-concurrent tool can't start yet; stop scanning
			// to maintain order.
			break
		}
	}
	s.mu.Unlock()

	for _, t := range toStart {
		go s.executeTool(t)
	}
}

// canExecuteUnlocked is like canExecute but assumes s.mu is held.
func (s *StreamingToolExecutor) canExecuteUnlocked(isConcurrencySafe bool) bool {
	var executingCount int
	var allExecutingSafe bool = true
	for _, t := range s.tools {
		if t.Status == StatusExecuting {
			executingCount++
			if !t.IsConcurrencySafe {
				allExecutingSafe = false
			}
		}
	}
	if executingCount == 0 {
		return true
	}
	return isConcurrencySafe && allExecutingSafe
}

// executeTool runs a single tool and stores its results.
func (s *StreamingToolExecutor) executeTool(tracked *TrackedTool) {
	defer func() {
		close(tracked.done)
		s.signal()
		go s.processQueue()
	}()

	// Check for cancellation before starting.
	if s.discarded.Load() {
		s.setToolResult(tracked, ExecutionResult{
			ToolUseID: tracked.ID,
			ToolName:  tracked.Call.ToolName,
			Phase:     PhaseCancelled,
			Result:    types.ToolResult{Content: "discarded: streaming fallback", IsError: true},
		})
		return
	}

	if s.hasError.Load() {
		s.setToolResult(tracked, ExecutionResult{
			ToolUseID: tracked.ID,
			ToolName:  tracked.Call.ToolName,
			Phase:     PhaseCancelled,
			Result:    types.ToolResult{Content: "cancelled: sibling tool errored", IsError: true},
		})
		return
	}

	// Create a progress callback that forwards to the progress channel.
	onProgress := func(p types.ToolProgress) {
		p.ToolUseID = tracked.ID
		select {
		case s.progressCh <- p:
		default:
			// Drop if channel full.
		}
		s.signal()
	}

	// Execute the tool using the batch context (cancellable by sibling errors).
	er := s.executor.ExecuteTool(s.batchCtx, tracked.Call, s.toolCtx, onProgress)

	// If this is a bash tool error, cancel siblings.
	if er.Result.IsError && tracked.Call.ToolName == s.config.BashToolName {
		s.hasError.Store(true)
		s.batchCancel()
	}

	// Apply context modifiers for non-concurrent tools.
	if er.ContextModifier != nil && !tracked.IsConcurrencySafe {
		s.mu.Lock()
		s.toolCtx = er.ContextModifier(s.toolCtx)
		s.mu.Unlock()
	}

	s.setToolResult(tracked, er)
}

func (s *StreamingToolExecutor) setToolResult(tracked *TrackedTool, er ExecutionResult) {
	s.mu.Lock()
	tracked.Results = []ExecutionResult{er}
	if er.ContextModifier != nil {
		tracked.ContextModifiers = append(tracked.ContextModifiers, er.ContextModifier)
	}
	tracked.Status = StatusCompleted
	s.mu.Unlock()
}

// yieldCompleted sends completed results to the output channel, in order.
// Returns true if at least one result was yielded.
func (s *StreamingToolExecutor) yieldCompleted(ctx context.Context, out chan<- StreamingResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	yielded := false
	for _, t := range s.tools {
		if t.Status == StatusYielded {
			continue
		}

		if t.Status == StatusCompleted && len(t.Results) > 0 {
			t.Status = StatusYielded

			for _, er := range t.Results {
				select {
				case out <- StreamingResult{
					ExecutionResult: er,
					ToolCtx:         s.toolCtx,
				}:
					yielded = true
				case <-ctx.Done():
					return yielded
				}
			}
		} else if t.Status == StatusExecuting && !t.IsConcurrencySafe {
			// Non-concurrent tool is still executing; block yielding
			// to preserve order.
			break
		} else if t.Status == StatusQueued {
			break
		}
	}
	return yielded
}

// allYielded returns true if every tracked tool has been yielded.
func (s *StreamingToolExecutor) allYielded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tools {
		if t.Status != StatusYielded {
			return false
		}
	}
	return len(s.tools) > 0
}

// signal wakes up the result consumer.
func (s *StreamingToolExecutor) signal() {
	select {
	case s.wakeUp <- struct{}{}:
	default:
	}
}

// WaitAll blocks until every tracked tool has completed.
func (s *StreamingToolExecutor) WaitAll() {
	s.mu.Lock()
	doneChans := make([]chan struct{}, len(s.tools))
	for i, t := range s.tools {
		doneChans[i] = t.done
	}
	s.mu.Unlock()

	for _, ch := range doneChans {
		<-ch
	}
}
