package checkpoint

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// newTestManager returns a Manager wired to a temp dir with a discard logger.
func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(dir, logger), dir
}

// writeFile is a small helper for creating a file with content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCheckpointPackage(t *testing.T) {
	// Smoke test - package compiles
	t.Log("checkpoint package loaded")
}

func TestNewManager(t *testing.T) {
	mgr, dir := newTestManager(t)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
	// The manager should have created a "checkpoints" subdir inside dir.
	sub := filepath.Join(dir, "checkpoints")
	info, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("checkpoints dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", sub)
	}
	if len(mgr.List()) != 0 {
		t.Fatalf("expected empty checkpoint list, got %d", len(mgr.List()))
	}
}

func TestCheckpoint_CreateAndList(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	f1 := filepath.Join(workDir, "a.txt")
	f2 := filepath.Join(workDir, "b.txt")
	writeFile(t, f1, "hello")
	writeFile(t, f2, "world")

	cp, err := mgr.Create("initial", []string{f1, f2}, "initial state")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if cp == nil {
		t.Fatal("Create returned nil checkpoint")
	}
	if cp.ID == "" {
		t.Error("checkpoint has empty ID")
	}
	if cp.Name != "initial" {
		t.Errorf("Name = %q, want %q", cp.Name, "initial")
	}
	if cp.Message != "initial state" {
		t.Errorf("Message = %q, want %q", cp.Message, "initial state")
	}
	if len(cp.Files) != 2 {
		t.Errorf("expected 2 files tracked, got %d", len(cp.Files))
	}
	if _, ok := cp.Files[f1]; !ok {
		t.Errorf("file %s missing from checkpoint", f1)
	}

	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("List = %d checkpoints, want 1", len(list))
	}
	if list[0].ID != cp.ID {
		t.Errorf("listed ID = %q, want %q", list[0].ID, cp.ID)
	}
}

func TestCheckpoint_RewindRestoresContent(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	target := filepath.Join(workDir, "config.txt")
	writeFile(t, target, "original")

	cp, err := mgr.Create("snap", []string{target}, "before edit")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mutate the file.
	writeFile(t, target, "modified")

	restored, err := mgr.Rewind(cp.ID)
	if err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if len(restored) == 0 {
		t.Error("expected at least one restored entry")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("file content = %q, want %q", string(got), "original")
	}
}

func TestCheckpoint_RewindDeletesCreatedFile(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	// Path that does NOT exist yet at checkpoint time.
	ghost := filepath.Join(workDir, "new.txt")

	cp, err := mgr.Create("pre-create", []string{ghost}, "file absent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Now create the file.
	writeFile(t, ghost, "should be removed")
	if _, err := os.Stat(ghost); err != nil {
		t.Fatalf("file should exist before rewind: %v", err)
	}

	if _, err := mgr.Rewind(cp.ID); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	if _, err := os.Stat(ghost); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed after rewind, err = %v", err)
	}
}

func TestCheckpoint_RewindLast(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	f := filepath.Join(workDir, "x.txt")
	writeFile(t, f, "v1")

	if _, err := mgr.Create("v1", []string{f}, "version 1"); err != nil {
		t.Fatalf("Create v1: %v", err)
	}
	writeFile(t, f, "v2")
	if _, err := mgr.Create("v2", []string{f}, "version 2"); err != nil {
		t.Fatalf("Create v2: %v", err)
	}

	// Mutate after last checkpoint.
	writeFile(t, f, "v3-dirty")

	if _, err := mgr.RewindLast(); err != nil {
		t.Fatalf("RewindLast: %v", err)
	}

	got, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("content = %q, want %q", string(got), "v2")
	}
}

func TestCheckpoint_RewindLastEmpty(t *testing.T) {
	mgr, _ := newTestManager(t)
	if _, err := mgr.RewindLast(); err == nil {
		t.Error("expected error when rewinding with no checkpoints")
	}
}

func TestCheckpoint_RewindUnknownID(t *testing.T) {
	mgr, _ := newTestManager(t)
	if _, err := mgr.Rewind("ckpt_doesnotexist"); err == nil {
		t.Error("expected error for unknown checkpoint ID")
	}
}

func TestCheckpoint_ListOrderNewestFirst(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	f := filepath.Join(workDir, "ordered.txt")
	writeFile(t, f, "a")

	cp1, err := mgr.Create("first", []string{f}, "m1")
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	// Ensure distinct millisecond-level IDs.
	writeFile(t, f, "b")
	cp2, err := mgr.Create("second", []string{f}, "m2")
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	list := mgr.List()
	if len(list) != 2 {
		t.Fatalf("List = %d, want 2", len(list))
	}
	// Newest must come first; allow equal timestamps by matching either order
	// but prefer cp2 first.
	if !list[0].Timestamp.Equal(list[1].Timestamp) {
		if list[0].ID != cp2.ID {
			t.Errorf("expected newest (%s) first, got %s", cp2.ID, list[0].ID)
		}
		if list[1].ID != cp1.ID {
			t.Errorf("expected oldest (%s) last, got %s", cp1.ID, list[1].ID)
		}
	}
}

func TestCheckpoint_MaxHistoryTrim(t *testing.T) {
	mgr, _ := newTestManager(t)
	// Shrink history to a small bound so we can exercise trimming.
	mgr.maxHistory = 3

	workDir := t.TempDir()
	f := filepath.Join(workDir, "h.txt")

	for i := 0; i < 6; i++ {
		writeFile(t, f, string(rune('a'+i)))
		if _, err := mgr.Create("step", []string{f}, "msg"); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	list := mgr.List()
	if len(list) != 3 {
		t.Errorf("after trim List = %d, want 3", len(list))
	}
}

func TestCheckpoint_Fork(t *testing.T) {
	mgr, _ := newTestManager(t)

	workDir := t.TempDir()
	f := filepath.Join(workDir, "src.txt")
	writeFile(t, f, "forked content")

	cp, err := mgr.Create("fork-base", []string{f}, "base")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	forkDir := t.TempDir()
	if err := mgr.Fork(cp.ID, forkDir); err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Fork copies snapshot files under forkDir, preserving the original path
	// layout (because the implementation uses filepath.Join(forkDir, snap.Path)).
	forkedPath := filepath.Join(forkDir, f)
	data, err := os.ReadFile(forkedPath)
	if err != nil {
		t.Fatalf("read forked file: %v", err)
	}
	if string(data) != "forked content" {
		t.Errorf("forked content = %q, want %q", string(data), "forked content")
	}
}

func TestCheckpoint_ForkUnknownID(t *testing.T) {
	mgr, _ := newTestManager(t)
	forkDir := t.TempDir()
	if err := mgr.Fork("ckpt_nope", forkDir); err == nil {
		t.Error("expected error forking unknown checkpoint")
	}
}

func TestHashContentStable(t *testing.T) {
	a := hashContent([]byte("same"))
	b := hashContent([]byte("same"))
	c := hashContent([]byte("different"))
	if a != b {
		t.Errorf("hash not stable: %q vs %q", a, b)
	}
	if a == c {
		t.Error("different inputs produced same hash")
	}
	if a == "" {
		t.Error("empty hash")
	}
}

func TestSanitizePath(t *testing.T) {
	cases := map[string]string{
		"/foo/bar.txt": "_foo_bar.txt",
		"a\\b":         "a_b",
		"C:/x":         "C__x",
		"plain":        "plain",
	}
	for in, want := range cases {
		if got := sanitizePath(in); got != want {
			t.Errorf("sanitizePath(%q) = %q, want %q", in, got, want)
		}
	}
}
