package commands

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tasks"
)

type worktreeSummary struct {
	Path   string
	Branch string
	Files  []string
}

// BindWorkOverview adds a single view of local agent tasks, Git worktrees,
// changed files, and overlaps that should be reviewed before integration.
func BindWorkOverview(reg *command.Registry, taskReg *tasks.TaskRegistry) {
	if reg == nil || taskReg == nil {
		return
	}
	reg.Register(&command.Command{Name: "work", Description: "Mostrar tarefas, worktrees e arquivos em conflito", Type: command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) != 0 {
				return b13Err("uso: /work"), nil
			}
			var out strings.Builder
			active := taskReg.List()
			sort.Slice(active, func(i, j int) bool { return active[i].StartTime.After(active[j].StartTime) })
			worktrees, worktreeErr := collectWorktrees(ctx.WorkingDir)
			out.WriteString("Tarefas:\n")
			if len(active) == 0 {
				out.WriteString("  Nenhuma tarefa registrada.\n")
			}
			for _, task := range active {
				fmt.Fprintf(&out, "  %s  %s  %s", task.ID, task.Status, task.Description)
				if task.AgentName != "" {
					fmt.Fprintf(&out, "  agente=%s", task.AgentName)
				}
				if task.Progress != nil {
					fmt.Fprintf(&out, "  ferramentas=%d  tokens=%d", task.Progress.ToolUseCount, task.Progress.TokenCount)
				}
				if task.Workspace != "" {
					if wt := taskWorktree(task.Workspace, worktrees); wt != nil {
						fmt.Fprintf(&out, "  worktree=%s (%s)", wt.Branch, wt.Path)
					} else {
						fmt.Fprintf(&out, "  workspace=%s", task.Workspace)
					}
				}
				if task.CostUSD != nil {
					fmt.Fprintf(&out, "  custo=US$%.4f", *task.CostUSD)
				} else {
					out.WriteString("  custo=não medido")
				}
				out.WriteByte('\n')
			}
			if worktreeErr != nil {
				fmt.Fprintf(&out, "\nWorktrees indisponíveis: %v", worktreeErr)
				return b13OK(strings.TrimSpace(out.String())), nil
			}
			out.WriteString("\nWorktrees:\n")
			for _, wt := range worktrees {
				fmt.Fprintf(&out, "  %s  %s  %d arquivo(s) alterado(s)\n", wt.Branch, wt.Path, len(wt.Files))
			}
			owners := make(map[string][]string)
			for _, wt := range worktrees {
				for _, file := range wt.Files {
					owners[file] = append(owners[file], wt.Branch)
				}
			}
			var conflicts []string
			for file, branches := range owners {
				if len(branches) > 1 {
					conflicts = append(conflicts, file)
				}
			}
			sort.Strings(conflicts)
			out.WriteString("\nSobreposição de arquivos:\n")
			if len(conflicts) == 0 {
				out.WriteString("  Nenhuma detectada.\n")
			}
			for _, file := range conflicts {
				fmt.Fprintf(&out, "  %s (%s)\n", file, strings.Join(owners[file], ", "))
			}
			return b13OK(strings.TrimSpace(out.String())), nil
		}})
}

func taskWorktree(workspace string, worktrees []worktreeSummary) *worktreeSummary {
	var best *worktreeSummary
	for i := range worktrees {
		rel, err := filepath.Rel(worktrees[i].Path, workspace)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if best == nil || len(worktrees[i].Path) > len(best.Path) {
			best = &worktrees[i]
		}
	}
	return best
}

func collectWorktrees(wd string) ([]worktreeSummary, error) {
	raw, err := gitExecCtx(wd, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var worktrees []worktreeSummary
	for _, block := range strings.Split(raw, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var wt worktreeSummary
		for _, line := range strings.Split(block, "\n") {
			if path, ok := strings.CutPrefix(line, "worktree "); ok {
				wt.Path = path
			}
			if branch, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				wt.Branch = branch
			}
			if line == "detached" {
				wt.Branch = "(HEAD isolado)"
			}
		}
		if wt.Path == "" {
			continue
		}
		if wt.Branch == "" {
			wt.Branch = "(sem branch)"
		}
		files, err := changedWorktreeFiles(wt.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", wt.Path, err)
		}
		wt.Files = files
		worktrees = append(worktrees, wt)
		if len(worktrees) >= 32 {
			break
		}
	}
	return worktrees, nil
}

func changedWorktreeFiles(path string) ([]string, error) {
	raw, err := gitExecCtx(path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var files []string
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 4 {
			continue
		}
		files = append(files, part[3:])
		if (part[0] == 'R' || part[1] == 'R' || part[0] == 'C' || part[1] == 'C') && i+1 < len(parts) {
			i++
		}
		if len(files) >= 1000 {
			break
		}
	}
	return files, nil
}
