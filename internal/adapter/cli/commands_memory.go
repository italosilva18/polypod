package cli

import (
	"fmt"
	"strings"
)

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
