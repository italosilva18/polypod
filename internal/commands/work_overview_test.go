package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tasks"
)

func TestCollectWorktreesFindsOverlappingChanges(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(wd string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git(repo, "init", "-q")
	git(repo, "config", "user.email", "test@example.com")
	git(repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "shared.txt")
	git(repo, "commit", "-qm", "base")
	branch := filepath.Join(root, "branch")
	git(repo, "worktree", "add", "-qb", "feature", branch)
	for _, dir := range []string{repo, branch} {
		if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("changed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	worktrees, err := collectWorktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("worktrees = %+v", worktrees)
	}
	for _, wt := range worktrees {
		if len(wt.Files) != 1 || wt.Files[0] != "shared.txt" {
			t.Fatalf("changed files = %+v", wt)
		}
	}
	if strings.TrimSpace(worktrees[0].Branch) == "" {
		t.Fatal("missing branch")
	}
	registry := command.NewRegistry()
	taskRegistry := tasks.NewTaskRegistry()
	cost := 0.02
	zeroCost := 0.0
	taskRegistry.Register(&tasks.TaskState{ID: "task-1", Status: tasks.StatusRunning, Description: "Corrigir código", AgentName: "coder", Workspace: branch,
		CostUSD: &cost, Progress: &tasks.AgentProgress{TokenCount: 42, ToolUseCount: 2}})
	taskRegistry.Register(&tasks.TaskState{ID: "task-local", Status: tasks.StatusCompleted, Description: "Indexar local Ollama", AgentName: "local-coder", Workspace: repo,
		CostUSD: &zeroCost, Progress: &tasks.AgentProgress{TokenCount: 120, ToolUseCount: 1}})
	taskRegistry.Register(&tasks.TaskState{ID: "task-unpriced", Status: tasks.StatusCompleted, Description: "Revisar protótipo gratuito", AgentName: "reviewer", Workspace: repo,
		Progress: &tasks.AgentProgress{TokenCount: 85, ToolUseCount: 3}})
	BindWorkOverview(registry, taskRegistry)
	cmd, ok := registry.Get("work")
	if !ok {
		t.Fatal("work command not registered")
	}
	result, err := cmd.Execute(nil, command.CommandContext{WorkingDir: repo})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"task-1", "tokens=42", "worktree=feature", "custo=US$0.0200",
		"task-local", "custo=US$0.0000",
		"task-unpriced", "custo=não medido",
		"shared.txt", "feature", "Sobreposição",
	} {
		if !strings.Contains(result.Display, expected) {
			t.Fatalf("missing %q: %s", expected, result.Display)
		}
	}
}
