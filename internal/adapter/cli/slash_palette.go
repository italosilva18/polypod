package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/costa/polypod/internal/models"
)

type slashSuggestion struct {
	value       string
	description string
	kind        string // command, skill, provider, model
	provider    string
	model       string
}

var slashDescriptions = map[string]string{
	"/help":     "Lista os comandos",
	"/provider": "Escolhe provedor e modelo",
	"/model":    "Escolhe provedor e modelo",
	"/connect":   "Conecta uma conta ou chave de API",
	"/auth":      "Gerencia contas OAuth conectadas (multi-conta)",
	"/effort":    "Ajusta o orçamento de raciocínio profundo (low|medium|high|max)",
	"/architect": "Executa planejamento de Arquiteto + aplicação cirúrgica do Editor",
	"/bestof":    "Executa Best-of-N paralelo em worktrees isoladas verificadas",
	"/waterfall": "Mostra a cascata visual de execução do turno (tempos de LLM e tools)",
	"/questions": "Lista perguntas simultâneas pendentes de subagentes",
	"/answer":    "Responde a uma pergunta de subagente (/answer <id> <resposta>)",
	"/editor":    "Edita o texto no editor externo",
	"/btw":      "Faz uma pergunta rápida sem alterar a conversa",
	"/config":   "Abre a configuração",
	"/mcp":      "Gerencia conexões MCP",
	"/plan":     "Gerencia o modo de planejamento",
	"/agents":   "Lista agentes",
	"/session":  "Mostra a sessão atual",
	"/sessions": "Lista sessões",
	"/doctor":   "Diagnostica a Poly",
	"/quit":     "Sai da Poly",
}

func (m model) slashPaletteItems() []slashSuggestion {
	if m.state != stateIdle || m.waitingForUserAnswer || m.currentApproval != nil ||
		m.configForm != nil || m.historySearch != nil || m.quickOpen != nil || m.modelPicker != nil {
		return nil
	}
	input := m.textarea.Value()
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, "\r\n") || input == m.paletteDismissedInput {
		return nil
	}
	lower := strings.ToLower(input)
	for _, command := range []string{"/provider", "/model"} {
		if !strings.HasPrefix(lower, command+" ") || m.cmdDeps.listProviders == nil {
			continue
		}
		rest := input[len(command)+1:]
		providerPart, modelPart, hasModel := strings.Cut(rest, " ")
		if !hasModel {
			var out []slashSuggestion
			for _, option := range m.cmdDeps.listProviders() {
				if !strings.Contains(strings.ToLower(option.Name), strings.ToLower(providerPart)) {
					continue
				}
				status := "disponível"
				if !option.Available {
					status = "configuração necessária"
				}
				out = append(out, slashSuggestion{value: command + " " + option.Name + " ", description: status, kind: "provider", provider: option.Name})
			}
			return out
		}
		providerPart = strings.ToLower(providerPart)
		for _, option := range m.cmdDeps.listProviders() {
			if option.Name != providerPart {
				continue
			}
			return modelSuggestions(command, option, modelPart)
		}
		return nil
	}

	seen := make(map[string]int)
	var out []slashSuggestion
	add := func(value, description string) {
		if value == "" || !strings.HasPrefix(strings.ToLower(value), lower) {
			return
		}
		if index, exists := seen[value]; exists {
			if description != "" && out[index].description == "Comando disponível" {
				out[index].description = description
			}
			return
		}
		if description == "" {
			description = slashDescriptions[strings.TrimSpace(value)]
		}
		if description == "" {
			description = "Comando disponível"
		}
		seen[value] = len(out)
		out = append(out, slashSuggestion{value: value, description: description, kind: "command"})
	}
	for _, value := range slashCommands {
		add(value, "")
	}
	if m.cmdDeps.listSkills != nil && !strings.Contains(input, " ") {
		for _, skillName := range m.cmdDeps.listSkills() {
			value := "/" + skillName + ":skill"
			if strings.HasPrefix(strings.ToLower(value), lower) {
				seen[value] = len(out)
				out = append(out, slashSuggestion{value: value, description: "Usar a skill " + skillName,
					kind: "skill"})
			}
		}
	}
	if m.cmdDeps.commandRegistry != nil && !strings.Contains(input, " ") {
		for _, registered := range m.cmdDeps.commandRegistry.ListEnabled() {
			description := strings.ToLower(registered.Description)
			if registered.Name == "provider-switch" || strings.Contains(description, "pending") ||
				strings.Contains(description, "em fila") || strings.Contains(description, "pendente") ||
				strings.Contains(description, "placeholder") {
				continue
			}
			add("/"+registered.Name, registered.Description)
		}
	}
	if len(out) == 1 && out[0].value == input && strings.HasSuffix(input, " ") {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].value == input {
			return true
		}
		if out[j].value == input {
			return false
		}
		rank := func(value string) int {
			switch value {
			case "/provider":
				return 0
			case "/model":
				return 1
			case "/help":
				return 2
			default:
				return 3
			}
		}
		if rank(out[i].value) != rank(out[j].value) {
			return rank(out[i].value) < rank(out[j].value)
		}
		return out[i].value < out[j].value
	})
	return out
}

func modelSuggestions(command string, option ProviderOption, query string) []slashSuggestion {
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] || !strings.Contains(strings.ToLower(id), strings.ToLower(query)) {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	add(option.Model)
	for _, candidate := range models.ListAllModels() {
		if candidate != nil && !candidate.IsDeprecated && candidate.Provider == option.Name {
			add(candidate.ID)
		}
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if ids[i] == option.Model {
			return true
		}
		if ids[j] == option.Model {
			return false
		}
		return ids[i] < ids[j]
	})
	out := make([]slashSuggestion, 0, len(ids))
	for _, id := range ids {
		out = append(out, slashSuggestion{value: command + " " + option.Name + " " + id,
			description: "Usar este modelo", kind: "model", provider: option.Name, model: id})
	}
	return out
}

func (m model) slashPaletteView() string {
	items := m.slashPaletteItems()
	if len(items) == 0 {
		return ""
	}
	selected := min(max(m.paletteSelection, 0), len(items)-1)
	maxVisible := min(6, max(2, m.height/4))
	start := 0
	if selected >= maxVisible {
		start = selected - maxVisible + 1
	}
	end := min(len(items), start+maxVisible)
	width := max(28, m.width-8)
	if m.width > 0 {
		width = min(width, m.width-4)
	}
	var b strings.Builder
	title := "Comandos e skills"
	if items[0].kind == "provider" {
		title = "Provedores"
	}
	if items[0].kind == "model" {
		title = "Modelos"
	}
	if items[0].kind == "skill" {
		title = "Skills"
	}
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#67E8F9")).Render(title))
	count := fmt.Sprintf("%d opções", len(items))
	if len(items) == 1 {
		count = "1 opção"
	}
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render("  " + count))
	for i := start; i < end; i++ {
		item := items[i]
		b.WriteString("\n")
		prefix := "  "
		if i == selected {
			prefix = "› "
		}
		line := prefix + item.value
		if item.description != "" {
			line += "  ·  " + item.description
		}
		line = ansi.Truncate(line, max(12, width-4), "…")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("#CBD5E1"))
		if i == selected {
			style = style.Bold(true).Foreground(lipgloss.Color("#F8FAFC")).Background(lipgloss.Color("#334155"))
		}
		b.WriteString(style.Render(line))
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render("↑↓ navegar  Tab completar  Enter escolher  Esc fechar"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#475569")).
		Padding(0, 1).Width(width).Render(b.String())
}

func (m model) slashPaletteHeight() int {
	view := m.slashPaletteView()
	if view == "" {
		return 0
	}
	return lipgloss.Height(view) + 1
}

func (m *model) moveSlashPalette(direction int) bool {
	items := m.slashPaletteItems()
	if len(items) == 0 {
		return false
	}
	m.paletteSelection = (m.paletteSelection + direction + len(items)) % len(items)
	return true
}

func (m *model) acceptSlashPalette(execute bool) (bool, tea.Cmd) {
	items := m.slashPaletteItems()
	if len(items) == 0 {
		return false, nil
	}
	selected := min(max(m.paletteSelection, 0), len(items)-1)
	item := items[selected]
	input := m.textarea.Value()
	if item.kind == "command" && item.value == input {
		if !execute && (input == "/provider" || input == "/model") {
			m.textarea.SetValue(input + " ")
			m.textarea.CursorEnd()
			m.paletteSelection = 0
			return true, nil
		}
		return false, nil
	}
	if item.kind == "model" && execute {
		if m.cmdDeps.switchProvider == nil {
			return false, nil
		}
		if err := m.cmdDeps.switchProvider(item.provider, item.model); err != nil {
			m.notification = "Erro ao trocar provedor: " + err.Error()
			m.notifyLevel = "error"
			m.restorePaletteDraft()
			return true, clearNotifyAfter(5*time.Second, m.notification)
		}
		m.textarea.SetValue("")
		m.textarea.Reset()
		m.restorePaletteDraft()
		m.notification = fmt.Sprintf("Provedor: %s · modelo: %s", item.provider, item.model)
		m.notifyLevel = "success"
		m.refreshStatusBar()
		return true, clearNotifyAfter(3*time.Second, m.notification)
	}
	if item.kind == "skill" {
		name := strings.TrimSuffix(strings.TrimPrefix(item.value, "/"), ":skill")
		draft := ""
		if m.paletteDraftValid {
			draft = m.paletteDraft
			m.paletteDraft, m.paletteDraftValid = "", false
		}
		m.textarea.SetValue("Use a skill " + name + " para " + draft)
		m.textarea.CursorEnd()
		m.paletteSelection = 0
		return true, nil
	}
	m.textarea.SetValue(item.value)
	m.textarea.CursorEnd()
	m.paletteSelection = 0
	m.resetTab()
	if item.kind == "command" && execute && (item.value == "/provider" || item.value == "/model") {
		m.textarea.SetValue("")
		m.textarea.Reset()
		m.restorePaletteDraft()
		return true, m.openModelPicker()
	}
	if item.kind == "command" && execute && item.value == "/connect" {
		m.textarea.SetValue("")
		m.textarea.Reset()
		m.restorePaletteDraft()
		m.openConnectPicker()
	}
	return true, nil
}

func (m *model) restorePaletteDraft() {
	if !m.paletteDraftValid {
		return
	}
	m.textarea.SetValue(m.paletteDraft)
	m.textarea.CursorEnd()
	m.paletteDraft = ""
	m.paletteDraftValid = false
}
