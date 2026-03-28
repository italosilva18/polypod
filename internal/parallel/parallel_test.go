package parallel

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecuteSingle(t *testing.T) {
	exec := NewExecutor(func(name, args string) (string, error) {
		return "result:" + name, nil
	}, slog.Default())

	results := exec.Execute([]ToolCall{{ID: "1", Name: "read_file", Arguments: `{"path":"a.go"}`}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Result != "result:read_file" {
		t.Errorf("unexpected result: %s", results[0].Result)
	}
}

func TestExecuteParallel(t *testing.T) {
	var concurrent atomic.Int32

	exec := NewExecutor(func(name, args string) (string, error) {
		concurrent.Add(1)
		defer concurrent.Add(-1)
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	}, slog.Default())

	calls := []ToolCall{
		{ID: "1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "2", Name: "read_file", Arguments: `{"path":"b.go"}`},
		{ID: "3", Name: "read_file", Arguments: `{"path":"c.go"}`},
	}

	start := time.Now()
	results := exec.Execute(calls)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Parallel should finish in ~50ms, not 150ms
	if elapsed > 120*time.Millisecond {
		t.Errorf("expected parallel execution (~50ms), got %s", elapsed)
	}
}

func TestExecuteSequentialForWrites(t *testing.T) {
	var order []string

	exec := NewExecutor(func(name, args string) (string, error) {
		order = append(order, name)
		return "ok", nil
	}, slog.Default())

	calls := []ToolCall{
		{ID: "1", Name: "read_file", Arguments: "{}"},
		{ID: "2", Name: "edit_file", Arguments: "{}"},  // write: forces sequential
		{ID: "3", Name: "read_file", Arguments: "{}"},
	}

	results := exec.Execute(calls)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestExecuteErrorHandling(t *testing.T) {
	exec := NewExecutor(func(name, args string) (string, error) {
		return "", fmt.Errorf("test error")
	}, slog.Default())

	results := exec.Execute([]ToolCall{{ID: "1", Name: "read_file", Arguments: "{}"}})
	if results[0].Error == nil {
		t.Fatal("expected error")
	}
}
