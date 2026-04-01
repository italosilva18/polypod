package diffsandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxStageAndApply(t *testing.T) {
	dir := t.TempDir()

	sb := New(true)

	// Stage a create
	path := filepath.Join(dir, "new.txt")
	sb.StageCreate(path, []byte("hello world"))

	if sb.Pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", sb.Pending())
	}

	// File should NOT exist yet
	if _, err := os.Stat(path); err == nil {
		t.Fatal("file should not exist before apply")
	}

	// Apply
	applied, err := sb.Apply()
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied, got %d", len(applied))
	}

	// File should exist now
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file should exist after apply: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q", string(data))
	}

	// Pending should be 0
	if sb.Pending() != 0 {
		t.Errorf("pending should be 0 after apply, got %d", sb.Pending())
	}
}

func TestSandboxReject(t *testing.T) {
	sb := New(true)
	sb.StageCreate("/tmp/test-reject.txt", []byte("data"))
	sb.StageCreate("/tmp/test-reject2.txt", []byte("data2"))

	n := sb.Reject()
	if n != 2 {
		t.Errorf("rejected %d, want 2", n)
	}
	if sb.Pending() != 0 {
		t.Error("pending should be 0 after reject")
	}
}

func TestSandboxFormatDiff(t *testing.T) {
	sb := New(true)
	sb.StageCreate("new.go", []byte("package main\n\nfunc main() {}\n"))
	sb.StageEdit("old.go", []byte("old"), []byte("new"), "old content", "new content")

	diff := sb.FormatDiff()
	if diff == "" {
		t.Fatal("diff should not be empty")
	}
	if sb.Pending() != 2 {
		t.Errorf("pending = %d, want 2", sb.Pending())
	}
}

func TestSandboxDisabled(t *testing.T) {
	sb := New(false)
	if sb.IsEnabled() {
		t.Error("should be disabled")
	}
	sb.Enable()
	if !sb.IsEnabled() {
		t.Error("should be enabled after Enable()")
	}
	sb.Disable()
	if sb.IsEnabled() {
		t.Error("should be disabled after Disable()")
	}
}
