package churn

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func setupGitRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}

	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")

	return dir
}

func commitFile(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runInDir(t, dir, "git", "add", path)
	runInDir(t, dir, "git", "commit", "-m", msg)
}

func runInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, string(out))
	}
}

func TestCompute_SingleFile(t *testing.T) {
	dir := setupGitRepo(t)
	commitFile(t, dir, "main.go", "package main\n\nfunc main() {}\n", "initial")
	commitFile(t, dir, "main.go", "package main\n\nfunc main() { println(\"hi\") }\n", "add println")

	cfg := DefaultConfig(dir)
	hotspots, err := Compute(cfg)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	found := false
	for _, h := range hotspots {
		if h.Path == "main.go" {
			found = true
			if h.Commits < 2 {
				t.Errorf("commits = %d; want >= 2", h.Commits)
			}
		}
	}
	if !found {
		t.Errorf("main.go not in hotspots: %v", hotspots)
	}
}

func TestCompute_MultipleFiles(t *testing.T) {
	dir := setupGitRepo(t)
	commitFile(t, dir, "a.go", "package a\n", "add a")
	commitFile(t, dir, "b.go", "package b\n", "add b")
	commitFile(t, dir, "a.go", "package a\n\nvar X = 1\n", "modify a")

	cfg := DefaultConfig(dir)
	hotspots, err := Compute(cfg)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(hotspots) < 2 {
		t.Errorf("got %d hotspots; want >= 2", len(hotspots))
	}

	var aCommits, bCommits int
	for _, h := range hotspots {
		switch h.Path {
		case "a.go":
			aCommits = h.Commits
		case "b.go":
			bCommits = h.Commits
		}
	}
	if aCommits < 2 {
		t.Errorf("a.go commits = %d; want >= 2", aCommits)
	}
	if bCommits < 1 {
		t.Errorf("b.go commits = %d; want >= 1", bCommits)
	}
}

func TestCompute_EmptyRepo(t *testing.T) {
	dir := setupGitRepo(t)

	cfg := DefaultConfig(dir)
	// An empty repo has no HEAD, so git log may return an error
	// or Compute may return an empty slice. Both are acceptable.
	hotspots, err := Compute(cfg)
	if err == nil && len(hotspots) != 0 {
		t.Errorf("empty repo hotspots = %d; want 0", len(hotspots))
	}
}

func TestCompute_ExcludeGlob(t *testing.T) {
	dir := setupGitRepo(t)
	commitFile(t, dir, "src/main.go", "package main\n", "add src")
	commitFile(t, dir, "vendor/lib.go", "package lib\n", "add vendor")

	cfg := DefaultConfig(dir)
	hotspots, err := Compute(cfg)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	for _, h := range hotspots {
		if h.Path == "vendor/lib.go" {
			t.Errorf("vendor/lib.go should be excluded, got: %+v", h)
		}
	}
}

func TestCompute_MinCommits(t *testing.T) {
	dir := setupGitRepo(t)
	commitFile(t, dir, "once.go", "package once\n", "add once")
	commitFile(t, dir, "twice.go", "package twice\n", "add twice")
	commitFile(t, dir, "twice.go", "package twice\n\nvar X = 1\n", "modify twice")

	cfg := DefaultConfig(dir)
	cfg.MinCommits = 2
	hotspots, err := Compute(cfg)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	for _, h := range hotspots {
		if h.Path == "once.go" {
			t.Errorf("once.go should be filtered out with MinCommits=2")
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("/some/repo")
	if cfg.RepoDir != "/some/repo" {
		t.Errorf("RepoDir = %q", cfg.RepoDir)
	}
	if len(cfg.ExcludeGlob) == 0 {
		t.Error("ExcludeGlob should have defaults")
	}
	if cfg.MinCommits < 1 {
		t.Errorf("MinCommits = %d; want >= 1", cfg.MinCommits)
	}
}

func TestTopN(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "a", Score: 10},
		{Path: "b", Score: 20},
		{Path: "c", Score: 5},
	}

	top := TopN(hotspots, 2)
	if len(top) != 2 {
		t.Errorf("TopN len = %d; want 2", len(top))
	}
}

func TestTopN_LargerThanList(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "a", Score: 10},
		{Path: "b", Score: 20},
	}

	top := TopN(hotspots, 10)
	if len(top) != 2 {
		t.Errorf("TopN len = %d; want 2", len(top))
	}
}

func TestByPath(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "internal/auth.go"},
		{Path: "internal/db.go"},
		{Path: "cmd/main.go"},
	}

	filtered := ByPath(hotspots, "internal")
	if len(filtered) != 2 {
		t.Errorf("ByPath len = %d; want 2", len(filtered))
	}
}

func TestByPath_NoMatch(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "internal/auth.go"},
		{Path: "cmd/main.go"},
	}

	filtered := ByPath(hotspots, "nonexistent")
	if len(filtered) != 0 {
		t.Errorf("ByPath len = %d; want 0", len(filtered))
	}
}

func TestFormatReport(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "file.go", Commits: 10, Authors: 3, Adds: 100, Dels: 50, Score: 5.0},
	}

	out := FormatReport(hotspots, 0)
	if out == "" {
		t.Error("FormatReport empty")
	}
}

func TestFormatReport_Empty(t *testing.T) {
	out := FormatReport(nil, 0)
	if out == "" {
		t.Error("FormatReport should return something for empty input")
	}
}

func TestFormatReport_TopLimit(t *testing.T) {
	hotspots := []Hotspot{
		{Path: "a.go", Commits: 10, Score: 10.0},
		{Path: "b.go", Commits: 5, Score: 5.0},
		{Path: "c.go", Commits: 2, Score: 2.0},
	}

	out := FormatReport(hotspots, 1)
	if out == "" {
		t.Error("FormatReport empty")
	}
}

func TestComputeStats(t *testing.T) {
	hotspots := []Hotspot{
		{Commits: 10, Adds: 50, Dels: 20},
		{Commits: 5, Adds: 30, Dels: 10},
	}

	stats := ComputeStats(hotspots)
	if stats.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d; want 2", stats.TotalFiles)
	}
	if stats.TotalCommits != 15 {
		t.Errorf("TotalCommits = %d; want 15", stats.TotalCommits)
	}
	if stats.TotalAdds != 80 {
		t.Errorf("TotalAdds = %d; want 80", stats.TotalAdds)
	}
	if stats.TotalDels != 30 {
		t.Errorf("TotalDels = %d; want 30", stats.TotalDels)
	}
	if stats.MaxCommits != 10 {
		t.Errorf("MaxCommits = %d; want 10", stats.MaxCommits)
	}
	if stats.AvgCommits != 7.5 {
		t.Errorf("AvgCommits = %f; want 7.5", stats.AvgCommits)
	}
}

func TestComputeStats_Empty(t *testing.T) {
	stats := ComputeStats(nil)
	if stats.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d; want 0", stats.TotalFiles)
	}
	if stats.TotalCommits != 0 {
		t.Errorf("TotalCommits = %d; want 0", stats.TotalCommits)
	}
}
