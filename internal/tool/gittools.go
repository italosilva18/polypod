package tool

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/costa/polypod/internal/codemap"
	"github.com/costa/polypod/internal/git"
	"github.com/costa/polypod/internal/types"
)

// RegisterGitTools registers all git and project information tools with the registry.
func RegisterGitTools(reg *Registry, workingDir string) {
	reg.RegisterDef(newGitStatusTool(workingDir))
	reg.RegisterDef(newGitDiffTool(workingDir))
	reg.RegisterDef(newGitLogTool(workingDir))
	reg.RegisterDef(newGitCommitTool(workingDir))
	reg.RegisterDef(newProjectInfoTool(workingDir))
}

// --- git_status ---
func newGitStatusTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "git_status" },
		DescriptionFn: func() string { return "Show parsed git status with staged/unstaged/untracked files" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if !git.IsInsideRepo() {
				return types.ToolResult{
					Content: "Not inside a git repository.",
					IsError: true,
				}, nil
			}

			statuses, err := git.GetStatus()
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error getting git status: %v", err),
					IsError: true,
				}, nil
			}

			var sb strings.Builder

			// Header with branch info
			branch, err := git.GetCurrentBranch()
			if err == nil {
				sb.WriteString(fmt.Sprintf("Branch: %s\n\n", branch))
			}

			if len(statuses) == 0 {
				sb.WriteString("Working tree clean. Nothing to commit.")
				return types.ToolResult{Content: sb.String()}, nil
			}

			sb.WriteString(git.FormatStatus(statuses))

			// Add summary counts
			var staged, unstaged, untracked int
			for _, s := range statuses {
				switch {
				case s.Status == "??":
					untracked++
				case s.Staged:
					staged++
				default:
					unstaged++
				}
			}
			sb.WriteString(fmt.Sprintf("\nSummary: %d staged, %d unstaged, %d untracked\n",
				staged, unstaged, untracked))

			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// --- git_diff ---
func newGitDiffTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "git_diff" },
		DescriptionFn: func() string { return "Show git diff with optional staging filter" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"staged": map[string]any{
						"type":        "boolean",
						"description": "If true, show only staged changes (--cached). Default: false (unstaged).",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if !git.IsInsideRepo() {
				return types.ToolResult{
					Content: "Not inside a git repository.",
					IsError: true,
				}, nil
			}

			staged := false
			if v, ok := args["staged"].(bool); ok {
				staged = v
			} else if v, ok := args["staged"].(string); ok && v == "true" {
				staged = true
			}

			diff, err := git.GetDiff(staged)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error getting diff: %v", err),
					IsError: true,
				}, nil
			}

			if strings.TrimSpace(diff) == "" {
				label := "unstaged"
				if staged {
					label = "staged"
				}
				return types.ToolResult{
					Content: fmt.Sprintf("No %s changes.", label),
				}, nil
			}

			// Add stat header
			stat, _ := git.GetDiffStat(staged)
			var sb strings.Builder
			if stat != "" {
				sb.WriteString(stat)
				sb.WriteString("\n---\n\n")
			}

			// Truncate very large diffs
			if len(diff) > 50000 {
				diff = diff[:50000] + fmt.Sprintf("\n\n... [diff truncated, %d bytes total]", len(diff))
			}
			sb.WriteString(diff)

			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// --- git_log ---
func newGitLogTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "git_log" },
		DescriptionFn: func() string { return "Show recent git commits with hash, date, author, and subject" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{
						"type":        "integer",
						"description": "Number of commits to show (default: 10, max: 50)",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if !git.IsInsideRepo() {
				return types.ToolResult{
					Content: "Not inside a git repository.",
					IsError: true,
				}, nil
			}

			n := 10
			if v, ok := args["count"].(float64); ok && v > 0 {
				n = int(v)
			} else if v, ok := args["count"].(string); ok {
				if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
					n = parsed
				}
			}
			if n > 50 {
				n = 50
			}

			commits, err := git.GetRecentCommits(n)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error getting commits: %v", err),
					IsError: true,
				}, nil
			}

			if len(commits) == 0 {
				return types.ToolResult{Content: "No commits found."}, nil
			}

			return types.ToolResult{Content: git.FormatCommits(commits)}, nil
		},
	}
}

// --- git_commit ---
func newGitCommitTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "git_commit" },
		DescriptionFn: func() string {
			return "Stage files and create a git commit. Requires approval for destructive operation."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "Commit message",
					},
					"files": map[string]any{
						"type":        "string",
						"description": "Comma-separated list of files to stage. If empty, stages all changes.",
					},
				},
				"required": []string{"message"},
			}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			msg, _ := args["message"].(string)
			if strings.TrimSpace(msg) == "" {
				return types.ValidationResult{Valid: false, Message: "commit message is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		CheckPermFn: func(args map[string]any, ctx types.ToolContext) types.PermissionResult {
			msg, _ := args["message"].(string)
			return types.PermissionResult{
				Behavior: types.PermissionAsk,
				Message:  fmt.Sprintf("Create git commit: %q", msg),
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if !git.IsInsideRepo() {
				return types.ToolResult{
					Content: "Not inside a git repository.",
					IsError: true,
				}, nil
			}

			message, _ := args["message"].(string)
			files, _ := args["files"].(string)

			// Stage files
			if files != "" {
				paths := strings.Split(files, ",")
				for i := range paths {
					paths[i] = strings.TrimSpace(paths[i])
				}
				if err := git.StageFiles(paths); err != nil {
					return types.ToolResult{
						Content: fmt.Sprintf("Error staging files: %v", err),
						IsError: true,
					}, nil
				}
			} else {
				if err := git.StageAll(); err != nil {
					return types.ToolResult{
						Content: fmt.Sprintf("Error staging all files: %v", err),
						IsError: true,
					}, nil
				}
			}

			// Verify there are staged changes
			if !git.HasStagedChanges() {
				return types.ToolResult{
					Content: "Nothing to commit. No staged changes after staging.",
					IsError: true,
				}, nil
			}

			// Create commit
			if err := git.CreateCommit(message); err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error creating commit: %v", err),
					IsError: true,
				}, nil
			}

			// Get summary of what was committed
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Committed: %s\n", message))

			// Show the new commit hash
			commits, err := git.GetRecentCommits(1)
			if err == nil && len(commits) > 0 {
				sb.WriteString(fmt.Sprintf("Hash: %s\n", commits[0].Hash[:7]))
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	}
}

// --- project_info ---
func newProjectInfoTool(wd string) types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "project_info" },
		DescriptionFn: func() string {
			return "Show comprehensive project information: languages, frameworks, build systems, structure"
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Project root directory (default: working directory)",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			path := wd
			if p, ok := args["path"].(string); ok && p != "" {
				path = resolvePath(wd, p)
			}

			idx, err := codemap.IndexProject(path)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error indexing project: %v", err),
					IsError: true,
				}, nil
			}

			summary := codemap.FormatProjectSummary(idx)
			return types.ToolResult{Content: summary}, nil
		},
	}
}
