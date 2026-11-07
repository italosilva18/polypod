package orchestrator

import (
	"errors"
	"testing"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

func TestOrchestratorObservesOnlyExecutedSuccessfulTools(t *testing.T) {
	reg := newRegWith(
		buildTestTool(&testTool{name: "read_file", readOnly: true, execResult: types.ToolResult{Content: "ok"}}),
		buildTestTool(&testTool{name: "write_file", execResult: types.ToolResult{Content: "error", IsError: true}}),
		buildTestTool(&testTool{name: "edit_file", execErr: errors.New("failed")}),
	)
	orch := New(Config{Tools: reg, Logger: discardLogger()})
	ctx, trace := evolution.WithToolTrace(t.Context())
	for _, name := range []string{"read_file", "write_file", "edit_file", "unknown_tool"} {
		if _, err := orch.ExecuteTool(ctx, name, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if names := trace.Names(); len(names) != 1 || names[0] != "read_file" {
		t.Fatalf("orchestrator recorded failed or unknown calls: %v", names)
	}
	results := orch.ExecuteParallel(ctx, []tool.ToolCall{{Name: "read_file"}, {Name: "write_file"}})
	if len(results) != 2 {
		t.Fatalf("parallel execution returned %d results", len(results))
	}
	if names := trace.Names(); len(names) != 2 || names[1] != "read_file" {
		t.Fatalf("parallel path did not observe successful registered tool: %v", names)
	}
}
