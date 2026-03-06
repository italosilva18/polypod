package cli

import (
	"fmt"
	"sort"
	"strings"
)

// commandResult holds the output of a local slash command.
type commandResult struct {
	output  string
	handled bool
	quit    bool
}

// processCommand checks if input is a slash command and executes it locally.
func processCommand(input string, deps commandDeps) commandResult {
	input = strings.TrimSpace(input)
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
		{"/clear", "limpa historico da sessao"},
		{"/agents", "lista agentes disponiveis"},
		{"/agent switch <nome>", "troca de agente ativo"},
		{"/skills", "lista skills do agente atual"},
		{"/memory list", "lista memorias salvas"},
		{"/memory search <q>", "busca memorias"},
		{"/model", "mostra agente/modelo atual"},
		{"/session", "info da sessao"},
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
	b.WriteString(cmdLabelStyle.Render("=historico"))

	return b.String()
}

func handleClear(deps commandDeps) commandResult {
	if deps.clearSession == nil {
		return commandResult{output: cmdLabelStyle.Render("sessao nao disponivel"), handled: true}
	}
	if err := deps.clearSession(); err != nil {
		return commandResult{output: errorStyle.Render(fmt.Sprintf("erro: %v", err)), handled: true}
	}
	return commandResult{output: notifySuccessStyle.Render("historico limpo"), handled: true}
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
