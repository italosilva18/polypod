package watcher

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatcherRealTimeAddedModifiedRemovedAndBuffer(t *testing.T) {
	dir := t.TempDir()
	initialFile := filepath.Join(dir, "existing.go")
	if err := os.WriteFile(initialFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Paths = []string{dir}
	w := New(cfg, slog.Default())
	buf := NewExternalChangeBuffer(32)
	w.OnEvent(func(ev FileChangeEvent) {
		buf.Record(ev.Path, ev.Op)
	})

	// Baseline scan
	w.scan()

	// 1. Add a new file and modify the existing file
	addedFile := filepath.Join(dir, "new_feature.go")
	if err := os.WriteFile(addedFile, []byte("package main\nfunc feature() {}\n"), 0644); err != nil {
		t.Fatalf("write added: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(initialFile, []byte("package main\n// edited externally\n"), 0644); err != nil {
		t.Fatalf("write modified: %v", err)
	}
	_ = os.Chtimes(initialFile, future, future)

	w.poll()

	promptBlock := buf.DrainFormatted()
	if !strings.Contains(promptBlock, "new_feature.go (adicionado)") {
		t.Fatalf("expected added file in formatted block, got:\n%s", promptBlock)
	}
	if !strings.Contains(promptBlock, "existing.go (modificado)") {
		t.Fatalf("expected modified file in formatted block, got:\n%s", promptBlock)
	}

	// Buffer should be empty right after DrainFormatted
	if afterDrain := buf.DrainFormatted(); afterDrain != "" {
		t.Fatalf("expected empty buffer after drain, got %q", afterDrain)
	}

	// 2. Remove/unlink addedFile and verify FileOpRemoved is emitted
	if err := os.Remove(addedFile); err != nil {
		t.Fatalf("remove: %v", err)
	}
	w.poll()

	removedBlock := buf.DrainFormatted()
	if !strings.Contains(removedBlock, "new_feature.go (removido)") {
		t.Fatalf("expected removed file in formatted block, got:\n%s", removedBlock)
	}
}
