package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/costa/polypod/internal/audit"
	"github.com/costa/polypod/internal/types"
)

func TestToolAuditRecordsEffectsWithoutArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	store, err := audit.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var count int32
	reg := newRegWith(buildTestTool(&testTool{name: "sample", execCount: &count,
		execResult: types.ToolResult{Content: "private response"}}))
	orch := New(Config{Logger: discardLogger(), Tools: reg})
	orch.SetAuditStore(store)
	result, err := orch.ExecuteTool(context.Background(), "sample", map[string]any{"password": "private argument"})
	if err != nil || result.IsError || atomic.LoadInt32(&count) != 1 {
		t.Fatalf("tool result=%+v, err=%v, count=%d", result, err, count)
	}
	events, err := store.Recent(context.Background(), 10)
	if err != nil || len(events) != 2 || events[0].Status != "started" || events[1].Status != "completed" {
		t.Fatalf("events=%+v, err=%v", events, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private argument") || strings.Contains(string(raw), "private response") {
		t.Fatal("audit trail included tool input or output")
	}
}
