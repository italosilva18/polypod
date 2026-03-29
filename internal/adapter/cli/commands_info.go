package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

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
