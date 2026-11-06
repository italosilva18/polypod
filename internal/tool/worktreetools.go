package tool

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/types"
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
		DescriptionFn: func() string { return "Create a new git worktree for isolated changes. Requires git repo." },
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

			wtPath, _ := args["path"].(string)
			if wtPath == "" {
				parent := filepath.Dir(repoTop)
				wtPath = filepath.Join(parent, filepath.Base(repoTop)+"-"+name)
			}

			branchName := "wt/" + name

			// git worktree add -b <branch> <path> <base>
			cmd = exec.Command("git", "worktree", "add", "-b", branchName, wtPath, baseRef)
			cmd.Dir = workingDir
			output, err := cmd.CombinedOutput()
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("worktree add failed: %s", string(output))}, nil
			}

			info := WorktreeInfo{
				Name:    name,
				Path:    wtPath,
				Branch:  branchName,
				BaseRef: baseRef,
				Created: time.Now(),
			}

			msg := fmt.Sprintf("Worktree created:\n  Name:   %s\n  Path:   %s\n  Branch: %s\n  Base:   %s\n\nTo enter: cd %s",
				info.Name, info.Path, info.Branch, info.BaseRef, info.Path)
			return types.ToolResult{Content: msg}, nil
		},
	}
}

// NewExitWorktreeTool creates a worktree_exit tool (remove worktree).
func NewExitWorktreeTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "worktree_exit" },
		DescriptionFn: func() string { return "Remove a git worktree. Optionally merge its branch back first." },
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
			name, _ := args["name"].(string)
			wtPath, _ := args["path"].(string)
			if name == "" && wtPath == "" {
				return types.ValidationResult{Valid: false, Message: "must provide name or path"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name, _ := args["name"].(string)
			wtPath, _ := args["path"].(string)
			mergeBack, _ := args["merge_back"].(bool)
			force, _ := args["force"].(bool)

			if name == "" && wtPath == "" {
				return types.ToolResult{IsError: true, Content: "must provide name or path"}, nil
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

			var mergeResult string
			if mergeBack {
				// Get branch name
				cmd := exec.Command("git", "-C", wtPath, "rev-parse", "--abbrev-ref", "HEAD")
				branchOut, err := cmd.Output()
				if err != nil {
					return types.ToolResult{IsError: true, Content: "cannot get branch name"}, nil
				}
				branch := strings.TrimSpace(string(branchOut))

				// Merge back
				cmd = exec.Command("git", "merge", "--no-ff", branch)
				cmd.Dir = workingDir
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
			cmd.Dir = workingDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("worktree remove failed: %s", string(out))}, nil
			}

			return types.ToolResult{Content: mergeResult + fmt.Sprintf("Worktree removed: %s", wtPath)}, nil
		},
	}
}

// RegisterWorktreeTools registers both worktree tools.
func RegisterWorktreeTools(reg *Registry) {
	reg.RegisterDef(NewEnterWorktreeTool())
	reg.RegisterDef(NewExitWorktreeTool())
}
