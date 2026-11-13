package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pgit "github.com/costa/polypod/internal/git"
)

// ── /review ─────────────────────────────────────────────────────────────────

// handleReview runs an AI-driven code review against pending changes,
// staged changes, a git revision range, a single file, or the current
// PR (diff vs the default branch).
//
// Supported targets:
//
//	/review                  → working tree diff (uncommitted)
//	/review --staged         → staged diff
//	/review --pr             → diff between current branch and origin's default branch
//	/review HEAD~3..HEAD     → arbitrary git revision range
//	/review main..feature    → branch-to-branch diff
//	/review path/to/file.go  → diff for a single file (working tree)
func handleReview(parts []string, deps commandDeps) commandResult {
	dir := commandWorkingDir(deps)
	if deps.generateText == nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("servico AI nao disponivel"),
			handled: true,
		}
	}
	if !pgit.IsInsideRepoAt(dir) {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("nao esta em um repositorio git"),
			handled: true,
		}
	}

	target := ""
	if len(parts) >= 2 {
		target = strings.Join(parts[1:], " ")
	}

	diff, label, files, err := resolveReviewTarget(target, dir)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	if strings.TrimSpace(diff) == "" {
		return commandResult{
			output: cmdLabelStyle.Render("nenhuma alteracao para revisar em ") +
				cmdValueStyle.Render(label),
			handled: true,
		}
	}

	// Truncate very large diffs to keep within model context budgets.
	const maxDiffChars = 60000
	truncated := false
	if len(diff) > maxDiffChars {
		diff = diff[:maxDiffChars]
		truncated = true
	}

	added, removed := countDiffLines(diff)

	prompt := buildReviewPrompt(diff, label, files, added, removed)
	result, err := deps.generateText(prompt)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("falha na revisão: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	header := formatReviewHeader("REVISÃO DE CÓDIGO", label, files, added, removed, truncated)
	body := formatAIBlock(result)
	return commandResult{output: header + "\n\n" + body, handled: true}
}

// resolveReviewTarget interprets the user-provided target argument and
// returns (diff, human-readable label, file count, error).
func resolveReviewTarget(target, dir string) (string, string, int, error) {
	target = strings.TrimSpace(target)

	switch {
	case target == "":
		diff, err := pgit.GetDiff(false, dir)
		if err != nil {
			return "", "", 0, err
		}
		files, _ := pgit.GetFilesDiff(false, dir)
		return diff, "working tree (unstaged)", len(files), nil

	case target == "--staged" || target == "--cached":
		diff, err := pgit.GetDiff(true, dir)
		if err != nil {
			return "", "", 0, err
		}
		files, _ := pgit.GetFilesDiff(true, dir)
		return diff, "staged", len(files), nil

	case target == "--pr":
		base, err := detectDefaultBranch(dir)
		if err != nil {
			return "", "", 0, err
		}
		head, err := pgit.GetCurrentBranch(dir)
		if err != nil {
			return "", "", 0, err
		}
		rng := base + "..." + head
		diff, err := runGitCapture(dir, "diff", rng)
		if err != nil {
			return "", "", 0, err
		}
		files := countGitFiles(dir, "diff", "--name-only", rng)
		return diff, fmt.Sprintf("PR: %s", rng), files, nil
	}

	// Range syntax (a..b or a...b)
	if strings.Contains(target, "..") {
		diff, err := runGitCapture(dir, "diff", target)
		if err != nil {
			return "", "", 0, err
		}
		files := countGitFiles(dir, "diff", "--name-only", target)
		return diff, target, files, nil
	}

	// File path
	filePath := target
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(dir, filePath)
	}
	if _, err := os.Stat(filePath); err == nil {
		diff, derr := runGitCapture(dir, "diff", "--", target)
		if derr != nil {
			return "", "", 0, derr
		}
		return diff, target, 1, nil
	}

	// Treat as an arbitrary git ref/file the user knows about.
	diff, derr := runGitCapture(dir, "diff", "--", target)
	if derr != nil {
		return "", "", 0, fmt.Errorf("alvo nao reconhecido: %s", target)
	}
	return diff, target, 1, nil
}

// buildReviewPrompt assembles the prompt instructing the AI to perform a
// code review of the supplied diff. The output format is opinionated so
// the result remains readable in a terminal.
func buildReviewPrompt(diff, label string, files, added, removed int) string {
	var b strings.Builder
	b.WriteString("Voce e um revisor de codigo senior fazendo code review.\n\n")
	b.WriteString(fmt.Sprintf("Alvo: %s (%d arquivos, +%d/-%d linhas)\n\n", label, files, added, removed))
	b.WriteString("Analise o diff abaixo e produza uma revisao em PORTUGUES, formatada EXATAMENTE assim (sem alterar os titulos):\n\n")
	b.WriteString("📋 RESUMO\n")
	b.WriteString("Resumo de 1-2 frases sobre o que mudou.\n\n")
	b.WriteString("✅ PONTOS POSITIVOS\n")
	b.WriteString("• ponto 1\n• ponto 2\n\n")
	b.WriteString("⚠️ PROBLEMAS ENCONTRADOS\n\n")
	b.WriteString("[ALTA] caminho/do/arquivo.go:LINHA\n")
	b.WriteString("  Descricao do problema.\n")
	b.WriteString("  Sugestao: como corrigir.\n\n")
	b.WriteString("[MEDIA] outro/arquivo.go:LINHA\n")
	b.WriteString("  ...\n\n")
	b.WriteString("[BAIXA] outro/arquivo.go:LINHA\n")
	b.WriteString("  ...\n\n")
	b.WriteString("🔧 RECOMENDACOES\n")
	b.WriteString("1. Recomendacao acionavel 1\n")
	b.WriteString("2. Recomendacao acionavel 2\n\n")
	b.WriteString("Foque em: qualidade de codigo, bugs potenciais, performance, boas praticas, cobertura de testes.\n")
	b.WriteString("Use severidades [ALTA] [MEDIA] [BAIXA]. Se nao houver problemas em uma severidade, omita o item.\n")
	b.WriteString("Cite SEMPRE o arquivo:linha. NAO inclua blocos de codigo markdown na resposta.\n\n")
	b.WriteString("DIFF:\n")
	b.WriteString("```diff\n")
	b.WriteString(diff)
	b.WriteString("\n```\n")
	return b.String()
}

// ── /security-review ────────────────────────────────────────────────────────

// handleSecurityReview runs a security-focused AI review of code changes
// or a target file. With --full it scans the entire repository (slow and
// token-heavy).
func handleSecurityReview(parts []string, deps commandDeps) commandResult {
	if deps.generateText == nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render("servico AI nao disponivel"),
			handled: true,
		}
	}

	target := ""
	if len(parts) >= 2 {
		target = strings.Join(parts[1:], " ")
	}

	source, label, fileCount, err := resolveSecurityTarget(target, commandWorkingDir(deps))
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("erro: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}
	if strings.TrimSpace(source) == "" {
		return commandResult{
			output: cmdLabelStyle.Render("nada a analisar em ") +
				cmdValueStyle.Render(label),
			handled: true,
		}
	}

	const maxChars = 80000
	truncated := false
	if len(source) > maxChars {
		source = source[:maxChars]
		truncated = true
	}

	prompt := buildSecurityPrompt(source, label, fileCount)
	result, err := deps.generateText(prompt)
	if err != nil {
		return commandResult{
			output:  errorStyle.Render("falha na revisão de segurança: ") + cmdLabelStyle.Render(err.Error()),
			handled: true,
		}
	}

	header := formatSecurityHeader(label, fileCount, truncated)
	body := formatAIBlock(result)
	return commandResult{output: header + "\n\n" + body, handled: true}
}

// resolveSecurityTarget returns (content-or-diff, label, file-count, error)
// based on the target argument. The content can be either a unified diff
// (for change-based scopes) or concatenated file bodies (for --full and
// single-file targets).
func resolveSecurityTarget(target, dir string) (string, string, int, error) {
	target = strings.TrimSpace(target)

	switch {
	case target == "":
		if !pgit.IsInsideRepoAt(dir) {
			return "", "", 0, fmt.Errorf("nao esta em um repositorio git (use /security-review <file> ou --full)")
		}
		diff, err := pgit.GetDiff(false, dir)
		if err != nil {
			return "", "", 0, err
		}
		files, _ := pgit.GetFilesDiff(false, dir)
		return diff, "working tree (unstaged)", len(files), nil

	case target == "--staged" || target == "--cached":
		if !pgit.IsInsideRepoAt(dir) {
			return "", "", 0, fmt.Errorf("nao esta em um repositorio git")
		}
		diff, err := pgit.GetDiff(true, dir)
		if err != nil {
			return "", "", 0, err
		}
		files, _ := pgit.GetFilesDiff(true, dir)
		return diff, "staged", len(files), nil

	case target == "--full":
		body, count, err := collectRepoSources(dir)
		if err != nil {
			return "", "", 0, err
		}
		return body, "codebase completo", count, nil
	}

	// Single file target.
	filePath := target
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(dir, filePath)
	}
	if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
		data, rerr := os.ReadFile(filePath)
		if rerr != nil {
			return "", "", 0, rerr
		}
		return fmt.Sprintf("// FILE: %s\n%s", target, string(data)), target, 1, nil
	}

	return "", "", 0, fmt.Errorf("alvo nao reconhecido: %s", target)
}

// buildSecurityPrompt creates the security-focused prompt sent to the AI.
func buildSecurityPrompt(source, label string, fileCount int) string {
	var b strings.Builder
	b.WriteString("Voce e um auditor de seguranca de aplicacoes (AppSec) experiente.\n\n")
	b.WriteString(fmt.Sprintf("Alvo: %s (%d arquivos)\n\n", label, fileCount))
	b.WriteString("Analise o codigo abaixo e identifique vulnerabilidades de seguranca. Procure especificamente por:\n")
	b.WriteString("- SQL injection\n")
	b.WriteString("- Command injection (shell exec, eval)\n")
	b.WriteString("- Path traversal\n")
	b.WriteString("- XSS em qualquer output HTML\n")
	b.WriteString("- Segredos/chaves hardcoded\n")
	b.WriteString("- Crypto inseguro (MD5, SHA1, DES, RC4, randoms previsiveis)\n")
	b.WriteString("- Falta de validacao de input\n")
	b.WriteString("- Privilege escalation\n")
	b.WriteString("- Operacoes de arquivo perigosas\n")
	b.WriteString("- Desserializacao insegura\n")
	b.WriteString("- CORS mal configurado\n\n")
	b.WriteString("Responda em PORTUGUES no formato EXATO abaixo (sem alterar os titulos):\n\n")
	b.WriteString("🚨 CRITICOS (N)\n")
	b.WriteString("1. caminho/do/arquivo.go:LINHA - Tipo da Vulnerabilidade\n")
	b.WriteString("   `trecho de codigo vulneravel`\n")
	b.WriteString("   FIX: como corrigir.\n\n")
	b.WriteString("⚠️ ALTOS (N)\n")
	b.WriteString("1. caminho/do/arquivo.go:LINHA - Tipo\n")
	b.WriteString("   FIX: ...\n\n")
	b.WriteString("ℹ️ MEDIOS (N)\n")
	b.WriteString("1. ...\n\n")
	b.WriteString("📝 BAIXOS (N)\n")
	b.WriteString("1. ...\n\n")
	b.WriteString("Se uma severidade nao tiver achados, escreva apenas: '(nenhum)'.\n")
	b.WriteString("Se NENHUMA vulnerabilidade for encontrada, responda apenas:\n")
	b.WriteString("✓ NENHUM PROBLEMA: <N> arquivos analisados\n\n")
	b.WriteString("Cite SEMPRE arquivo:linha. Seja conciso e factual. NAO use blocos markdown alem do trecho de codigo.\n\n")
	b.WriteString("CODIGO:\n")
	b.WriteString(source)
	b.WriteString("\n")
	return b.String()
}

// ── helpers ─────────────────────────────────────────────────────────────────

// formatReviewHeader builds the styled top banner shared by /review.
func formatReviewHeader(kind, label string, files, added, removed int, truncated bool) string {
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("═══ " + kind + " ═══"))
	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("Target: "))
	b.WriteString(cmdValueStyle.Render(label))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d arquivos, ", files)))
	b.WriteString(diffAddStyle.Render(fmt.Sprintf("+%d", added)))
	b.WriteString(cmdLabelStyle.Render("/"))
	b.WriteString(diffDelStyle.Render(fmt.Sprintf("-%d", removed)))
	b.WriteString(cmdLabelStyle.Render(" linhas)"))
	if truncated {
		b.WriteString("\n")
		b.WriteString(notifyWarnStyle.Render("⚠ diff truncado para caber no contexto do modelo"))
	}
	return b.String()
}

// formatSecurityHeader builds the styled top banner for /security-review.
func formatSecurityHeader(label string, files int, truncated bool) string {
	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("═══ SECURITY REVIEW ═══"))
	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("Target: "))
	b.WriteString(cmdValueStyle.Render(label))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d arquivos)", files)))
	if truncated {
		b.WriteString("\n")
		b.WriteString(notifyWarnStyle.Render("⚠ entrada truncada para caber no contexto do modelo"))
	}
	return b.String()
}

// formatAIBlock applies light highlighting to the AI's prose response so
// section titles and severity tags pop in the terminal.
func formatAIBlock(text string) string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "📋") || strings.HasPrefix(trim, "✅") ||
			strings.HasPrefix(trim, "⚠️") || strings.HasPrefix(trim, "🔧") ||
			strings.HasPrefix(trim, "🚨") || strings.HasPrefix(trim, "ℹ️") ||
			strings.HasPrefix(trim, "📝") || strings.HasPrefix(trim, "✓"):
			b.WriteString(cmdTitleStyle.Render(line))
		case strings.HasPrefix(trim, "[ALTA]") || strings.HasPrefix(trim, "[CRITICA]"):
			b.WriteString(notifyErrorStyle.Render(line))
		case strings.HasPrefix(trim, "[MEDIA]"):
			b.WriteString(notifyWarnStyle.Render(line))
		case strings.HasPrefix(trim, "[BAIXA]"):
			b.WriteString(cmdBadgeStyle.Render(line))
		default:
			b.WriteString(cmdPreviewStyle.Render(line))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// countDiffLines tallies added and removed lines from a unified diff,
// ignoring the +++/--- header lines.
func countDiffLines(diff string) (added, removed int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return
}

// runGitCapture invokes Git with separate arguments so a file or ref never
// becomes shell syntax, and keeps it in the active conversation worktree.
func runGitCapture(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// countGitFiles runs `git <args...>` and returns the number of non-empty
// lines in its output. Returns 0 on error.
func countGitFiles(dir string, args ...string) int {
	out, err := runGitCapture(dir, args...)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// detectDefaultBranch tries a few heuristics to find the repository's
// default branch (the base for /review --pr). Falls back to "main" if it
// cannot determine one with confidence.
func detectDefaultBranch(dir string) (string, error) {
	// Most reliable: ask the origin remote.
	if out, err := runGitCapture(dir, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		// "refs/remotes/origin/main" → "origin/main"
		out = strings.TrimSpace(out)
		out = strings.TrimPrefix(out, "refs/remotes/")
		return out, nil
	}
	// Fallbacks: common defaults.
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := runGitCapture(dir, "rev-parse", "--verify", candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("nao foi possivel detectar a branch padrao do repo")
}

// collectRepoSources walks the working directory and concatenates source
// files for the /security-review --full target. It skips binaries,
// vendored code, build outputs and large files.
func collectRepoSources(wd string) (string, int, error) {

	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"__pycache__": true, "dist": true, "build": true,
		".next": true, ".cache": true, ".idea": true,
		".vscode": true, "target": true, "coverage": true,
		".polypod": true,
	}
	allowExt := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true,
		".jsx": true, ".tsx": true, ".rb": true, ".java": true,
		".kt": true, ".rs": true, ".c": true, ".h": true,
		".cpp": true, ".hpp": true, ".cs": true, ".php": true,
		".sh": true, ".sql": true, ".html": true, ".tmpl": true,
	}

	var b strings.Builder
	count := 0
	const maxFileBytes = 64 * 1024
	const maxTotalBytes = 600 * 1024

	err := filepath.Walk(wd, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		name := info.Name()
		if info.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowExt[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		if info.Size() > maxFileBytes {
			return nil
		}
		if b.Len() > maxTotalBytes {
			return filepath.SkipAll
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(wd, p)
		b.WriteString("// FILE: ")
		b.WriteString(rel)
		b.WriteString("\n")
		b.Write(data)
		b.WriteString("\n\n")
		count++
		return nil
	})
	if err != nil {
		return "", count, err
	}
	return b.String(), count, nil
}
