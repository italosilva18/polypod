package parallel

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ToolCall represents a single tool invocation to execute.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolResult holds the result of a tool execution.
type ToolResult struct {
	ID     string
	Name   string
	Result string
	Error  error
	Duration time.Duration
}

// Executor runs tool calls, parallelizing independent ones.
type Executor struct {
	execFn func(name, argsJSON string) (string, error)
	logger *slog.Logger
}

// NewExecutor creates a parallel tool executor.
func NewExecutor(execFn func(name, argsJSON string) (string, error), logger *slog.Logger) *Executor {
	return &Executor{execFn: execFn, logger: logger}
}

// Execute runs tool calls with maximum parallelism for independent calls.
// Tools that read are independent; tools that write depend on prior writes to the same file.
func (e *Executor) Execute(calls []ToolCall) []ToolResult {
	if len(calls) <= 1 {
		return e.executeSequential(calls)
	}

	// Build dependency graph
	groups := e.buildGroups(calls)
	var allResults []ToolResult

	for _, group := range groups {
		if len(group) == 1 {
			// Single call — run directly
			r := e.executeSingle(group[0])
			allResults = append(allResults, r)
		} else {
			// Multiple independent calls — run in parallel
			results := e.executeParallel(group)
			allResults = append(allResults, results...)
		}
	}

	return allResults
}

// buildGroups separates calls into groups of independent calls that can run in parallel.
func (e *Executor) buildGroups(calls []ToolCall) [][]ToolCall {
	// Read-only tools that can always run in parallel
	readOnly := map[string]bool{
		"read_file": true, "read_files": true, "read_dir": true,
		"list_directory": true, "search_files": true, "repo_map": true,
		"find_symbol": true, "project_info": true, "git_status": true,
		"git_log": true, "git_diff": true, "git_remote": true,
		"git_blame": true, "git_show": true, "db_query": true,
		"db_schema": true, "db_tables": true, "list_memories": true,
		"recall_memory": true, "web_search": true, "fetch_url": true,
		"list_usb_devices": true, "list_serial_ports": true,
		"voice_available": true, "sandbox_available": true,
		"image_info": true, "mcp_list_servers": true,
		"plugin_list": true, "template_list": true,
		"list_sessions": true, "session_info": true,
		"scheduler_list": true, "usage_summary": true,
		"notify_channels": true, "security_scan": true,
		"security_secrets": true, "security_deps": true,
		"code_review": true, "lint_check": true,
	}

	var groups [][]ToolCall
	var currentParallel []ToolCall

	for _, call := range calls {
		if readOnly[call.Name] {
			currentParallel = append(currentParallel, call)
		} else {
			// Flush parallel group
			if len(currentParallel) > 0 {
				groups = append(groups, currentParallel)
				currentParallel = nil
			}
			// Write/mutate tools run alone
			groups = append(groups, []ToolCall{call})
		}
	}
	if len(currentParallel) > 0 {
		groups = append(groups, currentParallel)
	}

	return groups
}

func (e *Executor) executeParallel(calls []ToolCall) []ToolResult {
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup

	e.logger.Info("executando tools em paralelo", "count", len(calls))

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c ToolCall) {
			defer wg.Done()
			results[idx] = e.executeSingle(c)
		}(i, call)
	}

	wg.Wait()
	return results
}

func (e *Executor) executeSequential(calls []ToolCall) []ToolResult {
	results := make([]ToolResult, 0, len(calls))
	for _, call := range calls {
		results = append(results, e.executeSingle(call))
	}
	return results
}

func (e *Executor) executeSingle(call ToolCall) ToolResult {
	start := time.Now()
	result, err := e.execFn(call.Name, call.Arguments)
	duration := time.Since(start)

	e.logger.Debug("tool executada",
		"name", call.Name,
		"duration_ms", duration.Milliseconds(),
		"error", err != nil,
	)

	return ToolResult{
		ID:       call.ID,
		Name:     call.Name,
		Result:   result,
		Error:    err,
		Duration: duration,
	}
}

// FormatResults formats parallel execution results for display.
func FormatResults(results []ToolResult) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(&sb, "[%s] erro: %v (%dms)\n", r.Name, r.Error, r.Duration.Milliseconds())
		} else {
			fmt.Fprintf(&sb, "[%s] ok (%dms)\n", r.Name, r.Duration.Milliseconds())
		}
	}
	return sb.String()
}

// ExtractFilePaths extracts file paths from tool arguments for dependency analysis.
func ExtractFilePaths(argsJSON string) []string {
	var args map[string]string
	json.Unmarshal([]byte(argsJSON), &args)
	var paths []string
	for _, key := range []string{"path", "paths", "file", "source"} {
		if v, ok := args[key]; ok && v != "" {
			paths = append(paths, v)
		}
	}
	return paths
}
