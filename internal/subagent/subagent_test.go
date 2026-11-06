package subagent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// testLogger returns a logger that discards most output but is not nil.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- AgentRunner ---

func TestNewAgentRunner(t *testing.T) {
	r := NewAgentRunner(testLogger())
	if r == nil {
		t.Fatal("NewAgentRunner returned nil")
	}
	if r.logger == nil {
		t.Error("runner logger is nil")
	}
	if r.tasks == nil {
		t.Error("runner tasks map is nil")
	}
	if len(r.List()) != 0 {
		t.Errorf("expected 0 tasks on new runner, got %d", len(r.List()))
	}
}

func TestAgentRunner_SpawnAndWait_Success(t *testing.T) {
	r := NewAgentRunner(testLogger())

	executor := func(ctx context.Context, query string) (string, error) {
		return "result: " + query, nil
	}

	task, err := r.Spawn(context.Background(), SpawnConfig{
		AgentName: "task",
		Query:     "hello",
		Timeout:   2 * time.Second,
	}, executor)
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	if task == nil {
		t.Fatal("Spawn returned nil task")
	}

	// Wait for completion.
	waitCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done, err := r.Wait(waitCtx, task.State.ID)
	if err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}
	if done.State.Status != types.TaskStatusCompleted {
		t.Errorf("expected status completed, got %s", done.State.Status)
	}
	if done.Result != "result: hello" {
		t.Errorf("expected 'result: hello', got %q", done.Result)
	}
	if done.Error != nil {
		t.Errorf("expected nil error, got %v", done.Error)
	}
	if done.State.EndTime == nil {
		t.Error("expected EndTime to be set")
	}
}

func TestAgentRunner_SpawnAndWait_Failure(t *testing.T) {
	r := NewAgentRunner(testLogger())
	wantErr := errors.New("executor boom")

	executor := func(ctx context.Context, query string) (string, error) {
		return "", wantErr
	}

	task, err := r.Spawn(context.Background(), SpawnConfig{
		AgentName: "task",
		Query:     "fail-me",
		Timeout:   2 * time.Second,
	}, executor)
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}

	<-task.Done

	if task.State.Status != types.TaskStatusFailed {
		t.Errorf("expected status failed, got %s", task.State.Status)
	}
	if task.Error == nil || !errors.Is(task.Error, wantErr) {
		t.Errorf("expected executor error, got %v", task.Error)
	}
}

func TestAgentRunner_GetListStatsAndCleanup(t *testing.T) {
	r := NewAgentRunner(testLogger())

	executor := func(ctx context.Context, query string) (string, error) {
		return "ok", nil
	}

	task, err := r.Spawn(context.Background(), SpawnConfig{
		AgentName: "task",
		Query:     "q",
		Timeout:   time.Second,
		ToolUseID: "tool-use-42",
	}, executor)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-task.Done

	// Get.
	got, ok := r.Get(task.State.ID)
	if !ok {
		t.Fatal("Get reported not found")
	}
	if got.State.ToolUseID != "tool-use-42" {
		t.Errorf("ToolUseID not propagated: %q", got.State.ToolUseID)
	}

	// Missing ID.
	if _, ok := r.Get(types.TaskID("does-not-exist")); ok {
		t.Error("Get returned ok for missing id")
	}

	// List.
	all := r.List()
	if len(all) != 1 {
		t.Errorf("expected 1 task, got %d", len(all))
	}

	// ListByStatus / Running.
	completed := r.ListByStatus(types.TaskStatusCompleted)
	if len(completed) != 1 {
		t.Errorf("expected 1 completed task, got %d", len(completed))
	}
	if running := r.Running(); len(running) != 0 {
		t.Errorf("expected 0 running tasks, got %d", len(running))
	}

	// Stats.
	stats := r.Stats()
	if stats["total"] != 1 {
		t.Errorf("stats total = %d, want 1", stats["total"])
	}
	if stats["completed"] != 1 {
		t.Errorf("stats completed = %d, want 1", stats["completed"])
	}
	if stats["running"] != 0 {
		t.Errorf("stats running = %d, want 0", stats["running"])
	}

	// Cleanup with a tiny maxAge should remove the completed task.
	// Sleep a moment to make EndTime older than maxAge.
	time.Sleep(10 * time.Millisecond)
	removed := r.Cleanup(time.Millisecond)
	if removed != 1 {
		t.Errorf("Cleanup removed %d, want 1", removed)
	}
	if len(r.List()) != 0 {
		t.Errorf("expected 0 tasks after cleanup, got %d", len(r.List()))
	}
}

func TestAgentRunner_KillRunning(t *testing.T) {
	r := NewAgentRunner(testLogger())

	// Executor that blocks until ctx done.
	blocked := make(chan struct{})
	executor := func(ctx context.Context, query string) (string, error) {
		close(blocked)
		<-ctx.Done()
		return "", ctx.Err()
	}

	task, err := r.Spawn(context.Background(), SpawnConfig{
		AgentName: "task",
		Query:     "long",
		Timeout:   5 * time.Second,
	}, executor)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Wait for executor to actually start.
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("executor never started")
	}

	if err := r.Kill(task.State.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Status should be Killed immediately (set by Kill itself).
	if got, _ := r.Get(task.State.ID); got.State.Status != types.TaskStatusKilled {
		t.Errorf("expected status killed, got %s", got.State.Status)
	}

	// Wait for the goroutine to observe cancellation and finish.
	<-task.Done

	// Killing a terminal task should fail.
	if err := r.Kill(task.State.ID); err == nil {
		t.Error("expected error killing terminal task")
	}

	// Killing unknown id should fail.
	if err := r.Kill(types.TaskID("no-such")); err == nil {
		t.Error("expected error killing unknown task")
	}
}

func TestAgentRunner_Wait_UnknownTask(t *testing.T) {
	r := NewAgentRunner(testLogger())
	_, err := r.Wait(context.Background(), types.TaskID("ghost"))
	if err == nil {
		t.Error("expected error waiting on unknown task")
	}
}

func TestAgentRunner_Spawn_DefaultTimeout(t *testing.T) {
	r := NewAgentRunner(testLogger())
	executor := func(ctx context.Context, query string) (string, error) {
		// Ensure there's a deadline (default timeout applied).
		if _, ok := ctx.Deadline(); !ok {
			return "", errors.New("no deadline")
		}
		return "ok", nil
	}
	task, err := r.Spawn(context.Background(), SpawnConfig{
		AgentName: "task",
		Query:     "x",
		// Timeout omitted on purpose — should default.
	}, executor)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-task.Done
	if task.State.Status != types.TaskStatusCompleted {
		t.Errorf("status = %s, want completed; err=%v", task.State.Status, task.Error)
	}
}

// --- NewAgentToolWithFunc: tool schema and Execute ---

func TestNewAgentToolWithFunc_Schema(t *testing.T) {
	r := NewAgentRunner(testLogger())
	tool := NewAgentToolWithFunc(r, func(ctx context.Context, agent, query string) (string, error) {
		return "stub result", nil
	})
	if tool == nil {
		t.Fatal("NewAgentToolWithFunc returned nil")
	}

	if tool.Name() != constants.ToolAgentRun {
		t.Errorf("tool name = %q, want %q", tool.Name(), constants.ToolAgentRun)
	}
	if tool.Description() == "" {
		t.Error("tool description empty")
	}
	if !tool.IsReadOnly(nil) {
		t.Error("agent_run tool should be read-only")
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Error("agent_run tool should be concurrency-safe")
	}
	if !tool.IsEnabled() {
		t.Error("agent_run tool should be enabled")
	}

	schema := tool.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties not a map: %T", schema["properties"])
	}
	for _, p := range []string{"prompt", "agent_name", "timeout"} {
		if _, ok := props[p]; !ok {
			t.Errorf("schema missing property %q", p)
		}
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema required not []string: %T", schema["required"])
	}
	foundPrompt := false
	for _, r := range required {
		if r == "prompt" {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Error("schema required should include 'prompt'")
	}
}

func TestNewAgentToolWithFunc_ExecuteSuccess(t *testing.T) {
	r := NewAgentRunner(testLogger())

	var gotAgent, gotQuery string
	tool := NewAgentToolWithFunc(r, func(ctx context.Context, agent, query string) (string, error) {
		gotAgent = agent
		gotQuery = query
		return "sub-result", nil
	})

	res, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{
			"prompt":     "analyze file",
			"agent_name": "coder",
		},
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError=true: %s", res.Content)
	}
	if res.Content != "sub-result" {
		t.Errorf("result = %q, want 'sub-result'", res.Content)
	}
	if gotAgent != "coder" {
		t.Errorf("executor called with agent=%q, want 'coder'", gotAgent)
	}
	if gotQuery != "analyze file" {
		t.Errorf("executor called with query=%q, want 'analyze file'", gotQuery)
	}
}

func TestNewAgentToolWithFunc_ExecuteDefaultAgent(t *testing.T) {
	r := NewAgentRunner(testLogger())

	var gotAgent string
	tool := NewAgentToolWithFunc(r, func(ctx context.Context, agent, query string) (string, error) {
		gotAgent = agent
		return "ok", nil
	})

	_, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{"prompt": "do stuff"},
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAgent != "task" {
		t.Errorf("default agent = %q, want 'task'", gotAgent)
	}
}

func TestNewAgentToolWithFunc_ExecuteSubAgentError(t *testing.T) {
	r := NewAgentRunner(testLogger())
	tool := NewAgentToolWithFunc(r, func(ctx context.Context, agent, query string) (string, error) {
		return "", errors.New("sub boom")
	})

	res, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{"prompt": "x"},
	)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when sub-agent fails")
	}
	if !strings.Contains(res.Content, "sub boom") {
		t.Errorf("content should mention underlying error, got %q", res.Content)
	}
}

// --- NewSubAgentExecutor constructor ---

func TestNewSubAgentExecutor_DefaultsAndOverrides(t *testing.T) {
	// Defaults (zero config) — dependencies can be nil because we only test
	// the constructor, not Execute.
	def := NewSubAgentExecutor(SubAgentConfig{})
	if def == nil {
		t.Fatal("NewSubAgentExecutor returned nil for empty config")
	}
	if def.maxTurns != DefaultMaxTurns {
		t.Errorf("default maxTurns = %d, want %d", def.maxTurns, DefaultMaxTurns)
	}
	if def.tokenBudget != DefaultTokenBudge {
		t.Errorf("default tokenBudget = %d, want %d", def.tokenBudget, DefaultTokenBudge)
	}
	if def.logger == nil {
		t.Error("default logger should fall back to slog.Default, not nil")
	}

	// Overrides.
	custom := NewSubAgentExecutor(SubAgentConfig{
		Logger:      testLogger(),
		MaxTurns:    3,
		TokenBudget: 1234,
	})
	if custom.maxTurns != 3 {
		t.Errorf("custom maxTurns = %d, want 3", custom.maxTurns)
	}
	if custom.tokenBudget != 1234 {
		t.Errorf("custom tokenBudget = %d, want 1234", custom.tokenBudget)
	}
}

// Ensure the package-level default constants are sensible.
func TestDefaultsAreReasonable(t *testing.T) {
	if DefaultMaxTurns <= 0 {
		t.Errorf("DefaultMaxTurns = %d, want > 0", DefaultMaxTurns)
	}
	if DefaultTokenBudge <= 0 {
		t.Errorf("DefaultTokenBudge = %d, want > 0", DefaultTokenBudge)
	}
	if DefaultTimeout <= 0 {
		t.Errorf("DefaultTimeout = %v, want > 0", DefaultTimeout)
	}
}
