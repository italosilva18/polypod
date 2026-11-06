package watcher

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newTestLogger returns a logger that discards all output, suitable for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeFile is a small helper that writes content to a path, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// touchFile updates the mtime of a file to "now + delta" so the watcher
// reliably detects a change even on filesystems with coarse mtime resolution.
func touchFile(t *testing.T, path string, delta time.Duration) {
	t.Helper()
	now := time.Now().Add(delta)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Paths) == 0 {
		t.Errorf("expected default Paths to be non-empty")
	}
	if cfg.Paths[0] != "." {
		t.Errorf("expected default path to be '.', got %q", cfg.Paths[0])
	}
	if len(cfg.Extensions) == 0 {
		t.Errorf("expected default Extensions to be non-empty")
	}
	if cfg.Interval <= 0 {
		t.Errorf("expected positive Interval, got %v", cfg.Interval)
	}

	// A few common extensions we expect to be present.
	wantExts := []string{".go", ".py", ".js", ".ts"}
	for _, want := range wantExts {
		found := false
		for _, ext := range cfg.Extensions {
			if ext == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected default extensions to include %q", want)
		}
	}

	// IgnoreDirs should at least include .git and node_modules.
	wantIgnore := []string{".git", "node_modules"}
	for _, want := range wantIgnore {
		found := false
		for _, d := range cfg.IgnoreDirs {
			if d == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected default IgnoreDirs to include %q", want)
		}
	}
}

func TestNew(t *testing.T) {
	cfg := DefaultConfig()
	w := New(cfg, newTestLogger())

	if w == nil {
		t.Fatal("New returned nil")
	}
	if w.states == nil {
		t.Error("expected states map to be initialized")
	}
	if len(w.config.Paths) == 0 {
		t.Error("expected config to be stored")
	}
}

func TestMatchesExtension(t *testing.T) {
	w := New(WatchConfig{Extensions: []string{".go", ".py"}}, newTestLogger())

	cases := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"app.py", true},
		{"README.md", false},
		{"binary", false},
		{"sub/dir/file.go", true},
		{"sub/dir/file.txt", false},
	}
	for _, c := range cases {
		got := w.matchesExtension(c.path)
		if got != c.want {
			t.Errorf("matchesExtension(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestAICommentPattern(t *testing.T) {
	cases := []struct {
		line     string
		matches  bool
		wantType string
		wantText string
	}{
		{"// AI! refactor this function", true, "!", "refactor this function"},
		{"// AI? what does this do", true, "?", "what does this do"},
		{"// AI: explain this", true, ":", "explain this"},
		{"# AI! python style trigger", true, "!", "python style trigger"},
		{"# AI: instruction", true, ":", "instruction"},
		{"// not an AI comment", false, "", ""},
		{"// AI no separator", false, "", ""},
		{"plain text", false, "", ""},
	}
	for _, c := range cases {
		m := AICommentPattern.FindStringSubmatch(c.line)
		if c.matches {
			if len(m) < 3 {
				t.Errorf("expected match for %q, got none", c.line)
				continue
			}
			if m[1] != c.wantType {
				t.Errorf("line %q: type = %q, want %q", c.line, m[1], c.wantType)
			}
			if m[2] != c.wantText && m[2] != c.wantText+" " {
				// Allow trailing whitespace tolerance.
				if len(m[2]) < len(c.wantText) || m[2][:len(c.wantText)] != c.wantText {
					t.Errorf("line %q: prompt = %q, want %q", c.line, m[2], c.wantText)
				}
			}
		} else if len(m) >= 3 {
			t.Errorf("expected no match for %q, got %v", c.line, m)
		}
	}
}

func TestWatchDetectsChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	writeFile(t, file, "package main\n")
	// Make sure the initial mtime is well in the past so subsequent
	// modifications are unambiguously newer.
	touchFile(t, file, -2*time.Second)

	cfg := WatchConfig{
		Paths:      []string{dir},
		Extensions: []string{".go"},
		Interval:   20 * time.Millisecond,
	}
	w := New(cfg, newTestLogger())

	var (
		mu      sync.Mutex
		changed []string
	)
	w.OnChange(func(path string) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, path)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Watch(ctx)
		close(done)
	}()

	// Give the watcher one tick to perform its initial scan.
	time.Sleep(60 * time.Millisecond)

	// Modify the file with a clearly newer mtime.
	writeFile(t, file, "package main\n// updated\n")
	touchFile(t, file, 1*time.Second)

	// Poll for the change up to the context deadline.
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(changed)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(changed) == 0 {
		t.Fatal("expected at least one change event")
	}
	if filepath.Base(changed[0]) != "main.go" {
		t.Errorf("expected main.go, got %s", changed[0])
	}
}

func TestWatchIgnoresUntrackedExtensions(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "code.go")
	ignored := filepath.Join(dir, "notes.md")
	writeFile(t, tracked, "package main\n")
	writeFile(t, ignored, "hello\n")
	touchFile(t, tracked, -2*time.Second)
	touchFile(t, ignored, -2*time.Second)

	cfg := WatchConfig{
		Paths:      []string{dir},
		Extensions: []string{".go"},
		Interval:   20 * time.Millisecond,
	}
	w := New(cfg, newTestLogger())

	var (
		mu      sync.Mutex
		changed []string
	)
	w.OnChange(func(path string) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, path)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Watch(ctx)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)

	// Touch the .md file – should NOT trigger a change.
	writeFile(t, ignored, "hello updated\n")
	touchFile(t, ignored, 1*time.Second)

	time.Sleep(200 * time.Millisecond)

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	for _, p := range changed {
		if filepath.Ext(p) != ".go" {
			t.Errorf("watcher reported change for non-go file: %s", p)
		}
	}
}

func TestWatchIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	ignoredDir := filepath.Join(dir, "vendor")
	if err := os.Mkdir(ignoredDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	ignoredFile := filepath.Join(ignoredDir, "lib.go")
	writeFile(t, ignoredFile, "package vendor\n")
	touchFile(t, ignoredFile, -2*time.Second)

	cfg := WatchConfig{
		Paths:      []string{dir},
		Extensions: []string{".go"},
		IgnoreDirs: []string{"vendor"},
		Interval:   20 * time.Millisecond,
	}
	w := New(cfg, newTestLogger())

	var (
		mu      sync.Mutex
		changed []string
	)
	w.OnChange(func(path string) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, path)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Watch(ctx)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)

	// Modify a file inside the ignored directory.
	writeFile(t, ignoredFile, "package vendor\n// changed\n")
	touchFile(t, ignoredFile, 1*time.Second)

	time.Sleep(200 * time.Millisecond)

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 0 {
		t.Errorf("expected no changes from ignored dir, got %v", changed)
	}
}

func TestWatchTriggerCallback(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "code.go")
	writeFile(t, file, "package main\n\nfunc f() {}\n")
	touchFile(t, file, -2*time.Second)

	cfg := WatchConfig{
		Paths:      []string{dir},
		Extensions: []string{".go"},
		Interval:   20 * time.Millisecond,
	}
	w := New(cfg, newTestLogger())

	var (
		mu       sync.Mutex
		triggers []Trigger
	)
	w.OnTrigger(func(tr Trigger) {
		mu.Lock()
		defer mu.Unlock()
		triggers = append(triggers, tr)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = w.Watch(ctx)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)

	// Add an AI trigger comment to the file.
	writeFile(t, file, "package main\n\n// AI! refactor this\nfunc f() {}\n")
	touchFile(t, file, 1*time.Second)

	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(triggers)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(triggers) == 0 {
		t.Fatal("expected at least one trigger event")
	}
	tr := triggers[0]
	if tr.Type != "!" {
		t.Errorf("trigger type = %q, want %q", tr.Type, "!")
	}
	if tr.Prompt != "refactor this" {
		t.Errorf("trigger prompt = %q, want %q", tr.Prompt, "refactor this")
	}
	if filepath.Base(tr.File) != "code.go" {
		t.Errorf("trigger file = %q, want code.go", tr.File)
	}
	if tr.Line <= 0 {
		t.Errorf("trigger line = %d, want > 0", tr.Line)
	}
	if tr.Content == "" {
		t.Error("trigger content should include surrounding context")
	}
}

func TestWatchContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")

	cfg := WatchConfig{
		Paths:      []string{dir},
		Extensions: []string{".go"},
		Interval:   50 * time.Millisecond,
	}
	w := New(cfg, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Watch(ctx)
	}()

	// Let it run briefly, then cancel.
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned error on cancel: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Watch did not return after context cancellation")
	}
}

func TestReadContext(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	writeFile(t, file, "line1\nline2\nline3\nline4\nline5\nline6\nline7\n")

	// Ask for line 4 with radius 1 -> should include lines 3,4,5.
	got := readContext(file, 4, 1)
	if got == "" {
		t.Fatal("readContext returned empty string")
	}
	for _, want := range []string{"line3", "line4", "line5"} {
		if !contains(got, want) {
			t.Errorf("readContext output missing %q; got:\n%s", want, got)
		}
	}
	if contains(got, "line1") || contains(got, "line7") {
		t.Errorf("readContext should not include lines outside radius; got:\n%s", got)
	}

	// Edge cases: line near top and bottom of file should not panic.
	if s := readContext(file, 1, 5); s == "" {
		t.Error("readContext at top returned empty")
	}
	if s := readContext(file, 7, 5); s == "" {
		t.Error("readContext at bottom returned empty")
	}

	// Nonexistent file returns empty string, not panic.
	if s := readContext(filepath.Join(dir, "nope.txt"), 1, 1); s != "" {
		t.Errorf("expected empty for missing file, got %q", s)
	}
}

// contains is a tiny substring check that avoids importing strings just for this.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
