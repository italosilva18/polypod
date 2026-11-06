package ai

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

// ---------- helpers ----------

// makeTool builds a minimal types.Tool with the given name and
// read-only / concurrency-safe flags. Execute just echoes back a
// fixed string so we never touch the filesystem.
func makeTool(name string, readOnly, concurrencySafe bool) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return "fake tool " + name },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "ok:" + name}, nil
		},
		IsReadOnlyFn:    func(map[string]any) bool { return readOnly },
		IsConcurrencyFn: func(map[string]any) bool { return concurrencySafe },
	})
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// ---------- ToolEvent struct & constants ----------

func TestToolEvent_Fields(t *testing.T) {
	ev := ToolEvent{
		Type:       ToolEventStart,
		ToolName:   "read_file",
		ToolID:     "call_1",
		Args:       map[string]any{"path": "main.go"},
		Result:     "",
		Error:      "",
		Elapsed:    100 * time.Millisecond,
		IsReadOnly: true,
	}

	if ev.Type != ToolEventStart {
		t.Errorf("Type = %v; want ToolEventStart", ev.Type)
	}
	if ev.ToolName != "read_file" {
		t.Errorf("ToolName = %q; want read_file", ev.ToolName)
	}
	if ev.ToolID != "call_1" {
		t.Errorf("ToolID = %q; want call_1", ev.ToolID)
	}
	if len(ev.Args) != 1 {
		t.Errorf("len(Args) = %d; want 1", len(ev.Args))
	}
	if got := ev.Args["path"]; got != "main.go" {
		t.Errorf("Args[path] = %v; want main.go", got)
	}
	if ev.Elapsed != 100*time.Millisecond {
		t.Errorf("Elapsed = %v; want 100ms", ev.Elapsed)
	}
	if !ev.IsReadOnly {
		t.Error("IsReadOnly should be true")
	}
}

func TestToolEvent_CompleteFields(t *testing.T) {
	ev := ToolEvent{
		Type:     ToolEventComplete,
		ToolName: "bash",
		ToolID:   "call_42",
		Result:   "hello world",
		Elapsed:  2 * time.Second,
	}
	if ev.Type != ToolEventComplete {
		t.Errorf("Type = %v; want ToolEventComplete", ev.Type)
	}
	if ev.Result != "hello world" {
		t.Errorf("Result = %q", ev.Result)
	}
	if ev.Error != "" {
		t.Errorf("Error should be empty; got %q", ev.Error)
	}
}

func TestToolEvent_ErrorFields(t *testing.T) {
	ev := ToolEvent{
		Type:     ToolEventError,
		ToolName: "write_file",
		ToolID:   "call_99",
		Error:    "permission denied",
	}
	if ev.Type != ToolEventError {
		t.Errorf("Type = %v; want ToolEventError", ev.Type)
	}
	if ev.Error != "permission denied" {
		t.Errorf("Error = %q", ev.Error)
	}
}

func TestToolEventType_ConstantsDistinct(t *testing.T) {
	seen := map[ToolEventType]string{}
	for name, v := range map[string]ToolEventType{
		"Start":    ToolEventStart,
		"Progress": ToolEventProgress,
		"Complete": ToolEventComplete,
		"Error":    ToolEventError,
	} {
		if prev, ok := seen[v]; ok {
			t.Errorf("duplicate ToolEventType value %d for %s and %s", v, prev, name)
		}
		seen[v] = name
	}
	if len(seen) != 4 {
		t.Errorf("expected 4 distinct constants, got %d", len(seen))
	}
}

func TestToolEventType_IotaOrdering(t *testing.T) {
	// iota starting at 0 means Start=0, Progress=1, Complete=2, Error=3
	if ToolEventStart != 0 {
		t.Errorf("ToolEventStart = %d; want 0", ToolEventStart)
	}
	if ToolEventProgress != 1 {
		t.Errorf("ToolEventProgress = %d; want 1", ToolEventProgress)
	}
	if ToolEventComplete != 2 {
		t.Errorf("ToolEventComplete = %d; want 2", ToolEventComplete)
	}
	if ToolEventError != 3 {
		t.Errorf("ToolEventError = %d; want 3", ToolEventError)
	}
}

// ---------- ToolEventCallback ----------

func TestToolEventCallback_Invocation(t *testing.T) {
	var received []ToolEvent
	var mu sync.Mutex
	cb := ToolEventCallback(func(e ToolEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	cb(ToolEvent{Type: ToolEventStart, ToolName: "a", ToolID: "1"})
	cb(ToolEvent{Type: ToolEventComplete, ToolName: "a", ToolID: "1"})

	if len(received) != 2 {
		t.Fatalf("len(received) = %d; want 2", len(received))
	}
	if received[0].Type != ToolEventStart {
		t.Errorf("received[0].Type = %v; want Start", received[0].Type)
	}
	if received[1].Type != ToolEventComplete {
		t.Errorf("received[1].Type = %v; want Complete", received[1].Type)
	}
}

func TestToolEventCallback_NilIsGuardable(t *testing.T) {
	// A nil ToolEventCallback is a common sentinel; production code must
	// check for nil before invoking. Verify the idiom works and does not
	// panic.
	var cb ToolEventCallback
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-guard panicked: %v", r)
		}
	}()
	if cb != nil {
		cb(ToolEvent{Type: ToolEventStart})
		t.Error("cb should be nil")
	}
}

func TestToolEventCallback_NilViaDispatcher(t *testing.T) {
	// Simulate the tiny dispatcher pattern production code uses.
	dispatch := func(cb ToolEventCallback, ev ToolEvent) {
		if cb != nil {
			cb(ev)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("dispatcher panicked on nil callback: %v", r)
		}
	}()
	dispatch(nil, ToolEvent{Type: ToolEventStart, ToolName: "x"})

	var count int
	dispatch(func(e ToolEvent) { count++ }, ToolEvent{Type: ToolEventComplete})
	if count != 1 {
		t.Errorf("count = %d; want 1", count)
	}
}

// ---------- StreamingToolManager.PartitionCalls ----------

func TestPartitionCalls_AllReadOnlyConcurrent(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("read_a", true, true))
	reg.Register(makeTool("read_b", true, true))
	reg.Register(makeTool("read_c", true, true))

	m := NewStreamingToolManager(reg, nil, silentLogger())
	calls := []ToolCallInfo{
		{ID: "1", Name: "read_a", Arguments: "{}"},
		{ID: "2", Name: "read_b", Arguments: "{}"},
		{ID: "3", Name: "read_c", Arguments: "{}"},
	}

	concurrent, serial := m.PartitionCalls(calls)
	if len(concurrent) != 3 {
		t.Errorf("len(concurrent) = %d; want 3", len(concurrent))
	}
	if len(serial) != 0 {
		t.Errorf("len(serial) = %d; want 0", len(serial))
	}
}

func TestPartitionCalls_AllWriteSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("write_a", false, false))
	reg.Register(makeTool("write_b", false, false))

	m := NewStreamingToolManager(reg, nil, silentLogger())
	calls := []ToolCallInfo{
		{ID: "1", Name: "write_a", Arguments: "{}"},
		{ID: "2", Name: "write_b", Arguments: "{}"},
	}

	concurrent, serial := m.PartitionCalls(calls)
	if len(concurrent) != 0 {
		t.Errorf("len(concurrent) = %d; want 0", len(concurrent))
	}
	if len(serial) != 2 {
		t.Errorf("len(serial) = %d; want 2", len(serial))
	}
}

func TestPartitionCalls_Mixed(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("read_a", true, true))
	reg.Register(makeTool("write_a", false, false))
	reg.Register(makeTool("read_b", true, true))
	reg.Register(makeTool("write_b", false, false))

	m := NewStreamingToolManager(reg, nil, silentLogger())
	calls := []ToolCallInfo{
		{ID: "1", Name: "read_a", Arguments: "{}"},
		{ID: "2", Name: "write_a", Arguments: "{}"},
		{ID: "3", Name: "read_b", Arguments: "{}"},
		{ID: "4", Name: "write_b", Arguments: "{}"},
	}

	concurrent, serial := m.PartitionCalls(calls)
	if len(concurrent) != 2 {
		t.Errorf("len(concurrent) = %d; want 2", len(concurrent))
	}
	if len(serial) != 2 {
		t.Errorf("len(serial) = %d; want 2", len(serial))
	}

	// Concurrent batch should hold the two read-only tools.
	concurrentNames := map[string]bool{}
	for _, c := range concurrent {
		concurrentNames[c.Name] = true
	}
	if !concurrentNames["read_a"] || !concurrentNames["read_b"] {
		t.Errorf("concurrent missing reads: %v", concurrentNames)
	}

	serialNames := map[string]bool{}
	for _, s := range serial {
		serialNames[s.Name] = true
	}
	if !serialNames["write_a"] || !serialNames["write_b"] {
		t.Errorf("serial missing writes: %v", serialNames)
	}
}

func TestPartitionCalls_ReadOnlyButNotConcurrencySafe(t *testing.T) {
	// A tool that's read-only but NOT concurrency-safe must go to serial,
	// per the doc comment: "concurrent" requires BOTH flags.
	reg := tool.NewRegistry()
	reg.Register(makeTool("read_unsafe", true, false))

	m := NewStreamingToolManager(reg, nil, silentLogger())
	concurrent, serial := m.PartitionCalls([]ToolCallInfo{
		{ID: "1", Name: "read_unsafe", Arguments: "{}"},
	})
	if len(concurrent) != 0 {
		t.Errorf("len(concurrent) = %d; want 0", len(concurrent))
	}
	if len(serial) != 1 {
		t.Errorf("len(serial) = %d; want 1", len(serial))
	}
}

func TestPartitionCalls_UnknownToolGoesSerial(t *testing.T) {
	reg := tool.NewRegistry()
	// Registry is empty on purpose.
	m := NewStreamingToolManager(reg, nil, silentLogger())

	concurrent, serial := m.PartitionCalls([]ToolCallInfo{
		{ID: "1", Name: "ghost_tool", Arguments: "{}"},
	})
	if len(concurrent) != 0 {
		t.Errorf("len(concurrent) = %d; want 0", len(concurrent))
	}
	if len(serial) != 1 {
		t.Errorf("len(serial) = %d; want 1", len(serial))
	}
}

func TestPartitionCalls_EmptyInput(t *testing.T) {
	reg := tool.NewRegistry()
	m := NewStreamingToolManager(reg, nil, silentLogger())
	concurrent, serial := m.PartitionCalls(nil)
	if concurrent != nil || serial != nil {
		t.Errorf("empty input should return nil/nil; got %v / %v", concurrent, serial)
	}
}

// ---------- ToolRegistryAdapter.Execute ----------

func TestToolRegistryAdapter_ExecuteValidJSON(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "echo" },
		DescriptionFn: func() string { return "echo tool" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			msg, _ := args["msg"].(string)
			return types.ToolResult{Content: "echoed:" + msg}, nil
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
	}))

	adapter := NewToolRegistryAdapter(reg, nil)
	out, err := adapter.Execute("echo", `{"msg":"hi"}`)
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if out != "echoed:hi" {
		t.Errorf("out = %q; want echoed:hi", out)
	}
}

func TestToolRegistryAdapter_ExecuteInvalidJSON(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("noop", true, true))

	adapter := NewToolRegistryAdapter(reg, nil)

	// Should NOT panic. Based on the source, malformed JSON triggers a
	// fallback parse that also fails, so the function returns an error.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute panicked on invalid JSON: %v", r)
		}
	}()

	out, err := adapter.Execute("noop", "{not-json")
	if err == nil && out == "" {
		t.Error("expected either an error or a non-empty error string")
	}
	// Either err is non-nil (current behavior) OR the output string starts
	// with "Erro". We accept both so that harmless refactors don't break
	// this test.
	if err != nil {
		if !strings.Contains(err.Error(), "parsing tool args") {
			t.Errorf("err = %v; want it to mention parsing tool args", err)
		}
	} else if !strings.HasPrefix(out, "Erro") {
		t.Errorf("out = %q; want Erro... prefix or non-nil error", out)
	}
}

func TestToolRegistryAdapter_ExecuteUnknownTool(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewToolRegistryAdapter(reg, nil)

	out, err := adapter.Execute("nonexistent", `{}`)
	// The adapter returns the error as a string with nil err (so the model
	// can see it), per tool_adapter.go lines 117-119.
	if err != nil {
		t.Fatalf("Execute returned err: %v (expected nil with error string)", err)
	}
	if !strings.Contains(out, "desconhecida") && !strings.Contains(out, "nonexistent") {
		t.Errorf("out = %q; expected it to mention unknown tool", out)
	}
}

func TestToolRegistryAdapter_ExecuteToolReturnsError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(types.BuildTool(types.ToolDef{
		NameFn:        func() string { return "boom" },
		DescriptionFn: func() string { return "always errors" },
		InputSchemaFn: func() map[string]any { return map[string]any{"type": "object"} },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "bad thing happened", IsError: true}, nil
		},
	}))

	adapter := NewToolRegistryAdapter(reg, nil)
	out, err := adapter.Execute("boom", `{}`)
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if !strings.HasPrefix(out, "Erro") {
		t.Errorf("out = %q; want Erro prefix", out)
	}
	if !strings.Contains(out, "bad thing happened") {
		t.Errorf("out = %q; want it to contain the tool's error content", out)
	}
}

func TestToolRegistryAdapter_ExecuteUsesOrchestrator(t *testing.T) {
	reg := tool.NewRegistry()
	// Registry is empty on purpose; orchestrate func should be used instead.

	var gotName string
	var gotArgs map[string]any
	orchestrate := func(name string, args map[string]any) (types.ToolResult, error) {
		gotName = name
		gotArgs = args
		return types.ToolResult{Content: "from orchestrator"}, nil
	}

	adapter := NewToolRegistryAdapter(reg, orchestrate)
	out, err := adapter.Execute("foo", `{"k":"v"}`)
	if err != nil {
		t.Fatalf("Execute returned err: %v", err)
	}
	if out != "from orchestrator" {
		t.Errorf("out = %q; want 'from orchestrator'", out)
	}
	if gotName != "foo" {
		t.Errorf("gotName = %q; want foo", gotName)
	}
	if gotArgs["k"] != "v" {
		t.Errorf("gotArgs[k] = %v; want v", gotArgs["k"])
	}
}

func TestToolRegistryAdapter_ToolDefinitionsEmpty(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("alpha", true, true))
	reg.Register(makeTool("beta", false, false))

	adapter := NewToolRegistryAdapter(reg, nil)
	defs := adapter.ToolDefinitions(nil)
	if len(defs) != 2 {
		t.Errorf("len(defs) = %d; want 2", len(defs))
	}
	for _, d := range defs {
		if d.Function == nil || d.Function.Name == "" {
			t.Errorf("def has empty name: %+v", d)
		}
	}
}

func TestToolRegistryAdapter_ToolDefinitionsFiltered(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("alpha", true, true))
	reg.Register(makeTool("beta", false, false))

	adapter := NewToolRegistryAdapter(reg, nil)
	defs := adapter.ToolDefinitions([]string{"alpha"})
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d; want 1", len(defs))
	}
	if defs[0].Function.Name != "alpha" {
		t.Errorf("defs[0].Name = %q; want alpha", defs[0].Function.Name)
	}
}

// ---------- StreamingToolManager.ExecuteToolCalls (integration-ish) ----------

func TestStreamingToolManager_ExecuteToolCalls_ParallelAndSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(makeTool("read_a", true, true))
	reg.Register(makeTool("write_a", false, false))

	m := NewStreamingToolManager(reg, nil, silentLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := m.ExecuteToolCalls(ctx, []ToolCallInfo{
		{ID: "1", Name: "read_a", Arguments: "{}"},
		{ID: "2", Name: "write_a", Arguments: "{}"},
	})

	if len(results) != 2 {
		t.Fatalf("len(results) = %d; want 2", len(results))
	}
	// Concurrent results come first, then serial.
	if results[0].ID != "1" || results[0].Name != "read_a" {
		t.Errorf("results[0] = %+v; want read_a", results[0])
	}
	if results[1].ID != "2" || results[1].Name != "write_a" {
		t.Errorf("results[1] = %+v; want write_a", results[1])
	}
	for _, r := range results {
		if r.IsError {
			t.Errorf("unexpected error on %s: %s", r.Name, r.Content)
		}
	}

	total, done := m.Stats()
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	if done != 2 {
		t.Errorf("done = %d; want 2", done)
	}
}

func TestStreamingToolManager_ExecuteToolCalls_EmptyInput(t *testing.T) {
	reg := tool.NewRegistry()
	m := NewStreamingToolManager(reg, nil, silentLogger())
	results := m.ExecuteToolCalls(context.Background(), nil)
	if results != nil {
		t.Errorf("results = %v; want nil", results)
	}
}
