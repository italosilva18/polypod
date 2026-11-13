package scheduler

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestEvaluateGateModes(t *testing.T) {
	ctx := context.Background()

	// 1. exit_zero: passes on 0, blocks on non-zero
	pass, _, err := EvaluateGate(ctx, &Task{
		GateCommand: "true",
		GateOn:      GateOnExitZero,
	})
	if err != nil || !pass {
		t.Fatalf("expected exit_zero 'true' to pass, got pass=%v err=%v", pass, err)
	}

	pass, _, err = EvaluateGate(ctx, &Task{
		GateCommand: "false",
		GateOn:      GateOnExitZero,
	})
	if err != nil || pass {
		t.Fatalf("expected exit_zero 'false' to block, got pass=%v err=%v", pass, err)
	}

	// 2. non_empty: blocks when stdout is blank, passes when non-empty
	pass, _, err = EvaluateGate(ctx, &Task{
		GateCommand: "printf '   \\n'",
		GateOn:      GateOnNonEmpty,
	})
	if err != nil || pass {
		t.Fatalf("expected non_empty whitespace output to block, got pass=%v err=%v", pass, err)
	}

	pass, out, err := EvaluateGate(ctx, &Task{
		GateCommand: "printf ' M internal/ai/client.go\\n'",
		GateOn:      GateOnNonEmpty,
	})
	if err != nil || !pass || out == "" {
		t.Fatalf("expected non_empty dirty status to pass, got pass=%v out=%q err=%v", pass, out, err)
	}
}

func TestScheduledTaskSkipsAndWakesOnPreFlightGate(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "scheduler.json")
	flagFile := filepath.Join(dir, "dirty.flag")

	s := New(stateFile, slog.Default())
	var llmCalls atomic.Int32
	s.SetPromptRunner(func(ctx context.Context, task *Task) (string, error) {
		llmCalls.Add(1)
		return "analise concluida", nil
	})

	if err := s.AddTask(&Task{
		ID:          "git_watch",
		Name:        "Vigiar mudancas",
		Schedule:    "@every 1m",
		Prompt:      "Revise os arquivos alterados",
		GateCommand: "test -f " + flagFile + " && echo dirty || true",
		GateOn:      GateOnNonEmpty,
		Enabled:     true,
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	// Force task due now (flagFile does NOT exist -> empty stdout -> must skip at 0 LLM calls)
	s.mu.Lock()
	tk := s.tasks["git_watch"]
	tk.NextRunAt = time.Now().Add(-2 * time.Second)
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s.runTaskAt(tk, time.Now(), false)

	if got := llmCalls.Load(); got != 0 {
		t.Fatalf("expected 0 LLM calls when gate output is empty, got %d", got)
	}
	snap, ok := s.GetTask("git_watch")
	if !ok {
		t.Fatalf("task disappeared")
	}
	if snap.SkippedCount != 1 || snap.LastStatus != "skipped_gate" {
		t.Fatalf("expected SkippedCount=1 and status=skipped_gate, got skipped=%d status=%q", snap.SkippedCount, snap.LastStatus)
	}

	// Now create flagFile so gate outputs "dirty", mark due again, and verify LLM wakes!
	if pass, _, _ := EvaluateGate(context.Background(), &Task{GateCommand: "touch " + flagFile}); !pass {
		t.Fatalf("touch flagFile failed")
	}
	s.mu.Lock()
	tk = s.tasks["git_watch"]
	tk.NextRunAt = time.Now().Add(-2 * time.Second)
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	s.runTaskAt(tk, time.Now(), false)

	if got := llmCalls.Load(); got != 1 {
		t.Fatalf("expected 1 LLM call after gate became non-empty, got %d", got)
	}
	snap, _ = s.GetTask("git_watch")
	if snap.RunCount != 1 || snap.LastStatus != "success" {
		t.Fatalf("expected RunCount=1 and status=success, got runs=%d status=%q", snap.RunCount, snap.LastStatus)
	}
}
