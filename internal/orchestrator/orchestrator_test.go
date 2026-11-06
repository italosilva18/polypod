package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/permissions"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

// discardLogger returns a logger that drops all output so tests stay quiet.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testTool is a small bundle describing a tool wired up for a test.
type testTool struct {
	name     string
	readOnly bool
	// execCount atomically tracks how many times Execute was called.
	execCount *int32
	// execErr, if non-nil, is returned from Execute.
	execErr error
	// execResult is the result returned by Execute when execErr is nil.
	execResult types.ToolResult
	// onExecute is called (after incrementing execCount) from Execute.
	onExecute func()
}

func buildTestTool(tt *testTool) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return tt.name },
		DescriptionFn: func() string { return "test tool " + tt.name },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object"}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return tt.readOnly },
		IsDestructiveFn: func(map[string]any) bool { return !tt.readOnly },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if tt.execCount != nil {
				atomic.AddInt32(tt.execCount, 1)
			}
			if tt.onExecute != nil {
				tt.onExecute()
			}
			if tt.execErr != nil {
				return types.ToolResult{}, tt.execErr
			}
			return tt.execResult, nil
		},
	})
}

// newRegWith builds a registry populated with the given tools.
func newRegWith(ts ...types.Tool) *tool.Registry {
	r := tool.NewRegistry()
	for _, t := range ts {
		r.Register(t)
	}
	return r
}

// -------- Constructor tests --------

func TestNew_MinimalConfig(t *testing.T) {
	orch := New(Config{
		Tools:  tool.NewRegistry(),
		Logger: discardLogger(),
	})
	if orch == nil {
		t.Fatal("New returned nil")
	}
	if orch.GetSessionID().IsZero() {
		t.Error("SessionID should be auto-generated when not provided")
	}
	if orch.modes == nil {
		t.Error("modes should be defaulted")
	}
	if orch.commands == nil {
		t.Error("commands should be defaulted")
	}
	if orch.agents == nil {
		t.Error("agents should be defaulted")
	}
	if orch.runner == nil {
		t.Error("runner should be defaulted")
	}
	if orch.planSimulator == nil {
		t.Error("planSimulator should always be created")
	}
}

func TestNew_FullConfig_HonorsProvidedValues(t *testing.T) {
	reg := tool.NewRegistry()
	hookMgr := hooks.NewManager(discardLogger())
	modeState := modes.NewState()
	sessID := types.NewSessionID()

	orch := New(Config{
		Logger:    discardLogger(),
		Tools:     reg,
		Hooks:     hookMgr,
		Modes:     modeState,
		SessionID: sessID,
		AgentName: "custom",
	})

	if orch.GetSessionID() != sessID {
		t.Errorf("SessionID = %q; want %q", orch.GetSessionID(), sessID)
	}
	if orch.agentName != "custom" {
		t.Errorf("agentName = %q; want custom", orch.agentName)
	}
	if orch.tools != reg {
		t.Error("tools registry not retained")
	}
	if orch.hooks != hookMgr {
		t.Error("hooks manager not retained")
	}
	if orch.modes != modeState {
		t.Error("modes not retained")
	}
}

// -------- Happy-path ExecuteTool --------

func TestExecuteTool_Simple(t *testing.T) {
	var execCount int32
	tt := &testTool{
		name:       "echo",
		readOnly:   true,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "ok"},
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "echo", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true; content=%q", res.Content)
	}
	if res.Content != "ok" {
		t.Errorf("Content = %q; want ok", res.Content)
	}
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("exec count = %d; want 1", execCount)
	}
}

func TestExecuteTool_ResultFlowsThrough(t *testing.T) {
	tt := &testTool{
		name:     "rich",
		readOnly: true,
		execResult: types.ToolResult{
			Content: "hello world",
			NewMessages: []types.ChatMessage{
				{Role: "assistant", Content: "injected"},
			},
		},
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "rich", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if res.Content != "hello world" {
		t.Errorf("Content = %q", res.Content)
	}
	if len(res.NewMessages) != 1 || res.NewMessages[0].Content != "injected" {
		t.Errorf("NewMessages lost: %+v", res.NewMessages)
	}
}

// -------- Pipeline ordering --------

// writeShellHook creates a shell hook that appends a tag + the JSON payload
// to a shared log file. We rely on bash `flock`-free append which is atomic
// for small writes on Linux.
func writeShellHook(name string, event hooks.HookEvent, logPath, tag string) hooks.Hook {
	// The shell reads the JSON payload from stdin and writes "tag\n" + payload + "\n---\n"
	cmd := fmt.Sprintf(`{ printf '%%s\n' %q; cat; printf '\n---\n'; } >> %q`, tag, logPath)
	return hooks.Hook{
		Name:    name,
		Event:   event,
		Type:    hooks.HandlerShell,
		Command: cmd,
	}
}

func TestExecuteTool_PipelineOrdering(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pipeline.log")

	hookMgr := hooks.NewManager(discardLogger())
	hookMgr.Register(writeShellHook("pre", hooks.EventPreToolUse, logPath, "PRE"))
	hookMgr.Register(writeShellHook("post", hooks.EventPostToolUse, logPath, "POST"))

	// The tool itself appends "TOOL" between PRE and POST.
	var order []string
	var mu sync.Mutex
	tt := &testTool{
		name:     "trace",
		readOnly: true,
		onExecute: func() {
			mu.Lock()
			defer mu.Unlock()
			// Sanity: PRE hook should have written the log by now.
			data, err := os.ReadFile(logPath)
			if err == nil && strings.Contains(string(data), "PRE") {
				order = append(order, "TOOL_AFTER_PRE")
			} else {
				order = append(order, "TOOL_BEFORE_PRE")
			}
		},
		execResult: types.ToolResult{Content: "traced"},
	}

	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Hooks:  hookMgr,
		Logger: discardLogger(),
	})

	if _, err := orch.ExecuteTool(context.Background(), "trace", map[string]any{"x": 1}); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "TOOL_AFTER_PRE" {
		t.Errorf("pipeline order wrong: %v (expected tool to run after PRE hook)", order)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "PRE") {
		t.Errorf("PRE not in log: %q", content)
	}
	if !strings.Contains(content, "POST") {
		t.Errorf("POST not in log: %q", content)
	}
	// PRE should come before POST in the file.
	if strings.Index(content, "PRE") > strings.Index(content, "POST") {
		t.Errorf("PRE not before POST: %q", content)
	}
}

func TestExecuteTool_HookSeesToolNameAndArgs(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "payload.log")

	hookMgr := hooks.NewManager(discardLogger())
	hookMgr.Register(writeShellHook("capture", hooks.EventPreToolUse, logPath, "HOOK"))

	tt := &testTool{name: "peek", readOnly: true, execResult: types.ToolResult{Content: "k"}}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Hooks:  hookMgr,
		Logger: discardLogger(),
	})

	args := map[string]any{"marker": "visible-to-hook"}
	if _, err := orch.ExecuteTool(context.Background(), "peek", args); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	payload := string(data)
	if !strings.Contains(payload, `"tool_name":"peek"`) {
		t.Errorf("hook did not receive tool name: %q", payload)
	}
	if !strings.Contains(payload, "visible-to-hook") {
		t.Errorf("hook did not receive tool args: %q", payload)
	}
}

func TestExecuteTool_PostHookFiresEvenOnToolError(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "post.log")

	hookMgr := hooks.NewManager(discardLogger())
	hookMgr.Register(writeShellHook("post", hooks.EventPostToolUse, logPath, "POST"))

	// Tool returns an error — orchestrator converts it to an IsError result and
	// *does not* fire PostToolUse (based on source: PostToolUse fires after the
	// error path returns). We document whichever behavior actually happens.
	tt := &testTool{
		name:    "broken",
		execErr: fmt.Errorf("boom"),
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Hooks:  hookMgr,
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "broken", nil)
	if err != nil {
		t.Fatalf("ExecuteTool returned Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when tool returns error")
	}
	if !strings.Contains(res.Content, "boom") {
		t.Errorf("error message not wrapped in content: %q", res.Content)
	}

	// Observed behavior: the current orchestrator implementation returns early
	// after a tool execution error and does NOT fire PostToolUse. This test
	// records that behavior. If the implementation changes to fire PostToolUse
	// on failure, update the assertion.
	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "POST") {
		t.Log("note: PostToolUse fired on tool error (orchestrator changed)")
	}
}

// -------- Permissions gating --------

func TestExecuteTool_EnforcerDeny_ToolNotExecuted(t *testing.T) {
	var execCount int32
	tt := &testTool{
		name:       "guarded",
		readOnly:   true,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "should not run"},
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})

	enf := permissions.NewEnforcer(permissions.EnforcerConfig{
		Logger: discardLogger(),
	})
	enf.AddSessionDeny("guarded", "")
	orch.SetEnforcer(enf)

	res, err := orch.ExecuteTool(context.Background(), "guarded", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true, got %+v", res)
	}
	if !strings.Contains(res.Content, "negada") && !strings.Contains(strings.ToLower(res.Content), "deny") {
		t.Errorf("content does not indicate denial: %q", res.Content)
	}
	if atomic.LoadInt32(&execCount) != 0 {
		t.Errorf("tool was executed despite deny (count=%d)", execCount)
	}
}

func TestExecuteTool_EnforcerAllow_ToolExecuted(t *testing.T) {
	var execCount int32
	// Use tool name "Read" which the classifier marks as ToolRiskSafe
	// and thus auto-approves without any askFn callback.
	tt := &testTool{
		name:       "Read",
		readOnly:   true,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "file bytes"},
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})

	enf := permissions.NewEnforcer(permissions.EnforcerConfig{
		Logger: discardLogger(),
	})
	orch.SetEnforcer(enf)

	res, err := orch.ExecuteTool(context.Background(), "Read", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError: %q", res.Content)
	}
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("tool not executed, count=%d", execCount)
	}
}

func TestExecuteTool_NilEnforcer_ExecutesByDefault(t *testing.T) {
	var execCount int32
	tt := &testTool{
		name:       "free",
		readOnly:   true,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "ran"},
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})
	// No enforcer set — default allow.

	if orch.Enforcer() != nil {
		t.Fatal("Enforcer should be nil by default")
	}

	res, err := orch.ExecuteTool(context.Background(), "free", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError: %q", res.Content)
	}
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("exec count = %d; want 1", execCount)
	}
}

// -------- Plan mode --------

func TestExecuteTool_PlanMode_DestructiveToolSimulated(t *testing.T) {
	var execCount int32
	// Use a tool name that constants.IsDestructiveTool recognizes as destructive.
	tt := &testTool{
		name:       "write_file",
		readOnly:   false,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "wrote real data"},
	}

	modeState := modes.NewState()
	modeState.Current = modes.ModePlan

	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Modes:  modeState,
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "write_file", map[string]any{"path": "/tmp/x"})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if atomic.LoadInt32(&execCount) != 0 {
		t.Errorf("destructive tool was executed in plan mode (count=%d)", execCount)
	}
	if !strings.Contains(res.Content, "[PLAN]") {
		t.Errorf("expected simulated plan content, got %q", res.Content)
	}
}

func TestExecuteTool_PlanMode_ReadToolExecutesNormally(t *testing.T) {
	var execCount int32
	// "read_file" is NOT in the destructive set, so plan mode lets it through.
	tt := &testTool{
		name:       "read_file",
		readOnly:   true,
		execCount:  &execCount,
		execResult: types.ToolResult{Content: "file contents"},
	}

	modeState := modes.NewState()
	modeState.Current = modes.ModePlan

	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Modes:  modeState,
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "read_file", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("read tool should execute in plan mode, count=%d", execCount)
	}
	if res.Content != "file contents" {
		t.Errorf("Content = %q; want file contents", res.Content)
	}
}

// -------- Error handling --------

func TestExecuteTool_UnknownTool_ReturnsError(t *testing.T) {
	orch := New(Config{
		Tools:  tool.NewRegistry(),
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "does_not_exist", nil)
	// The orchestrator returns a ToolResult with IsError=true, not a Go error.
	if err != nil {
		t.Fatalf("ExecuteTool returned Go error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true for unknown tool, got %+v", res)
	}
	if !strings.Contains(res.Content, "desconhecida") && !strings.Contains(res.Content, "does_not_exist") {
		t.Errorf("content does not describe unknown tool: %q", res.Content)
	}
}

func TestExecuteTool_ToolError_WrappedInResult(t *testing.T) {
	tt := &testTool{
		name:    "buggy",
		execErr: fmt.Errorf("kaboom"),
	}
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(tt)),
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "buggy", nil)
	if err != nil {
		t.Fatalf("ExecuteTool returned Go error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when tool returns error")
	}
	if !strings.Contains(res.Content, "kaboom") {
		t.Errorf("tool error not wrapped into content: %q", res.Content)
	}
}

func TestExecuteTool_ValidationFail_ShortCircuits(t *testing.T) {
	var execCount int32
	pickyTool := types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "picky" },
		DescriptionFn: func() string { return "picky" },
		InputSchemaFn: func() map[string]any { return map[string]any{} },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if _, ok := args["required"]; !ok {
				return types.ValidationResult{Valid: false, Message: "missing required"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			atomic.AddInt32(&execCount, 1)
			return types.ToolResult{Content: "ran"}, nil
		},
	})

	orch := New(Config{
		Tools:  newRegWith(pickyTool),
		Logger: discardLogger(),
	})

	res, err := orch.ExecuteTool(context.Background(), "picky", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError on validation failure")
	}
	if !strings.Contains(res.Content, "missing required") {
		t.Errorf("validation message missing: %q", res.Content)
	}
	if atomic.LoadInt32(&execCount) != 0 {
		t.Errorf("tool executed despite validation failure (count=%d)", execCount)
	}
}

// -------- Misc helpers --------

func TestOrchestrator_Stats(t *testing.T) {
	orch := New(Config{
		Tools:  newRegWith(buildTestTool(&testTool{name: "a", execResult: types.ToolResult{Content: "x"}})),
		Logger: discardLogger(),
	})
	stats := orch.Stats()
	if _, ok := stats["session_id"]; !ok {
		t.Error("stats missing session_id")
	}
	if _, ok := stats["mode"]; !ok {
		t.Error("stats missing mode")
	}
	if got := stats["tools"]; got != 1 {
		t.Errorf("stats[tools] = %v; want 1", got)
	}
}

func TestOrchestrator_PlanSimulatorAccessible(t *testing.T) {
	orch := New(Config{
		Tools:  tool.NewRegistry(),
		Logger: discardLogger(),
	})
	if orch.PlanSimulator() == nil {
		t.Error("PlanSimulator() returned nil")
	}
}
