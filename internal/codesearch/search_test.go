package codesearch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeEmbedder struct{ calls int }

func (e *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.calls++
	result := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(strings.ToLower(text), "authentication") {
			result[i] = []float64{1, 0}
		} else {
			result[i] = []float64{0, 1}
		}
	}
	return result, nil
}

type gatedEmbedder struct {
	mu          sync.Mutex
	chunkCalls  int
	blockedName string
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
}

func (e *gatedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) > 0 && strings.Contains(texts[0], ".go\n") {
		e.mu.Lock()
		e.chunkCalls++
		e.mu.Unlock()
	}
	if len(texts) > 0 && strings.Contains(texts[0], e.blockedName) {
		e.startedOnce.Do(func() { close(e.started) })
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	result := make([][]float64, len(texts))
	for i := range result {
		result[i] = []float64{1, 0}
	}
	return result, nil
}

func prepareSearchProject(t *testing.T, name, code string) string {
	t.Helper()
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(root, true); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSearchDoesNotSerializeUnrelatedEmbeddings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	slow := prepareSearchProject(t, "slow.go", "package p\nfunc slowSymbol() {}\n")
	fast := prepareSearchProject(t, "fast.go", "package p\nfunc fastSymbol() {}\n")
	embedder := &gatedEmbedder{blockedName: "slowSymbol", started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(embedder.release) }) }
	defer release()
	engine := New(embedder)
	slowDone := make(chan error, 1)
	go func() {
		_, err := engine.Search(context.Background(), slow, "slowSymbol", 1)
		slowDone <- err
	}()
	select {
	case <-embedder.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow embedding did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	matches, err := engine.Search(ctx, fast, "fastSymbol", 1)
	if err != nil || len(matches) != 1 || matches[0].Path != "fast.go" {
		t.Fatalf("unrelated search waited for blocked embedding: %+v, %v", matches, err)
	}
	release()
	if err := <-slowDone; err != nil {
		t.Fatal(err)
	}
}

func TestSearchSharesInFlightSnippetEmbedding(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "shared.go", "package p\nfunc sharedSymbol() {}\n")
	embedder := &gatedEmbedder{blockedName: "sharedSymbol", started: make(chan struct{}), release: make(chan struct{})}
	engine := New(embedder)
	firstDone := make(chan error, 1)
	go func() {
		_, err := engine.Search(context.Background(), root, "first", 1)
		firstDone <- err
	}()
	select {
	case <-embedder.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first embedding did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := engine.Search(context.Background(), root, "second", 1)
		secondDone <- err
	}()
	close(embedder.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	embedder.mu.Lock()
	calls := embedder.chunkCalls
	embedder.mu.Unlock()
	if calls != 1 {
		t.Fatalf("same snippet embedded %d times, want 1", calls)
	}
}

func TestSearchOptInAndRanksCode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for name, content := range map[string]string{
		"auth.go": "package p\nfunc authentication() {}\n",
		"db.go":   "package p\nfunc connectDatabase() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	embedder := &fakeEmbedder{}
	engine := New(embedder)
	if _, err := engine.Search(context.Background(), root, "authentication", 2); err == nil {
		t.Fatal("search ran without opt-in")
	}
	if err := SetEnabled(root, true); err != nil {
		t.Fatal(err)
	}
	matches, err := engine.Search(context.Background(), root, "authentication", 2)
	if err != nil || len(matches) == 0 || matches[0].Path != "auth.go" || matches[0].StartLine != 1 {
		t.Fatalf("search: %+v, %v", matches, err)
	}
	firstCalls := embedder.calls
	if _, err := engine.Search(context.Background(), root, "authentication", 2); err != nil || embedder.calls != firstCalls+1 {
		t.Fatalf("unchanged snippets were embedded again: calls=%d, err=%v", embedder.calls, err)
	}
}

func TestSearchRequiresCommandConsentAfterConfigChanges(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	config := configPath(root)
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("enabled: true\nmax_files: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(root)
	if err != nil || loaded.Enabled {
		t.Fatalf("manual config unexpectedly enabled search: %+v, %v", loaded, err)
	}
	if err := SetEnabled(root, true); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig(root)
	if err != nil || !loaded.Enabled {
		t.Fatalf("command consent did not enable search: %+v, %v", loaded, err)
	}
	if err := os.WriteFile(config, []byte("enabled: true\nmax_files: 11\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig(root)
	if err != nil || loaded.Enabled {
		t.Fatalf("changed config retained stale consent: %+v, %v", loaded, err)
	}
	if err := SetEnabled(root, false); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadConfig(root)
	if err != nil || loaded.Enabled {
		t.Fatalf("index off did not revoke consent: %+v, %v", loaded, err)
	}
}

func TestSearchIncludesNestedCodeAndSkipsOtherExtensions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "README.md", "documentation")
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "nested", "feature.go"), []byte("package p\nfunc nestedFeature() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(&fakeEmbedder{})
	matches, err := engine.Search(context.Background(), root, "nestedFeature", 5)
	if err != nil || len(matches) != 1 || matches[0].Path != filepath.Join("src", "nested", "feature.go") {
		t.Fatalf("nested code search: %+v, %v", matches, err)
	}
}
