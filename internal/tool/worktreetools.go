package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/workdir"
)

// WorktreeInfo describes an active worktree.
type WorktreeInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Branch  string    `json:"branch"`
	BaseRef string    `json:"base_ref"`
	Created time.Time `json:"created"`
}

// NewEnterWorktreeTool creates a worktree_enter tool.
// It uses git commands directly to avoid hard dependency on internal/worktree.
func NewEnterWorktreeTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "worktree_enter" },
		DescriptionFn: func() string { return "Cria uma worktree Git isolada e move esta conversa para ela." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name for the worktree (used in branch name)",
					},
					"base_ref": map[string]any{
						"type":        "string",
						"description": "Base branch/ref (default: current HEAD)",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional custom path (default: ../<name>)",
					},
				},
				"required": []string{"name"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			name, _ := args["name"].(string)
			if name == "" {
				return types.ValidationResult{Valid: false, Message: "name is required"}
			}
			if strings.ContainsAny(name, "/.\\") {
				return types.ValidationResult{Valid: false, Message: "name must not contain / . or \\"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if !workdir.Bound(ctx.Context) {
				return types.ToolResult{IsError: true, Content: workdir.ErrScopeUnavailable.Error()}, nil
			}
			if _, active := workdir.Current(ctx.Context); active {
				return types.ToolResult{IsError: true, Content: workdir.ErrAlreadyEntered.Error()}, nil
			}
			name, _ := args["name"].(string)
			if name == "" {
				return types.ToolResult{IsError: true, Content: "name is required"}, nil
			}

			// Validate name (no slashes, dots)
			if strings.ContainsAny(name, "/.\\") {
				return types.ToolResult{IsError: true, Content: "name must not contain / . or \\"}, nil
			}

			workingDir := ctx.WorkingDir
			if workingDir == "" {
				workingDir = "."
			}

			// Check git repo
			cmd := exec.Command("git", "rev-parse", "--show-toplevel")
			cmd.Dir = workingDir
			topOut, err := cmd.Output()
			if err != nil {
				return types.ToolResult{IsError: true, Content: "not inside a git repository"}, nil
			}
			repoTop := strings.TrimSpace(string(topOut))

			baseRef, _ := args["base_ref"].(string)
			if baseRef == "" {
				baseRef = "HEAD"
			}
			baseBranch := ""
			if baseRef == "HEAD" {
				branch := exec.Command("git", "symbolic-ref", "--quiet", "--short", "HEAD")
				branch.Dir = workingDir
				if output, err := branch.Output(); err == nil {
					baseBranch = strings.TrimSpace(string(output))
				}
			} else {
				candidate := strings.TrimPrefix(baseRef, "refs/heads/")
				branch := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+candidate)
				branch.Dir = workingDir
				if err := branch.Run(); err == nil {
					baseBranch = candidate
				}
			}

			wtPath, _ := args["path"].(string)
			if wtPath == "" {
				parent := filepath.Dir(repoTop)
				wtPath = filepath.Join(parent, filepath.Base(repoTop)+"-"+name)
			} else if !filepath.IsAbs(wtPath) {
				wtPath = filepath.Join(repoTop, wtPath)
			}
			wtPath = filepath.Clean(wtPath)

			branchName := "wt/" + name

			// git worktree add -b <branch> <path> <base>
			cmd = exec.Command("git", "worktree", "add", "-b", branchName, wtPath, baseRef)
			cmd.Dir = workingDir
			output, err := cmd.CombinedOutput()
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("worktree add failed: %s", string(output))}, nil
			}
			if err := workdir.Enter(ctx.Context, wtPath, workingDir, baseBranch); err != nil {
				remove := exec.Command("git", "worktree", "remove", "--force", wtPath)
				remove.Dir = repoTop
				if cleanupErr := remove.Run(); cleanupErr != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("Worktree criada, mas não foi possível ativar nem remover %s: %v; limpeza: %v", wtPath, err, cleanupErr)}, nil
				}
				branch := exec.Command("git", "branch", "-D", branchName)
				branch.Dir = repoTop
				if cleanupErr := branch.Run(); cleanupErr != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("Worktree removida após falha de ativação, mas a branch %s permanece: %v", branchName, cleanupErr)}, nil
				}
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("Não foi possível ativar a worktree nesta conversa: %v", err)}, nil
			}

			info := WorktreeInfo{
				Name:    name,
				Path:    wtPath,
				Branch:  branchName,
				BaseRef: baseRef,
				Created: time.Now(),
			}

			msg := fmt.Sprintf("Worktree ativa nesta conversa:\n  Nome: %s\n  Caminho: %s\n  Branch: %s\n  Base: %s",
				info.Name, info.Path, info.Branch, info.BaseRef)
			return types.ToolResult{Content: msg}, nil
		},
	}
}

// NewExitWorktreeTool creates a worktree_exit tool (remove worktree).
func NewExitWorktreeTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "worktree_exit" },
		DescriptionFn: func() string { return "Sai da worktree desta conversa e a remove; pode integrar a branch antes." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Worktree name to remove",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Worktree path (alternative to name)",
					},
					"merge_back": map[string]any{
						"type":        "boolean",
						"description": "If true, merge the branch back to base before removing",
					},
					"force": map[string]any{
						"type":        "boolean",
						"description": "Force remove even with uncommitted changes",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name, _ := args["name"].(string)
			wtPath, _ := args["path"].(string)
			mergeBack, _ := args["merge_back"].(bool)
			force, _ := args["force"].(bool)
			active, scoped := workdir.Current(ctx.Context)
			if !workdir.Bound(ctx.Context) {
				return types.ToolResult{IsError: true, Content: workdir.ErrScopeUnavailable.Error()}, nil
			}
			if !scoped {
				return types.ToolResult{IsError: true, Content: workdir.ErrNotEntered.Error()}, nil
			}

			if name == "" && wtPath == "" {
				wtPath = active.Dir
			}

			workingDir := ctx.WorkingDir
			if workingDir == "" {
				workingDir = "."
			}

			// If only name, derive path
			if wtPath == "" {
				// List worktrees and find by branch name
				cmd := exec.Command("git", "worktree", "list", "--porcelain")
				cmd.Dir = workingDir
				out, err := cmd.Output()
				if err != nil {
					return types.ToolResult{IsError: true, Content: "cannot list worktrees"}, nil
				}
				lines := strings.Split(string(out), "\n")
				var currentPath, currentBranch string
				for _, line := range lines {
					if strings.HasPrefix(line, "worktree ") {
						currentPath = strings.TrimPrefix(line, "worktree ")
					} else if strings.HasPrefix(line, "branch ") {
						currentBranch = strings.TrimPrefix(line, "branch ")
						expected := "refs/heads/wt/" + name
						if currentBranch == expected {
							wtPath = currentPath
							break
						}
					}
				}
				if wtPath == "" {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("worktree '%s' not found", name)}, nil
				}
			}
			if !filepath.IsAbs(wtPath) {
				wtPath = filepath.Join(workingDir, wtPath)
			}
			wtPath = filepath.Clean(wtPath)
			if scoped && active.Dir != wtPath {
				return types.ToolResult{IsError: true, Content: "Esta conversa está em outra worktree; saia dela antes de remover outra."}, nil
			}
			if _, err := os.Stat(wtPath); os.IsNotExist(err) {
				previous, exitErr := workdir.Exit(ctx.Context, wtPath)
				if exitErr != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("A worktree foi removida fora do Polypod, mas o estado da conversa não pôde ser restaurado: %v", exitErr)}, nil
				}
				return types.ToolResult{Content: fmt.Sprintf("A worktree já não existe. Esta conversa voltou para %s.", previous)}, nil
			}
			commandDir := workingDir
			if scoped && active.Dir == wtPath {
				commandDir = active.Previous
			}

			var mergeResult string
			if mergeBack {
				if active.BaseBranch == "" {
					return types.ToolResult{IsError: true, Content: "A referência base não é uma branch local; faça o merge manualmente ou saia sem merge_back."}, nil
				}
				current := exec.Command("git", "-C", active.Previous, "symbolic-ref", "--quiet", "--short", "HEAD")
				currentOut, err := current.Output()
				if err != nil || strings.TrimSpace(string(currentOut)) != active.BaseBranch {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("Merge recusado: a branch de destino deve ser %s em %s.", active.BaseBranch, active.Previous)}, nil
				}
				if !force {
					status := exec.Command("git", "-C", wtPath, "status", "--porcelain", "--untracked-files=all")
					statusOut, err := status.Output()
					if err != nil {
						return types.ToolResult{IsError: true, Content: fmt.Sprintf("Não foi possível verificar a worktree antes do merge: %v", err)}, nil
					}
					if len(strings.TrimSpace(string(statusOut))) > 0 {
						return types.ToolResult{IsError: true, Content: "A worktree possui mudanças não commitadas. Faça commit ou saia sem merge_back."}, nil
					}
				}
				// Get branch name
				cmd := exec.Command("git", "-C", wtPath, "rev-parse", "--abbrev-ref", "HEAD")
				branchOut, err := cmd.Output()
				if err != nil {
					return types.ToolResult{IsError: true, Content: "cannot get branch name"}, nil
				}
				branch := strings.TrimSpace(string(branchOut))

				// Merge back
				cmd = exec.Command("git", "merge", "--no-ff", branch)
				cmd.Dir = commandDir
				mergeOut, err := cmd.CombinedOutput()
				if err != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("merge failed: %s", string(mergeOut))}, nil
				}
				mergeResult = fmt.Sprintf("Merged %s back.\n", branch)
			}

			// Remove worktree
			removeArgs := []string{"worktree", "remove"}
			if force {
				removeArgs = append(removeArgs, "--force")
			}
			removeArgs = append(removeArgs, wtPath)

			cmd := exec.Command("git", removeArgs...)
			cmd.Dir = commandDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("worktree remove failed: %s", string(out))}, nil
			}

			if scoped && active.Dir == wtPath {
				previous, err := workdir.Exit(ctx.Context, wtPath)
				if err != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("Worktree removida, mas o diretório da conversa não foi restaurado: %v", err)}, nil
				}
				return types.ToolResult{Content: mergeResult + fmt.Sprintf("Worktree removida: %s. Esta conversa voltou para %s.", wtPath, previous)}, nil
			}
			return types.ToolResult{Content: mergeResult + fmt.Sprintf("Worktree removida: %s", wtPath)}, nil
		},
	}
}

// RegisterWorktreeTools registers both worktree tools.
func RegisterWorktreeTools(reg *Registry) {
	reg.RegisterDef(NewEnterWorktreeTool())
	reg.RegisterDef(NewExitWorktreeTool())
}
