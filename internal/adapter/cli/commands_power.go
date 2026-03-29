package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

// ── Power commands ──────────────────────────────────────────────────────────

func handleCopy(messages []chatEntry) commandResult {
	// Find last assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role == "assistant" {
			if err := clipboard.WriteAll(messages[i].content); err != nil {
				return commandResult{
					output:  errorStyle.Render("erro ao copiar: ") + cmdLabelStyle.Render(err.Error()),
					handled: true,
				}
			}
			preview := truncate(messages[i].content, 60)
			return commandResult{
				output:  notifySuccessStyle.Render("copiado pro clipboard ") + cmdLabelStyle.Render("(" + preview + ")"),
				handled: true,
			}
		}
	}
	return commandResult{output: cmdLabelStyle.Render("nenhuma resposta pra copiar"), handled: true}
}

func handleRun(command string) commandResult {
	var b strings.Builder
	b.WriteString(cmdLabelStyle.Render("$ "))
	b.WriteString(cmdValueStyle.Render(command))
	b.WriteString("\n\n")

	output, err := runShellCmd(command)
	if err != nil {
		b.WriteString(errorStyle.Render("erro: "))
		b.WriteString(cmdLabelStyle.Render(err.Error()))
		if output != "" {
			b.WriteString("\n")
			b.WriteString(cmdPreviewStyle.Render(output))
		}
	} else if output == "" {
		b.WriteString(cmdLabelStyle.Render("(sem output)"))
	} else {
		b.WriteString(cmdPreviewStyle.Render(output))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleFile(path string) commandResult {
	// Resolve relative paths
	if !filepath.IsAbs(path) {
		wd, _ := os.Getwd()
		path = filepath.Join(wd, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	if info.IsDir() {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("caminho e um diretorio, use /project"),
			handled: true,
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro ao ler: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	lines := strings.Split(string(data), "\n")
	maxLines := 200
	truncatedFile := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncatedFile = true
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render(filepath.Base(path)))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d linhas)", len(strings.Split(string(data), "\n")))))
	b.WriteString("\n\n")

	for i, line := range lines {
		lineNum := fmt.Sprintf("%4d", i+1)
		b.WriteString(subtleStyle.Render(lineNum))
		b.WriteString(cmdLabelStyle.Render("  "))
		b.WriteString(cmdPreviewStyle.Render(line))
		b.WriteString("\n")
	}

	if truncatedFile {
		b.WriteString("\n")
		b.WriteString(cmdLabelStyle.Render(fmt.Sprintf("  ... truncado em %d linhas", maxLines)))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleSearch(pattern string) commandResult {
	output, err := runShellCmd(fmt.Sprintf("grep -rn --include='*' --max-count=30 %q .", pattern))
	if err != nil {
		if output == "" {
			return commandResult{
				output:  cmdLabelStyle.Render("nenhum resultado para: ") + cmdValueStyle.Render(pattern),
				handled: true,
			}
		}
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 30 {
		lines = lines[:30]
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Busca"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" \"%s\" (%d resultados)", pattern, len(lines))))
	b.WriteString("\n\n")

	for _, line := range lines {
		// Format: ./file:line:content
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			b.WriteString("  ")
			b.WriteString(cmdBadgeStyle.Render(parts[0]))
			b.WriteString(subtleStyle.Render(":"))
			b.WriteString(cmdValueStyle.Render(parts[1]))
			b.WriteString(subtleStyle.Render("  "))
			b.WriteString(cmdPreviewStyle.Render(strings.TrimSpace(parts[2])))
			b.WriteString("\n")
		} else {
			b.WriteString("  ")
			b.WriteString(cmdPreviewStyle.Render(line))
			b.WriteString("\n")
		}
	}

	return commandResult{output: b.String(), handled: true}
}

func handleGit(sub string) commandResult {
	var shellCmd string

	switch sub {
	case "log":
		shellCmd = "git log --oneline -20"
	case "diff":
		shellCmd = "git diff --stat"
	case "branch":
		shellCmd = "git branch -a"
	case "status", "":
		shellCmd = "git status --short"
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/git [status|log|diff|branch]"),
			handled: true,
		}
	}

	output, err := runShellCmd(shellCmd)

	var b strings.Builder
	b.WriteString(cmdLabelStyle.Render("$ "))
	b.WriteString(cmdValueStyle.Render(shellCmd))
	b.WriteString("\n\n")

	if err != nil {
		b.WriteString(errorStyle.Render("erro: "))
		b.WriteString(cmdLabelStyle.Render(err.Error()))
	} else if output == "" {
		b.WriteString(cmdLabelStyle.Render("(limpo)"))
	} else {
		b.WriteString(cmdPreviewStyle.Render(output))
	}

	return commandResult{output: b.String(), handled: true}
}

func handleProject() commandResult {
	wd, err := os.Getwd()
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Projeto"))
	b.WriteString(cmdLabelStyle.Render(" " + filepath.Base(wd)))
	b.WriteString("\n\n")
	b.WriteString(cmdValueStyle.Render("  " + filepath.Base(wd) + "/"))
	b.WriteString("\n")
	buildTree(&b, wd, "  ", 1, 3)

	return commandResult{output: b.String(), handled: true}
}

func handleExport(messages []chatEntry, path string) commandResult {
	if len(messages) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhuma mensagem pra exportar"), handled: true}
	}

	var b strings.Builder
	b.WriteString("# Chat Export\n\n")
	b.WriteString(fmt.Sprintf("*Exportado em %s*\n\n", time.Now().Format("2006-01-02 15:04:05")))
	b.WriteString("---\n\n")

	for _, entry := range messages {
		switch entry.role {
		case "user":
			b.WriteString("**> Voce:**\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("**Assistente:**\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "system":
			b.WriteString("*Sistema:*\n\n")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		case "error":
			b.WriteString("*Erro:* ")
			b.WriteString(entry.content)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return commandResult{
			output:  errorStyle.Render("erro ao salvar: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	return commandResult{
		output: notifySuccessStyle.Render("conversa exportada: ") + cmdValueStyle.Render(path) +
			cmdLabelStyle.Render(fmt.Sprintf(" (%d mensagens)", len(messages))),
		handled: true,
	}
}

// buildTree recursively builds a tree representation of a directory.
func buildTree(b *strings.Builder, dir string, prefix string, depth int, maxDepth int) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter ignored directories
	ignored := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, "dist": true, "build": true,
		".next": true, ".cache": true, ".idea": true,
		".vscode": true, "target": true, "coverage": true,
	}

	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && ignored[name] {
			continue
		}
		if ignored[name] {
			continue
		}
		filtered = append(filtered, e)
	}

	for i, entry := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		name := entry.Name()
		if entry.IsDir() {
			b.WriteString(prefix)
			b.WriteString(cmdBadgeStyle.Render(connector))
			b.WriteString(cmdValueStyle.Render(name + "/"))
			b.WriteString("\n")
			buildTree(b, filepath.Join(dir, name), childPrefix, depth+1, maxDepth)
		} else {
			b.WriteString(prefix)
			b.WriteString(subtleStyle.Render(connector))
			b.WriteString(cmdPreviewStyle.Render(name))
			b.WriteString("\n")
		}
	}
}
