package worktree

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Worktree represents an active git worktree for isolated sessions.
type Worktree struct {
	Name    string
	Path    string
	Branch  string
	Created time.Time
}

// Manager handles git worktree lifecycle.
type Manager struct {
	baseDir string
	logger  *slog.Logger
}

// NewManager creates a worktree manager.
func NewManager(baseDir string, logger *slog.Logger) *Manager {
	if baseDir == "" {
		baseDir = filepath.Join(os.TempDir(), "polypod-worktrees")
	}
	os.MkdirAll(baseDir, 0755)
	return &Manager{baseDir: baseDir, logger: logger}
}

// Create creates a new worktree with an isolated branch.
func (m *Manager) Create(ctx context.Context, name string) (*Worktree, error) {
	if !isGitRepo() {
		return nil, fmt.Errorf("nao e um repositorio git")
	}

	branch := "polypod/" + name
	worktreePath := filepath.Join(m.baseDir, name)

	// Create new branch and worktree
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "git", "worktree", "add", "-b", branch, worktreePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("criando worktree: %s\n%w", string(out), err)
	}

	wt := &Worktree{
		Name:    name,
		Path:    worktreePath,
		Branch:  branch,
		Created: time.Now(),
	}

	m.logger.Info("worktree criado", "name", name, "path", worktreePath, "branch", branch)
	return wt, nil
}

// Remove cleans up a worktree.
func (m *Manager) Remove(ctx context.Context, name string) error {
	worktreePath := filepath.Join(m.baseDir, name)
	branch := "polypod/" + name

	execCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Remove worktree
	cmd := exec.CommandContext(execCtx, "git", "worktree", "remove", worktreePath, "--force")
	cmd.CombinedOutput()

	// Delete branch
	cmd = exec.CommandContext(execCtx, "git", "branch", "-D", branch)
	cmd.CombinedOutput()

	m.logger.Info("worktree removido", "name", name)
	return nil
}

// List returns all active polypod worktrees.
func (m *Manager) List(ctx context.Context) ([]Worktree, error) {
	execCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "git", "worktree", "list", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	var current Worktree

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" && strings.Contains(current.Path, "polypod-worktrees") {
				worktrees = append(worktrees, current)
			}
			current = Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
			current.Name = strings.TrimPrefix(current.Branch, "polypod/")
		}
	}
	if current.Path != "" && strings.Contains(current.Path, "polypod-worktrees") {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// MergeBack merges worktree changes back to the original branch.
func (m *Manager) MergeBack(ctx context.Context, name, targetBranch string) error {
	branch := "polypod/" + name

	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "git", "merge", branch, "--no-ff",
		"-m", fmt.Sprintf("Merge polypod worktree '%s'", name))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge: %s\n%w", string(out), err)
	}

	return nil
}

func isGitRepo() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}
