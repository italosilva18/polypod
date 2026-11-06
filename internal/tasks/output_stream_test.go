package tasks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputStream_WriteThenReadFromOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "polypod_task_test.log")

	s, err := NewOutputStream(path)
	if err != nil {
		t.Fatalf("NewOutputStream: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Initial offset should be 0 for a freshly created file.
	if got := s.Offset(); got != 0 {
		t.Errorf("initial offset = %d; want 0", got)
	}

	// Write first chunk.
	first := []byte("hello ")
	if n, err := s.Write(first); err != nil || n != len(first) {
		t.Fatalf("Write first: n=%d err=%v", n, err)
	}

	// Read from offset 0 — should yield the first chunk.
	data, newOffset, err := s.ReadFrom(0)
	if err != nil {
		t.Fatalf("ReadFrom(0): %v", err)
	}
	if !bytes.Equal(data, first) {
		t.Errorf("first read = %q; want %q", data, first)
	}
	if newOffset != int64(len(first)) {
		t.Errorf("new offset = %d; want %d", newOffset, len(first))
	}

	// Append a second chunk.
	second := []byte("world")
	if _, err := s.Write(second); err != nil {
		t.Fatalf("Write second: %v", err)
	}

	// Reading from the previous offset must return ONLY the delta.
	delta, afterOffset, err := s.ReadFrom(newOffset)
	if err != nil {
		t.Fatalf("ReadFrom(newOffset): %v", err)
	}
	if !bytes.Equal(delta, second) {
		t.Errorf("delta = %q; want %q", delta, second)
	}
	if afterOffset != int64(len(first)+len(second)) {
		t.Errorf("afterOffset = %d; want %d", afterOffset, len(first)+len(second))
	}

	// Reading from an offset at/past the end yields no data.
	tail, tailOffset, err := s.ReadFrom(afterOffset)
	if err != nil {
		t.Fatalf("ReadFrom(afterOffset): %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("tail = %q; want empty", tail)
	}
	if tailOffset != afterOffset {
		t.Errorf("tailOffset = %d; want %d", tailOffset, afterOffset)
	}

	// Sanity: the underlying file on disk contains the full content.
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if !bytes.Equal(disk, append(first, second...)) {
		t.Errorf("disk = %q; want %q", disk, append(first, second...))
	}

	// File permissions should be 0600 (read/write owner only).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode&0o177 != 0 {
		t.Errorf("file mode = %#o; want group/other bits clear", mode)
	}
}

func TestOutputStream_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close.log")

	s, err := NewOutputStream(path)
	if err != nil {
		t.Fatalf("NewOutputStream: %v", err)
	}
	if _, err := s.Write([]byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// First Close should succeed.
	if err := s.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second Close should be a no-op (no error, no panic).
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// Third Close also fine.
	if err := s.Close(); err != nil {
		t.Errorf("third Close: %v", err)
	}

	// Writes after Close must fail (but not panic).
	if _, err := s.Write([]byte("x")); err == nil {
		t.Error("Write after Close: expected error, got nil")
	}
}

func TestTaskOutputPath_Format(t *testing.T) {
	base := "/var/state/polypod"
	id := "a1b2c3d4"
	got := TaskOutputPath(base, id)
	want := filepath.Join(base, "polypod_task_a1b2c3d4.log")
	if got != want {
		t.Errorf("TaskOutputPath = %q; want %q", got, want)
	}

	// Verify the filename pattern matches the documented format.
	if !strings.HasPrefix(filepath.Base(got), "polypod_task_") {
		t.Errorf("base prefix wrong: %q", filepath.Base(got))
	}
	if !strings.HasSuffix(got, ".log") {
		t.Errorf("suffix wrong: %q", got)
	}
	if !strings.Contains(got, id) {
		t.Errorf("path missing task id: %q", got)
	}

	// Empty baseDir should fall back to os.TempDir(), not produce a path
	// with a leading separator.
	fallback := TaskOutputPath("", "zz")
	if fallback == "" {
		t.Error("fallback path is empty")
	}
	if !strings.HasSuffix(fallback, "polypod_task_zz.log") {
		t.Errorf("fallback = %q; want suffix polypod_task_zz.log", fallback)
	}

	// DefaultTaskOutputBaseDir must return a usable, non-empty directory.
	// We point XDG_STATE_HOME at a temp dir so the test does not touch
	// the real user state directory.
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	baseDir := DefaultTaskOutputBaseDir()
	wantBase := filepath.Join(tmp, "polypod", "tasks")
	if baseDir != wantBase {
		t.Errorf("DefaultTaskOutputBaseDir = %q; want %q", baseDir, wantBase)
	}
	if info, err := os.Stat(baseDir); err != nil {
		t.Errorf("base dir not created: %v", err)
	} else if !info.IsDir() {
		t.Errorf("base path is not a directory")
	}
}
