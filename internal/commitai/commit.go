package commitai

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// StagedChange represents a file change in the staging area.
type StagedChange struct {
	Status string // "A" (added), "M" (modified), "D" (deleted), "R" (renamed)
	Path   string
}

// GetStagedDiff returns the staged diff and list of changed files.
func GetStagedDiff() (diff string, changes []StagedChange, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Get file list
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-status")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("git diff --name-status: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			changes = append(changes, StagedChange{Status: parts[0], Path: parts[1]})
		}
	}

	// Get actual diff
	cmd = exec.CommandContext(ctx, "git", "diff", "--cached", "--stat")
	statOut, _ := cmd.CombinedOutput()

	cmd = exec.CommandContext(ctx, "git", "diff", "--cached")
	diffOut, _ := cmd.CombinedOutput()
	diff = string(diffOut)

	// Truncate very large diffs
	if len(diff) > 8000 {
		diff = diff[:8000] + "\n... [diff truncado, " + fmt.Sprintf("%d", len(diffOut)) + " bytes total]"
	}

	return string(statOut) + "\n" + diff, changes, nil
}

// ClassifyChanges categorizes changes by type for commit message generation.
func ClassifyChanges(changes []StagedChange) string {
	var added, modified, deleted []string
	for _, c := range changes {
		switch c.Status {
		case "A":
			added = append(added, c.Path)
		case "M":
			modified = append(modified, c.Path)
		case "D":
			deleted = append(deleted, c.Path)
		}
	}

	var sb strings.Builder
	if len(added) > 0 {
		fmt.Fprintf(&sb, "Arquivos adicionados (%d): %s\n", len(added), strings.Join(added, ", "))
	}
	if len(modified) > 0 {
		fmt.Fprintf(&sb, "Arquivos modificados (%d): %s\n", len(modified), strings.Join(modified, ", "))
	}
	if len(deleted) > 0 {
		fmt.Fprintf(&sb, "Arquivos removidos (%d): %s\n", len(deleted), strings.Join(deleted, ", "))
	}
	return sb.String()
}

// DetectScope auto-detects the commit scope from file paths.
func DetectScope(changes []StagedChange) string {
	if len(changes) == 0 {
		return ""
	}

	// Find common directory prefix
	dirs := make(map[string]int)
	for _, c := range changes {
		parts := strings.Split(c.Path, "/")
		if len(parts) > 1 {
			dirs[parts[0]]++
			if len(parts) > 2 {
				dirs[parts[0]+"/"+parts[1]]++
			}
		}
	}

	// Most common directory is the scope
	bestDir := ""
	bestCount := 0
	for dir, count := range dirs {
		if count > bestCount {
			bestDir = dir
			bestCount = count
		}
	}

	// Clean up common prefixes
	bestDir = strings.TrimPrefix(bestDir, "internal/")
	bestDir = strings.TrimPrefix(bestDir, "src/")
	bestDir = strings.TrimPrefix(bestDir, "lib/")

	return bestDir
}

// GenerateCommitPrompt creates an AI prompt for generating a commit message.
func GenerateCommitPrompt(diff string, changes []StagedChange, includeWhy bool) string {
	classification := ClassifyChanges(changes)
	scope := DetectScope(changes)

	var sb strings.Builder
	sb.WriteString("Gere uma mensagem de commit seguindo Conventional Commits.\n\n")
	sb.WriteString("Formato: <tipo>(<escopo>): <descricao>\n\n")
	sb.WriteString("Tipos: feat, fix, docs, refactor, test, chore, perf, style, ci, build\n\n")

	if scope != "" {
		fmt.Fprintf(&sb, "Escopo sugerido: %s\n\n", scope)
	}

	sb.WriteString("## Resumo das mudancas\n")
	sb.WriteString(classification)
	sb.WriteString("\n## Diff\n```\n")
	sb.WriteString(diff)
	sb.WriteString("\n```\n\n")

	sb.WriteString("Regras:\n")
	sb.WriteString("1. Primeira linha: maximo 72 caracteres\n")
	sb.WriteString("2. Tipo + escopo + descricao concisa\n")
	sb.WriteString("3. Use imperativo: 'add', 'fix', 'update', nao 'added', 'fixed'\n")

	if includeWhy {
		sb.WriteString("4. Apos uma linha em branco, explique POR QUE a mudanca foi feita\n")
	}

	sb.WriteString("\nRetorne APENAS a mensagem de commit, sem explicacao adicional.")
	return sb.String()
}

// ExecuteCommit runs git commit with the given message.
func ExecuteCommit(message string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
