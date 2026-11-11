package turncheckpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAICheckpointPrivateRoundTripAndPendingBoundary(t *testing.T) {
	manager := New(t.TempDir(), nil)
	transcript, _ := json.Marshal([]map[string]string{{"role": "user", "content": "hello"}})
	cp := &AICheckpoint{Version: AIVersion, SessionID: "branch", TurnID: "turn", Format: "provider", Model: "model", Messages: transcript, Phase: AIPhaseTools, PendingToolCallID: "write-1"}
	if err := manager.SaveAI(cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.LoadAI("branch", "turn")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PendingToolCallID != "write-1" || loaded.ValidateResume() == nil {
		t.Fatalf("ambiguous tool became resumable: %+v", loaded)
	}
	info, err := os.Stat(manager.aiPath("branch", "turn"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("checkpoint mode = %o, want 600", info.Mode().Perm())
	}
	loaded.PendingToolCallID = ""
	loaded.Phase = AIPhaseModel
	if err := manager.SaveAI(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded, err = manager.LoadAI("branch", "turn"); err != nil || loaded.ValidateResume() != nil {
		t.Fatalf("safe model boundary should resume: %+v, %v", loaded, err)
	}
}

func TestResumeTurnPreservesFileRewindSnapshots(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.txt")
	second := filepath.Join(root, "second.txt")
	if err := os.WriteFile(first, []byte("old-first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("old-second"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := New(filepath.Join(root, "checkpoints"), nil)
	ctx := manager.BeginTurn(context.Background(), "session", "turn")
	if err := manager.CaptureBefore(ctx, first, []byte("old-first"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("new-first"), 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate a process restart without FinishTurn. The second edit should
	// join the original manifest, rather than replacing the first snapshot.
	restarted := New(filepath.Join(root, "checkpoints"), nil)
	ctx, err := restarted.ResumeTurn(context.Background(), "session", "turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.CaptureBefore(ctx, second, []byte("old-second"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("new-second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.RestoreTurns(context.Background(), "session", []string{"turn"})
	if err != nil || len(result.Restored) != 2 {
		t.Fatalf("rewind after resume: %+v, %v", result, err)
	}
	for path, want := range map[string]string{first: "old-first", second: "old-second"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestFailedResumeDoesNotHideExternalFileConflict(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(root, "checkpoints"), nil)
	ctx := m.BeginTurn(context.Background(), "session", "turn")
	if err := m.CaptureBefore(ctx, path, []byte("before"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("agent wrote"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := New(filepath.Join(root, "checkpoints"), nil)
	ctx, err := restarted.ResumeTurn(context.Background(), "session", "turn")
	if err != nil {
		t.Fatal(err)
	}
	// The model fails before any resumed file tool calls CaptureBefore.
	if err := restarted.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.RestoreTurns(context.Background(), "session", []string{"turn"})
	if err != nil || len(result.Conflicts) != 1 {
		t.Fatalf("external change was hidden by failed resume: %+v, %v", result, err)
	}
}

func TestResumedCaptureDoesNotHidePreexistingExternalEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(root, "checkpoints"), nil)
	ctx := m.BeginTurn(context.Background(), "session", "turn")
	if err := m.CaptureBefore(ctx, path, []byte("before"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("agent wrote"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := New(filepath.Join(root, "checkpoints"), nil)
	ctx, err := restarted.ResumeTurn(context.Background(), "session", "turn")
	if err != nil {
		t.Fatal(err)
	}
	// A resumed file tool reads the external content but fails before writing.
	if err := restarted.CaptureBefore(ctx, path, []byte("external edit"), true); err != nil {
		t.Fatal(err)
	}
	if err := restarted.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.RestoreTurns(context.Background(), "session", []string{"turn"})
	if err != nil || len(result.Conflicts) != 1 {
		t.Fatalf("external edit was hidden by resumed capture: %+v, %v", result, err)
	}
}
