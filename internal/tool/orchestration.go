package tool

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// Batch represents a group of tool calls that share the same execution strategy.
type Batch struct {
	// IsConcurrencySafe indicates all tools in this batch can run in parallel.
	IsConcurrencySafe bool

	// Calls are the tool invocations in this batch.
	Calls []ToolUseRequest
}

// BatchResult holds the results of executing a batch of tool calls.
type BatchResult struct {
	// Results are in the same order as the input calls.
	Results []ExecutionResult

	// Errors collects all non-nil errors from the batch.
	Errors []error
}

// HasErrors returns true if any tool in the batch failed.
func (br *BatchResult) HasErrors() bool {
	return len(br.Errors) > 0
}

// Orchestrator manages tool execution ordering, concurrency partitioning,
// and dependency resolution. It determines which tools can run in parallel
// and which must run sequentially, then delegates to a ToolExecutor.
type Orchestrator struct {
	executor *ToolExecutor
	registry *Registry
	logger   *slog.Logger
	config   OrchestratorConfig
}

// OrchestratorConfig controls orchestration behavior.
type OrchestratorConfig struct {
	// MaxConcurrency is the maximum number of tools running simultaneously
	// within a concurrent batch.
	MaxConcurrency int

	// Logger for orchestration events.
	Logger *slog.Logger

	// DeferredToolNames are tools that should be executed last in any batch.
	DeferredToolNames map[string]bool

	// PriorityToolNames are tools that should be executed first in any batch.
	PriorityToolNames map[string]bool

	// BashToolName is the name of the shell/bash tool. Errors from this tool
	// cancel sibling concurrent tools (bash commands often have implicit deps).
	BashToolName string
}

// DefaultOrchestratorConfig returns production defaults.
func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		MaxConcurrency: 10,
		Logger:         slog.Default(),
		DeferredToolNames: map[string]bool{
			"tool_search": true,
		},
		PriorityToolNames: map[string]bool{},
		BashToolName:      constants.ToolBash,
	}
}

// NewOrchestrator creates a tool orchestrator.
func NewOrchestrator(executor *ToolExecutor, registry *Registry, config OrchestratorConfig) *Orchestrator {
	return &Orchestrator{
		executor: executor,
		registry: registry,
		logger:   config.Logger,
		config:   config,
	}
}

// RunTools executes tool calls respecting concurrency constraints.
// It partitions calls into batches, runs concurrent batches in parallel,
// and sequential batches one at a time. Results and context updates are
// sent to the provided channel as they become available.
//
// The returned channel receives MessageUpdate values. It is closed when
// all tools have completed or the context is cancelled.
func (o *Orchestrator) RunTools(
	ctx context.Context,
	calls []ToolUseRequest,
	toolCtx types.ToolContext,
	onProgress func(types.ToolProgress),
) <-chan MessageUpdate {
	out := make(chan MessageUpdate, len(calls)*2)

	go func() {
		defer close(out)

		currentCtx := toolCtx
		batches := o.PartitionToolCalls(calls)

		for _, batch := range batches {
			if ctx.Err() != nil {
				break
			}

			if batch.IsConcurrencySafe {
				batchResult := o.runBatchConcurrent(ctx, batch, currentCtx, onProgress)
				for _, er := range batchResult.Results {
					if er.ContextModifier != nil {
						currentCtx = er.ContextModifier(currentCtx)
					}
					select {
					case out <- MessageUpdate{
						ExecutionResult: er,
						NewContext:      currentCtx,
					}:
					case <-ctx.Done():
						return
					}
				}
			} else {
				for _, call := range batch.Calls {
					if ctx.Err() != nil {
						break
					}
					er := o.executor.ExecuteTool(ctx, call, currentCtx, onProgress)
					if er.ContextModifier != nil {
						currentCtx = er.ContextModifier(currentCtx)
					}
					select {
					case out <- MessageUpdate{
						ExecutionResult: er,
						NewContext:      currentCtx,
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return out
}

// MessageUpdate is emitted for each tool execution result.
type MessageUpdate struct {
	ExecutionResult ExecutionResult
	NewContext      types.ToolContext
}

// PartitionToolCalls splits tool calls into batches where each batch is either:
//  1. A group of consecutive concurrency-safe tools (can run in parallel), or
//  2. A single non-concurrency-safe tool (must run alone).
//
// Deferred tools are moved to the end. Priority tools are moved to the front.
func (o *Orchestrator) PartitionToolCalls(calls []ToolUseRequest) []Batch {
	// Reorder: priority tools first, deferred tools last.
	ordered := o.reorderCalls(calls)

	var batches []Batch
	for _, call := range ordered {
		safe := o.isConcurrencySafe(call)

		if safe && len(batches) > 0 && batches[len(batches)-1].IsConcurrencySafe {
			// Extend the current concurrent batch.
			batches[len(batches)-1].Calls = append(batches[len(batches)-1].Calls, call)
		} else {
			batches = append(batches, Batch{
				IsConcurrencySafe: safe,
				Calls:             []ToolUseRequest{call},
			})
		}
	}

	return batches
}

// reorderCalls moves priority tools to the front and deferred tools to the back,
// preserving relative order within each group.
func (o *Orchestrator) reorderCalls(calls []ToolUseRequest) []ToolUseRequest {
	var priority, normal, deferred []ToolUseRequest

	for _, c := range calls {
		switch {
		case o.config.PriorityToolNames[c.ToolName]:
			priority = append(priority, c)
		case o.config.DeferredToolNames[c.ToolName]:
			deferred = append(deferred, c)
		default:
			normal = append(normal, c)
		}
	}

	result := make([]ToolUseRequest, 0, len(calls))
	result = append(result, priority...)
	result = append(result, normal...)
	result = append(result, deferred...)
	return result
}

// isConcurrencySafe checks whether a tool call can safely run in parallel
// with other concurrency-safe tools.
func (o *Orchestrator) isConcurrencySafe(call ToolUseRequest) bool {
	t, ok := o.registry.Get(call.ToolName)
	if !ok {
		return false
	}
	// We catch panics from IsConcurrencySafe because some tool implementations
	// may panic on malformed input (e.g., shell-quote parser).
	defer func() {
		if r := recover(); r != nil {
			o.logger.Warn("IsConcurrencySafe panicked", "tool", call.ToolName, "panic", r)
		}
	}()
	return t.IsConcurrencySafe(call.Input)
}

// runBatchConcurrent executes a batch of concurrency-safe tools in parallel
// with bounded concurrency. Results are returned in input order.
// If a bash tool errors, sibling tools are cancelled.
func (o *Orchestrator) runBatchConcurrent(
	ctx context.Context,
	batch Batch,
	toolCtx types.ToolContext,
	onProgress func(types.ToolProgress),
) BatchResult {
	n := len(batch.Calls)
	results := make([]ExecutionResult, n)
	errs := make([]error, n)

	// Create a cancellable child context for sibling cancellation.
	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()

	sem := make(chan struct{}, o.config.MaxConcurrency)
	var wg sync.WaitGroup
	var bashErrorOnce sync.Once

	for i, call := range batch.Calls {
		wg.Add(1)
		go func(idx int, c ToolUseRequest) {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-batchCtx.Done():
				results[idx] = ExecutionResult{
					ToolUseID: c.ID,
					ToolName:  c.ToolName,
					Phase:     PhaseCancelled,
					Result: types.ToolResult{
						Content: "cancelled: sibling tool errored",
						IsError: true,
					},
				}
				return
			}

			er := o.executor.ExecuteTool(batchCtx, c, toolCtx, onProgress)
			results[idx] = er

			if er.Error != nil {
				errs[idx] = er.Error
			}

			// Bash errors cancel siblings (bash commands often have implicit deps).
			if er.Result.IsError && c.ToolName == o.config.BashToolName {
				bashErrorOnce.Do(func() {
					o.logger.Info("bash tool error; cancelling sibling tools",
						"tool_use_id", c.ID,
					)
					cancelBatch()
				})
			}
		}(i, call)
	}

	wg.Wait()

	// Collect non-nil errors.
	var collectedErrs []error
	for _, e := range errs {
		if e != nil {
			collectedErrs = append(collectedErrs, e)
		}
	}

	return BatchResult{
		Results: results,
		Errors:  collectedErrs,
	}
}

// RunToolsSync executes tool calls synchronously (blocking) and returns all results.
// This is a convenience wrapper around RunTools for non-streaming callers.
func (o *Orchestrator) RunToolsSync(
	ctx context.Context,
	calls []ToolUseRequest,
	toolCtx types.ToolContext,
) []ExecutionResult {
	ch := o.RunTools(ctx, calls, toolCtx, nil)
	var results []ExecutionResult
	for update := range ch {
		results = append(results, update.ExecutionResult)
	}
	return results
}

// ResolveDependencies analyzes tool calls and returns them in an order that
// respects data dependencies. Tools that read files written by earlier tools
// are placed after the writing tool.
//
// This is best-effort: if dependency information cannot be determined,
// the original order is preserved.
func (o *Orchestrator) ResolveDependencies(calls []ToolUseRequest) []ToolUseRequest {
	if len(calls) <= 1 {
		return calls
	}

	// Track which files are written by which call index.
	writtenBy := make(map[string]int) // filepath -> call index

	type dep struct {
		from, to int
	}
	var deps []dep

	for i, call := range calls {
		t, ok := o.registry.Get(call.ToolName)
		if !ok {
			continue
		}

		paths := extractFilePaths(call.Input)
		isWrite := !t.IsReadOnly(call.Input)

		if isWrite {
			for _, p := range paths {
				writtenBy[p] = i
			}
		} else {
			// Read tool: if a previous write targets the same file, add a dependency.
			for _, p := range paths {
				if writerIdx, ok := writtenBy[p]; ok && writerIdx != i {
					deps = append(deps, dep{from: writerIdx, to: i})
				}
			}
		}
	}

	if len(deps) == 0 {
		return calls
	}

	// Topological sort using Kahn's algorithm.
	inDegree := make([]int, len(calls))
	adjList := make([][]int, len(calls))
	for _, d := range deps {
		adjList[d.from] = append(adjList[d.from], d.to)
		inDegree[d.to]++
	}

	var queue []int
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	sorted := make([]ToolUseRequest, 0, len(calls))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, calls[node])
		for _, neighbor := range adjList[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	// If topological sort didn't include all nodes (cycle), fall back to original order.
	if len(sorted) != len(calls) {
		o.logger.Warn("dependency cycle detected, using original order",
			"sorted", len(sorted), "total", len(calls))
		return calls
	}

	return sorted
}

// extractFilePaths extracts file path arguments from tool input.
func extractFilePaths(args map[string]any) []string {
	var paths []string
	for _, key := range []string{"path", "file_path", "file", "source", "target"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
	}
	return paths
}

// ExecutionSummary provides a summary of a batch execution.
type ExecutionSummary struct {
	TotalTools   int
	Succeeded    int
	Failed       int
	Cancelled    int
	TotalDuraton time.Duration
	ByPhase      map[ExecutionPhase]int
}

// Summarize creates a summary from a set of execution results.
func Summarize(results []ExecutionResult) ExecutionSummary {
	s := ExecutionSummary{
		TotalTools: len(results),
		ByPhase:    make(map[ExecutionPhase]int),
	}
	for _, r := range results {
		s.TotalDuraton += r.Duration
		s.ByPhase[r.Phase]++
		switch {
		case r.Phase == PhaseCancelled:
			s.Cancelled++
		case r.Result.IsError:
			s.Failed++
		default:
			s.Succeeded++
		}
	}
	return s
}

// FormatSummary returns a human-readable summary string.
func (s ExecutionSummary) FormatSummary() string {
	return fmt.Sprintf(
		"%d tools: %d succeeded, %d failed, %d cancelled (%s total)",
		s.TotalTools, s.Succeeded, s.Failed, s.Cancelled, s.TotalDuraton.Round(time.Millisecond),
	)
}
