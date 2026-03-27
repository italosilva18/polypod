package modes

import (
	"fmt"
	"strings"
)

// Mode defines how the AI operates.
type Mode string

const (
	ModeEdit Mode = "edit" // Default: edits files, runs commands (with permission)
	ModePlan Mode = "plan" // Read-only analysis: no file writes, no command execution
	ModeAsk  Mode = "ask"  // Only answers questions, no tool use at all
	ModeAuto Mode = "auto" // Fully autonomous: edits without asking permission
)

// AllModes for iteration.
var AllModes = []Mode{ModeEdit, ModePlan, ModeAsk, ModeAuto}

// State holds the current operational mode and thinking config.
type State struct {
	Current       Mode
	ThinkTokens   int    // reasoning token budget (0 = default)
	ThinkEnabled  bool   // extended thinking on/off
	ArchitectMode bool   // use dual-model architect/editor
	ArchitectModel string // model for planning (empty = same)
	EditorModel    string // model for editing (empty = same)
}

// NewState creates a default state (edit mode).
func NewState() *State {
	return &State{
		Current:      ModeEdit,
		ThinkEnabled: false,
	}
}

// SetMode changes the operating mode. Returns description.
func (s *State) SetMode(mode string) (string, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(mode)))
	switch m {
	case ModeEdit:
		s.Current = m
		return "Modo **edit**: Edita arquivos e executa comandos (com permissao).", nil
	case ModePlan:
		s.Current = m
		return "Modo **plan**: Apenas analisa e planeja. Nenhum arquivo sera modificado.", nil
	case ModeAsk:
		s.Current = m
		return "Modo **ask**: Apenas responde perguntas. Nenhuma ferramenta sera usada.", nil
	case ModeAuto:
		s.Current = m
		return "Modo **auto**: Totalmente autonomo. Edita sem pedir permissao.", nil
	default:
		return "", fmt.Errorf("modo desconhecido '%s'. Use: edit, plan, ask, auto", mode)
	}
}

// BlockedTools returns tool names that should be blocked in the current mode.
func (s *State) BlockedTools() []string {
	switch s.Current {
	case ModeAsk:
		// Block all tools
		return []string{"*"}
	case ModePlan:
		// Block write/execute tools, allow read tools
		return []string{
			"run_command", "create_file", "edit_file", "delete_file", "patch_file",
			"git_commit", "git_push", "git_merge", "git_cherry_pick", "git_stash",
			"git_tag", "git_init", "git_clone", "git_pull",
			"sandbox_run", "sandbox_script",
			"scheduler_add", "scheduler_remove",
			"plugin_install", "plugin_remove",
			"mcp_connect", "mcp_disconnect",
		}
	default:
		return nil
	}
}

// SystemPromptPrefix returns mode-specific instructions prepended to the system prompt.
func (s *State) SystemPromptPrefix() string {
	switch s.Current {
	case ModePlan:
		return `[MODO PLAN] Voce esta em modo de planejamento. Regras:
1. NAO modifique nenhum arquivo.
2. NAO execute nenhum comando destrutivo.
3. Analise, planeje e descreva o que FARIA, passo a passo.
4. Voce PODE ler arquivos, buscar no codebase e analisar codigo.
5. Formate seu plano como uma lista numerada com detalhes.

`
	case ModeAsk:
		return `[MODO ASK] Voce esta em modo pergunta-resposta. Regras:
1. NAO use nenhuma ferramenta.
2. Apenas responda perguntas com seu conhecimento.
3. Seja conciso e direto.

`
	case ModeAuto:
		return `[MODO AUTO] Voce esta em modo autonomo. Regras:
1. Faca todas as mudancas necessarias sem pedir confirmacao.
2. Execute comandos livremente.
3. Seja eficiente e complete a tarefa integralmente.

`
	default:
		return ""
	}
}

// ThinkingPrompt returns instructions for extended thinking.
func (s *State) ThinkingPrompt() string {
	if !s.ThinkEnabled {
		return ""
	}
	budget := "padrao"
	if s.ThinkTokens > 0 {
		budget = fmt.Sprintf("%d tokens", s.ThinkTokens)
	}
	return fmt.Sprintf(`[THINKING] Raciocine detalhadamente antes de responder. Budget: %s.
Mostre seu raciocinio entre <thinking> e </thinking> antes da resposta final.

`, budget)
}

// ArchitectPrompt returns instructions for architect mode.
func (s *State) ArchitectPrompt() string {
	if !s.ArchitectMode {
		return ""
	}
	return `[ARCHITECT MODE] Voce e o Arquiteto. Regras:
1. Analise o problema e planeje QUAIS arquivos mudar e COMO.
2. NAO escreva codigo completo — descreva as mudancas em linguagem natural.
3. Seja preciso: indique linhas, funcoes, e trechos exatos a alterar.
4. Seu plano sera executado por um Editor que aplicara as mudancas.

`
}
