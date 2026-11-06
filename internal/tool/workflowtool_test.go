package tool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// fakeInvoker records every Invoke call and optionally returns a
// pre-programmed result/error for a given tool name.
type fakeInvoker struct {
	mu      sync.Mutex
	calls   []fakeCall
	results map[string]any
	errs    map[string]error
}

type fakeCall struct {
	tool string
	args map[string]any
}

func newFakeInvoker() *fakeInvoker {
	return &fakeInvoker{
		results: make(map[string]any),
		errs:    make(map[string]error),
	}
}

func (f *fakeInvoker) Invoke(ctx context.Context, toolName string, args map[string]any) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{tool: toolName, args: args})
	if err, ok := f.errs[toolName]; ok {
		return nil, err
	}
	if res, ok := f.results[toolName]; ok {
		return res, nil
	}
	return "ok:" + toolName, nil
}

func (f *fakeInvoker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestWorkflowTool_Define(t *testing.T) {
	store := NewWorkflowStore()
	invoker := newFakeInvoker()
	tool := NewWorkflowTool(store, invoker)

	args := map[string]any{
		"action": "define",
		"workflow": map[string]any{
			"name":        "deploy",
			"description": "fake deploy flow",
			"steps": []any{
				map[string]any{
					"tool_name": "bash",
					"args":      map[string]any{"command": "make build"},
				},
				map[string]any{
					"tool_name":     "bash",
					"args":          map[string]any{"command": "make deploy"},
					"stop_on_error": true,
				},
			},
		},
	}

	if v := tool.ValidateInput(args); !v.Valid {
		t.Fatalf("validate failed: %s", v.Message)
	}

	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, args)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("define reported error: %s", res.Content)
	}

	wf, ok := store.Get("deploy")
	if !ok {
		t.Fatal("workflow not found in store after define")
	}
	if len(wf.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(wf.Steps))
	}
	if !wf.Steps[1].StopOnError {
		t.Fatal("expected second step to have StopOnError=true")
	}
	if invoker.callCount() != 0 {
		t.Fatalf("define must not invoke any tools; got %d calls", invoker.callCount())
	}
}

func TestWorkflowTool_RunHappyPath(t *testing.T) {
	store := NewWorkflowStore()
	invoker := newFakeInvoker()
	invoker.results["bash"] = "build ok"
	invoker.results["git"] = "pushed"

	if err := store.Define(Workflow{
		Name:        "ship",
		Description: "build and push",
		Steps: []WorkflowStep{
			{ToolName: "bash", Args: map[string]any{"command": "make build"}},
			{ToolName: "git", Args: map[string]any{"action": "push"}},
		},
	}); err != nil {
		t.Fatalf("store.Define: %v", err)
	}

	tool := NewWorkflowTool(store, invoker)

	res, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{"action": "run", "name": "ship"},
	)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("run reported error: %s", res.Content)
	}

	if invoker.callCount() != 2 {
		t.Fatalf("expected 2 invoker calls, got %d", invoker.callCount())
	}
	if invoker.calls[0].tool != "bash" || invoker.calls[1].tool != "git" {
		t.Fatalf("unexpected call order: %+v", invoker.calls)
	}
	if !strings.Contains(res.Content, "build ok") || !strings.Contains(res.Content, "pushed") {
		t.Fatalf("result missing aggregated step outputs: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"steps_ran": 2`) {
		t.Fatalf("expected steps_ran:2 in result, got: %s", res.Content)
	}
}

func TestWorkflowTool_RunStopOnError(t *testing.T) {
	store := NewWorkflowStore()
	invoker := newFakeInvoker()
	invoker.errs["broken"] = errors.New("boom")

	if err := store.Define(Workflow{
		Name: "fragile",
		Steps: []WorkflowStep{
			{ToolName: "ok_tool", Args: map[string]any{"k": "v"}},
			{ToolName: "broken", StopOnError: true},
			{ToolName: "never_runs"},
		},
	}); err != nil {
		t.Fatalf("store.Define: %v", err)
	}

	tool := NewWorkflowTool(store, invoker)

	res, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{"action": "run", "name": "fragile"},
	)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true after halted workflow")
	}

	// Only 2 calls should happen: ok_tool and broken. never_runs must be skipped.
	if invoker.callCount() != 2 {
		t.Fatalf("expected 2 invoker calls (halt after broken), got %d", invoker.callCount())
	}
	for _, c := range invoker.calls {
		if c.tool == "never_runs" {
			t.Fatal("never_runs should not have been invoked")
		}
	}
	if !strings.Contains(res.Content, "boom") {
		t.Fatalf("expected error message 'boom' in result, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"halted": true`) {
		t.Fatalf("expected halted:true in result, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"steps_ran": 2`) {
		t.Fatalf("expected steps_ran:2 in result, got: %s", res.Content)
	}
}

func TestWorkflowTool_List(t *testing.T) {
	store := NewWorkflowStore()
	_ = store.Define(Workflow{Name: "alpha", Steps: []WorkflowStep{{ToolName: "bash"}}})
	_ = store.Define(Workflow{Name: "beta", Steps: []WorkflowStep{{ToolName: "bash"}}})

	tool := NewWorkflowTool(store, newFakeInvoker())

	res, err := tool.Execute(
		types.ToolContext{Context: context.Background()},
		map[string]any{"action": "list"},
	)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("list reported error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "alpha") || !strings.Contains(res.Content, "beta") {
		t.Fatalf("list output missing workflow names: %s", res.Content)
	}
	if !tool.IsReadOnly(map[string]any{"action": "list"}) {
		t.Fatal("list action should be read-only")
	}
}
