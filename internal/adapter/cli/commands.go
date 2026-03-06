package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

// commandResult holds the output of a local slash command.
type commandResult struct {
	output       string
	handled      bool
	quit         bool
	clearDisplay bool // limpa m.messages quando true
}

// processCommand checks if input is a slash command and executes it locally.
func processCommand(input string, deps commandDeps, messages []chatEntry) commandResult {
	input = strings.TrimSpace(input)

	// ! prefix as alias for /run
	if strings.HasPrefix(input, "!") {
		cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
		if cmd == "" {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("!<comando>"), handled: true}
		}
		return handleRun(cmd)
	}

	if !strings.HasPrefix(input, "/") {
		return commandResult{handled: false}
	}

	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		return commandResult{output: helpText(), handled: true}

	case "/quit", "/exit", "/sair":
		return commandResult{quit: true, handled: true}

	case "/clear":
		return handleClear(deps)

	case "/agents":
		return handleAgents(deps)

	case "/agent":
		if len(parts) >= 3 && strings.ToLower(parts[1]) == "switch" {
			return handleAgentSwitch(deps, parts[2])
		}
		return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/agent switch <nome>"), handled: true}

	case "/skills":
		return handleSkills(deps)

	case "/memory":
		return handleMemory(parts, deps)

	case "/model":
		return handleModel(deps)

	case "/session":
		return handleSession(deps)

	case "/copy":
		return handleCopy(messages)

	case "/run":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/run <comando>"), handled: true}
		}
		shellCmd := strings.TrimSpace(strings.TrimPrefix(input, parts[0]))
		return handleRun(shellCmd)

	case "/file":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/file <caminho>"), handled: true}
		}
		return handleFile(parts[1])

	case "/search":
		if len(parts) < 2 {
			return commandResult{output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/search <pattern>"), handled: true}
		}
		pattern := strings.Join(parts[1:], " ")
		return handleSearch(pattern)

	case "/git":
		sub := ""
		if len(parts) >= 2 {
			sub = strings.ToLower(parts[1])
		}
		return handleGit(sub)

	case "/project":
		return handleProject()

	case "/export":
		path := "chat_export.md"
		if len(parts) >= 2 {
			path = parts[1]
		}
		return handleExport(messages, path)

	case "/context":
		return handleContext(deps, messages)

	default:
		return commandResult{
			output: errorStyle.Render("comando desconhecido: ") +
				cmdValueStyle.Render(cmd) + "\n" +
				cmdLabelStyle.Render("digite /help para ver comandos"),
			handled: true,
		}
	}
}

func helpText() string {
	var b strings.Builder

	b.WriteString(cmdTitleStyle.Render("Comandos"))
	b.WriteString("\n\n")

	commands := []struct {
		cmd  string
		desc string
	}{
		{"/help", "lista comandos"},
		{"/clear", "limpa tela e historico da sessao"},
		{"/agents", "lista agentes disponiveis"},
		{"/agent switch <nome>", "troca de agente ativo"},
		{"/skills", "lista skills do agente atual"},
		{"/memory list", "lista memorias salvas"},
		{"/memory search <q>", "busca memorias"},
		{"/model", "mostra agente/modelo atual"},
		{"/session", "info da sessao"},
		{"/copy", "copia ultima resposta pro clipboard"},
		{"/run <cmd>  ou  !<cmd>", "executa comando shell inline"},
		{"/file <path>", "mostra conteudo de arquivo"},
		{"/search <pattern>", "busca texto no projeto (grep)"},
		{"/git [status|log|diff|branch]", "operacoes git comuns"},
		{"/project", "mostra arvore do projeto"},
		{"/export [file]", "exporta conversa pra markdown"},
		{"/context", "mostra info do contexto atual"},
		{"/quit", "sai do programa"},
	}

	maxCmd := 0
	for _, c := range commands {
		if len(c.cmd) > maxCmd {
			maxCmd = len(c.cmd)
		}
	}

	for _, c := range commands {
		padding := strings.Repeat(" ", maxCmd-len(c.cmd)+2)
		b.WriteString("  ")
		b.WriteString(cmdValueStyle.Render(c.cmd))
		b.WriteString(padding)
		b.WriteString(cmdLabelStyle.Render(c.desc))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  atalhos: "))
	b.WriteString(welcomeKeyStyle.Render("Tab"))
	b.WriteString(cmdLabelStyle.Render("=completar  "))
	b.WriteString(welcomeKeyStyle.Render("Esc"))
	b.WriteString(cmdLabelStyle.Render("=limpar  "))
	b.WriteString(welcomeKeyStyle.Render("Up/Down"))
	b.WriteString(cmdLabelStyle.Render("=historico  "))
	b.WriteString(welcomeKeyStyle.Render("Ctrl+L"))
	b.WriteString(cmdLabelStyle.Render("=limpar tela"))

	return b.String()
}

func handleClear(deps commandDeps) commandResult {
	if deps.clearSession == nil {
		return commandResult{output: cmdLabelStyle.Render("sessao nao disponivel"), handled: true}
	}
	if err := deps.clearSession(); err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	return commandResult{output: notifySuccessStyle.Render("historico limpo"), handled: true, clearDisplay: true}
}

func handleAgents(deps commandDeps) commandResult {
	if deps.listAgents == nil {
		return commandResult{output: cmdLabelStyle.Render("registry de agentes nao disponivel"), handled: true}
	}
	names := deps.listAgents()
	sort.Strings(names)

	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Agentes"))
	b.WriteString("\n\n")

	for _, name := range names {
		b.WriteString("  ")
		if name == active {
			b.WriteString(cmdActiveStyle.Render("● " + name))
			b.WriteString(cmdLabelStyle.Render(" (ativo)"))
		} else {
			b.WriteString(cmdInactiveStyle.Render("○ " + name))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(cmdLabelStyle.Render("  trocar: "))
	b.WriteString(cmdValueStyle.Render("/agent switch <nome>"))

	return commandResult{output: b.String(), handled: true}
}

func handleAgentSwitch(deps commandDeps, name string) commandResult {
	if deps.setAgent == nil {
		return commandResult{output: cmdLabelStyle.Render("servico AI nao disponivel"), handled: true}
	}
	deps.setAgent(name)
	return commandResult{
		output: notifySuccessStyle.Render("agente trocado: ") + cmdValueStyle.Render(name),
		handled: true,
	}
}

func handleSkills(deps commandDeps) commandResult {
	if deps.listSkills == nil {
		return commandResult{output: cmdLabelStyle.Render("registry de skills nao disponivel"), handled: true}
	}
	list := deps.listSkills()
	sort.Strings(list)

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Skills"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(list))))
	b.WriteString("\n\n")

	// Display in 2 columns if wide enough
	cols := 1
	colWidth := 30
	if len(list) > 0 {
		for _, s := range list {
			if len(s)+4 > colWidth {
				colWidth = len(s) + 4
			}
		}
	}

	for _, s := range list {
		b.WriteString("  ")
		b.WriteString(cmdBadgeStyle.Render("▸ "))
		b.WriteString(cmdValueStyle.Render(s))
		if cols > 1 {
			padding := colWidth - len(s) - 4
			if padding > 0 {
				b.WriteString(strings.Repeat(" ", padding))
			}
		}
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

func handleMemory(parts []string, deps commandDeps) commandResult {
	if deps.listMemories == nil {
		return commandResult{output: cmdLabelStyle.Render("memory store nao disponivel"), handled: true}
	}

	if len(parts) < 2 {
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/memory list") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/memory search <query>"),
			handled: true,
		}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "list":
		return handleMemoryList(deps)
	case "search":
		if len(parts) < 3 {
			return commandResult{
				output: cmdLabelStyle.Render("uso: ") + cmdValueStyle.Render("/memory search <query>"),
				handled: true,
			}
		}
		query := strings.Join(parts[2:], " ")
		return handleMemorySearch(deps, query)
	default:
		return commandResult{
			output: cmdLabelStyle.Render("uso: ") +
				cmdValueStyle.Render("/memory list") +
				cmdLabelStyle.Render(" | ") +
				cmdValueStyle.Render("/memory search <query>"),
			handled: true,
		}
	}
}

func handleMemoryList(deps commandDeps) commandResult {
	memories, err := deps.listMemories()
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{output: cmdLabelStyle.Render("nenhuma memoria salva"), handled: true}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Memorias"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(memories))))
	b.WriteString("\n\n")

	for _, m := range memories {
		b.WriteString("  ")
		b.WriteString(cmdTopicStyle.Render(m.topic))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdPreviewStyle.Render(truncate(m.content, 72)))
		b.WriteString("\n\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleMemorySearch(deps commandDeps, query string) commandResult {
	memories, err := deps.searchMemories(query)
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{
			output: cmdLabelStyle.Render("nenhuma memoria encontrada para: ") + cmdValueStyle.Render(query),
			handled: true,
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Resultados"))
	b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d)", len(memories))))
	b.WriteString("\n\n")

	for _, m := range memories {
		b.WriteString("  ")
		b.WriteString(cmdTopicStyle.Render(m.topic))
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(cmdPreviewStyle.Render(truncate(m.content, 72)))
		b.WriteString("\n\n")
	}

	return commandResult{output: strings.TrimRight(b.String(), "\n"), handled: true}
}

func handleModel(deps commandDeps) commandResult {
	if deps.activeAgent == nil {
		return commandResult{output: cmdLabelStyle.Render("servico AI nao disponivel"), handled: true}
	}
	return commandResult{
		output: cmdLabelStyle.Render("agente ativo: ") + cmdActiveStyle.Render(deps.activeAgent()),
		handled: true,
	}
}

func handleSession(deps commandDeps) commandResult {
	if deps.sessionInfo == nil {
		return commandResult{output: cmdLabelStyle.Render("sessao nao disponivel"), handled: true}
	}
	id, count, err := deps.sessionInfo()
	if err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Sessao"))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"id", id},
		{"mensagens", fmt.Sprintf("%d", count)},
		{"agente", active},
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}

	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

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
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
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

	if truncated {
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

func handleContext(deps commandDeps, messages []chatEntry) commandResult {
	wd, _ := os.Getwd()

	agent := "n/a"
	if deps.activeAgent != nil {
		if a := deps.activeAgent(); a != "" {
			agent = a
		}
	}

	skillCount := 0
	if deps.listSkills != nil {
		skillCount = len(deps.listSkills())
	}

	memoryCount := 0
	if deps.listMemories != nil {
		if mems, err := deps.listMemories(); err == nil {
			memoryCount = len(mems)
		}
	}

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Contexto"))
	b.WriteString("\n\n")

	rows := []struct{ label, value string }{
		{"mensagens", fmt.Sprintf("%d", len(messages))},
		{"agente", agent},
		{"skills", fmt.Sprintf("%d", skillCount)},
		{"memorias", fmt.Sprintf("%d", memoryCount)},
		{"diretorio", wd},
	}

	maxLabel := 0
	for _, r := range rows {
		if len(r.label) > maxLabel {
			maxLabel = len(r.label)
		}
	}

	for _, r := range rows {
		padding := strings.Repeat(" ", maxLabel-len(r.label))
		b.WriteString("  ")
		b.WriteString(cmdLabelStyle.Render(r.label + padding + "  "))
		b.WriteString(cmdValueStyle.Render(r.value))
		b.WriteString("\n")
	}

	return commandResult{output: b.String(), handled: true}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// memoryEntry is a simplified memory representation for display.
type memoryEntry struct {
	topic   string
	content string
}

// commandDeps holds function callbacks for slash commands.
type commandDeps struct {
	activeAgent    func() string
	setAgent       func(name string)
	listAgents     func() []string
	listSkills     func() []string
	listMemories   func() ([]memoryEntry, error)
	searchMemories func(query string) ([]memoryEntry, error)
	clearSession   func() error
	sessionInfo    func() (string, int, error) // id, count, err
}

// runShellCmd executes a shell command with a 15s timeout and returns its output.
func runShellCmd(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()

	output := string(out)
	if len(output) > 8000 {
		output = output[:8000] + "\n... (truncado em 8000 chars)"
	}

	return strings.TrimRight(output, "\n"), err
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
