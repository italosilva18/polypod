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
		return commandResult{output: "Uso: /agent switch <nome>", handled: true}

	case "/skills":
		return handleSkills(deps)

	case "/memory":
		return handleMemory(parts, deps)

	case "/model":
		return handleModel(deps)

	case "/session":
		return handleSession(deps)

	default:
		return commandResult{output: fmt.Sprintf("Comando desconhecido: %s (digite /help)", cmd), handled: true}
	}
}

func helpText() string {
	return `Comandos disponiveis:
  /help                  Lista comandos
  /clear                 Limpa historico da sessao
  /quit, /exit, /sair    Sai do programa
  /agents                Lista agentes disponiveis
  /agent switch <nome>   Troca de agente ativo
  /skills                Lista skills do agente atual
  /memory list           Lista memorias
  /memory search <q>     Busca memorias
  /model                 Mostra modelo atual
  /session               Info da sessao`
}

func handleClear(deps commandDeps) commandResult {
	if deps.clearSession == nil {
		return commandResult{output: "Sessao nao disponivel.", handled: true}
	}
	if err := deps.clearSession(); err != nil {
		return commandResult{output: fmt.Sprintf("Erro ao limpar: %v", err), handled: true}
	}
	return commandResult{output: "Historico limpo.", handled: true}
}

func handleAgents(deps commandDeps) commandResult {
	if deps.listAgents == nil {
		return commandResult{output: "Registry de agentes nao disponivel.", handled: true}
	}
	names := deps.listAgents()
	sort.Strings(names)

	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}

	var b strings.Builder
	b.WriteString("Agentes disponiveis:\n")
	for _, name := range names {
		marker := "  "
		if name == active {
			marker = "* "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", marker, name))
	}
	return commandResult{output: b.String(), handled: true}
}

func handleAgentSwitch(deps commandDeps, name string) commandResult {
	if deps.setAgent == nil {
		return commandResult{output: "Servico AI nao disponivel.", handled: true}
	}
	deps.setAgent(name)
	return commandResult{output: fmt.Sprintf("Agente trocado para: %s", name), handled: true}
}

func handleSkills(deps commandDeps) commandResult {
	if deps.listSkills == nil {
		return commandResult{output: "Registry de skills nao disponivel.", handled: true}
	}
	list := deps.listSkills()
	sort.Strings(list)
	return commandResult{output: "Skills: " + strings.Join(list, ", "), handled: true}
}

func handleMemory(parts []string, deps commandDeps) commandResult {
	if deps.listMemories == nil {
		return commandResult{output: "Memory store nao disponivel.", handled: true}
	}

	if len(parts) < 2 {
		return commandResult{output: "Uso: /memory list | /memory search <query>", handled: true}
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "list":
		return handleMemoryList(deps)
	case "search":
		if len(parts) < 3 {
			return commandResult{output: "Uso: /memory search <query>", handled: true}
		}
		query := strings.Join(parts[2:], " ")
		return handleMemorySearch(deps, query)
	default:
		return commandResult{output: "Uso: /memory list | /memory search <query>", handled: true}
	}
}

func handleMemoryList(deps commandDeps) commandResult {
	memories, err := deps.listMemories()
	if err != nil {
		return commandResult{output: fmt.Sprintf("Erro: %v", err), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{output: "Nenhuma memoria salva.", handled: true}
	}
	var b strings.Builder
	b.WriteString("Memorias:\n")
	for _, m := range memories {
		b.WriteString(fmt.Sprintf("  [%s]: %s\n", m.topic, truncate(m.content, 80)))
	}
	return commandResult{output: b.String(), handled: true}
}

func handleMemorySearch(deps commandDeps, query string) commandResult {
	memories, err := deps.searchMemories(query)
	if err != nil {
		return commandResult{output: fmt.Sprintf("Erro: %v", err), handled: true}
	}
	if len(memories) == 0 {
		return commandResult{output: "Nenhuma memoria encontrada.", handled: true}
	}
	var b strings.Builder
	b.WriteString("Resultados:\n")
	for _, m := range memories {
		b.WriteString(fmt.Sprintf("  [%s]: %s\n", m.topic, truncate(m.content, 80)))
	}
	return commandResult{output: b.String(), handled: true}
}

func handleModel(deps commandDeps) commandResult {
	if deps.activeAgent == nil {
		return commandResult{output: "Servico AI nao disponivel.", handled: true}
	}
	return commandResult{output: fmt.Sprintf("Agente ativo: %s", deps.activeAgent()), handled: true}
}

func handleSession(deps commandDeps) commandResult {
	if deps.sessionInfo == nil {
		return commandResult{output: "Sessao nao disponivel.", handled: true}
	}
	id, count, err := deps.sessionInfo()
	if err != nil {
		return commandResult{output: fmt.Sprintf("Erro: %v", err), handled: true}
	}
	active := ""
	if deps.activeAgent != nil {
		active = deps.activeAgent()
	}
	return commandResult{
		output:  fmt.Sprintf("Sessao: %s\nMensagens: %d\nAgente: %s", id, count, active),
		handled: true,
	}
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
