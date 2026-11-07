package ai

import (
	"context"
	"testing"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

func TestFallbackToolPathsObserveOneSuccessfulExecution(t *testing.T) {
	reg := tool.NewRegistry()
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "read_file" },
		ExecuteFn: func(types.ToolContext, map[string]any) (types.ToolResult, error) {
			return types.ToolResult{Content: "ok"}, nil
		},
	})
	ctx, trace := evolution.WithToolTrace(t.Context())
	if _, err := executeSkillResult(ctx, NewToolRegistryAdapter(reg, nil), "read_file", `{}`); err != nil {
		t.Fatal(err)
	}
	manager := NewStreamingToolManager(reg, nil, silentLogger())
	result := manager.executeSingle(ctx, ToolCallInfo{ID: "call-1", Name: "read_file", Arguments: `{}`})
	if result.IsError {
		t.Fatalf("direct streaming tool failed: %+v", result)
	}
	if names := trace.Names(); len(names) != 2 {
		t.Fatalf("fallback paths recorded %d executions, want 2: %v", len(names), names)
	}
}

func TestContextAdapterLeavesTraceToOrchestrator(t *testing.T) {
	reg := tool.NewRegistry()
	adapter := NewToolRegistryAdapterWithContext(reg,
		func(ctx context.Context, name string, _ map[string]any) (types.ToolResult, error) {
			evolution.ObserveTool(ctx, name)
			return types.ToolResult{Content: "ok"}, nil
		})
	ctx, trace := evolution.WithToolTrace(t.Context())
	if _, err := executeSkillResult(ctx, adapter, "read_file", `{}`); err != nil {
		t.Fatal(err)
	}
	if names := trace.Names(); len(names) != 1 || names[0] != "read_file" {
		t.Fatalf("context adapter double-recorded tool: %v", names)
	}
}
