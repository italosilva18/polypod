package turncheckpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffTurnUsesSnapshotAndWarnsOnLaterChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.go")
	before := []byte("package p\nfunc old() {}\n")
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(root, "checkpoints"), nil)
	ctx := m.BeginTurn(context.Background(), "session", "turn")
	if err := m.CaptureBefore(ctx, path, before, true); err != nil {
		t.Fatal(err)
	}
	if !m.HasFiles(ctx) {
		t.Fatal("captured write was not visible to turn verification")
	}
	if err := os.WriteFile(path, []byte("package p\nfunc fresh() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishTurn(ctx); err != nil {
		t.Fatal(err)
	}
	diffs, err := m.DiffTurn("session", "turn")
	if err != nil || len(diffs) != 1 || !strings.Contains(diffs[0].Patch, "+func fresh()") {
		t.Fatalf("turn diff = %+v, %v", diffs, err)
	}
	if err := os.WriteFile(path, []byte("package p\nfunc later() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diffs, err = m.DiffTurn("session", "turn")
	if err != nil || len(diffs) != 1 || diffs[0].Warning == "" {
		t.Fatalf("later change warning missing: %+v, %v", diffs, err)
	}
}
