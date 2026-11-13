package cli

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/models"
)

// ── Model Picker (Ctrl+Y / /model) ──────────────────────────────────────────
//
// modelPickerModel is a Bubbletea overlay that lets the user fuzzy-search
// the model database (internal/models/capabilities.go) and pick a new
// model for the active session. Selecting a model returns its ID; the
// caller is responsible for actually swapping it on the AI client.

const modelPickerMaxResults = 12

// ModelInfo is a flat, display-oriented snapshot of a single model
// (decoupled from the richer ModelCapabilities so the picker view does
// not need to import the underlying types every time it renders).
type ModelInfo struct {
	ID            string
	Provider      string
	Display       string
	MaxContext    int
	InputCost     float64
	OutputCost    float64
	SupportsTools bool
	Quality       string // "low", "medium", "high", "frontier"
	Speed         string // "fast", "medium", "slow"
}

// ProviderOption describes an available backend without exposing credentials.
type ProviderOption struct {
	Name      string
	Model     string
	Available bool
	Detail    string
	BaseURL   string
}

// modelPickerModel holds picker state.
type modelPickerModel struct {
	query           string
	models          []ModelInfo
	matches         []ModelInfo
	selected        int
	width           int
	height          int
	currentModel    string
	currentProvider string
	providers       []ProviderOption
	providerStage   bool
	providerName    string
	connectMode     bool
	keyEntry        bool
	keyValue        string
	catalogLoading  bool
	catalogOffline  bool
}

// modelPickerCancelSentinel is returned by Update when the user presses
// Esc. The caller treats this as "close picker, no change".
const modelPickerCancelSentinel = "\x00cancel"

// newModelPicker builds a picker, marking the given currentModel so it
// can be displayed in the footer.
func newModelPicker(currentModel string, providers ...ProviderOption) *modelPickerModel {
	p := &modelPickerModel{
		currentModel:  currentModel,
		providers:     providers,
		providerStage: len(providers) > 0,
	}
	p.models = p.loadModels()
	for _, option := range providers {
		if option.Model != "" {
			found := false
			for _, candidate := range p.models {
				if candidate.Provider == option.Name && candidate.ID == option.Model {
					found = true
					break
				}
			}
			if !found {
				p.models = append(p.models, ModelInfo{ID: option.Model, Provider: option.Name, Display: option.Model, SupportsTools: true})
			}
		}
	}
	p.matches = p.filter("")
	return p
}

// SetSize updates the cached terminal dimensions used by View().
func (m *modelPickerModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// loadModels pulls every model from the capabilities database and
// flattens it into the picker's display struct.
func (m *modelPickerModel) loadModels() []ModelInfo {
	caps := models.ListAllModels()
	out := make([]ModelInfo, 0, len(caps))
	for _, c := range caps {
		if c == nil || c.IsDeprecated {
			continue
		}
		out = append(out, ModelInfo{
			ID:            c.ID,
			Provider:      c.Provider,
			Display:       c.DisplayName,
			MaxContext:    c.MaxContextTokens,
			InputCost:     c.InputPricePer1M,
			OutputCost:    c.OutputPricePer1M,
			SupportsTools: c.SupportsTools,
			Quality:       c.Quality,
			Speed:         c.Speed,
		})
	}
	// Sort by quality (frontier first) then provider then ID for a
	// stable, friendly default ordering.
	sort.SliceStable(out, func(i, j int) bool {
		qi := qualityRank(out[i].Quality)
		qj := qualityRank(out[j].Quality)
		if qi != qj {
			return qi > qj
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// qualityRank returns a sortable integer for quality tiers.
func qualityRank(q string) int {
	switch strings.ToLower(q) {
	case "frontier":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

// Update routes keys, returning the (possibly mutated) picker, an
// optional command, and a non-empty string when the user has either
// confirmed a selection (the model ID) or cancelled
// (modelPickerCancelSentinel).
func (m *modelPickerModel) Update(msg tea.Msg) (*modelPickerModel, tea.Cmd, string) {
	if m == nil {
		return m, nil, ""
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil, ""

	case tea.KeyMsg:
		if m.keyEntry {
			switch msg.Type {
			case tea.KeyEsc:
				m.keyEntry = false
				m.keyValue = ""
				return m, nil, ""
			case tea.KeyCtrlO:
				if accountauth.IsOAuthProvider(m.providerName) {
					m.keyEntry = false
					m.keyValue = ""
					return m, nil, "\x1eoauth\x1f" + m.providerName
				}
				return m, nil, ""
			case tea.KeyEnter:
				if strings.TrimSpace(m.keyValue) == "" {
					if m.providerName == "openrouter" {
						m.keyEntry = false
						return m, nil, "\x1eoauth\x1fopenrouter"
					}
					return m, nil, ""
				}
				value := "\x1econnect\x1f" + m.providerName + "\x1f" + m.keyValue
				m.keyValue = ""
				return m, nil, value
			case tea.KeyBackspace:
				if len(m.keyValue) > 0 {
					runes := []rune(m.keyValue)
					m.keyValue = string(runes[:len(runes)-1])
				}
				return m, nil, ""
			case tea.KeyRunes, tea.KeySpace:
				if msg.Type == tea.KeySpace {
					m.keyValue += " "
				} else {
					m.keyValue += string(msg.Runes)
				}
				return m, nil, ""
			}
			return m, nil, ""
		}
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			if m.providerName != "" && msg.Type == tea.KeyEsc {
				m.providerName = ""
				m.providerStage = true
				m.query = ""
				m.selected = 0
				return m, nil, ""
			}
			return m, nil, modelPickerCancelSentinel
		case tea.KeyEnter:
			if m.providerStage {
				options := m.filteredProviders()
				if m.selected >= 0 && m.selected < len(options) {
					m.providerName = options[m.selected].Name
					if m.connectMode || !options[m.selected].Available {
						if m.providerName == "chatgpt" {
							return m, nil, "\x1eoauth"
						}
						if m.providerName == "ollama" {
							return m, nil, "\x1eollama"
						}
						m.providerStage = false
						m.keyEntry = true
						m.query = ""
						return m, nil, ""
					}
					m.providerStage = false
					m.query = ""
					m.selected = 0
					m.matches = m.filter("")
				}
				return m, nil, ""
			}
			if m.selected >= 0 && m.selected < len(m.matches) {
				if m.providerName != "" {
					return m, nil, m.providerName + "\x1f" + m.matches[m.selected].ID
				}
				return m, nil, m.matches[m.selected].ID
			}
			return m, nil, ""
		case tea.KeyUp, tea.KeyCtrlP:
			if m.selected > 0 {
				m.selected--
			}
			return m, nil, ""
		case tea.KeyDown, tea.KeyCtrlN:
			count := len(m.matches)
			if m.providerStage {
				count = len(m.filteredProviders())
			}
			if m.selected < count-1 {
				m.selected++
			}
			return m, nil, ""
		case tea.KeyBackspace:
			if len(m.query) > 0 {
				r := []rune(m.query)
				m.query = string(r[:len(r)-1])
				if !m.providerStage {
					m.matches = m.filter(m.query)
				}
				m.selected = 0
			}
			return m, nil, ""
		case tea.KeyRunes, tea.KeySpace:
			var s string
			if msg.Type == tea.KeySpace {
				s = " "
			} else {
				s = string(msg.Runes)
			}
			if s != "" {
				m.query += s
				if !m.providerStage {
					m.matches = m.filter(m.query)
				}
				m.selected = 0
			}
			return m, nil, ""
		}
	}
	return m, nil, ""
}

// filter narrows the model list down to those whose ID, display name,
// or provider contain the query as a case-insensitive substring. When
// query is empty all models are returned.
func (m *modelPickerModel) filter(query string) []ModelInfo {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" && m.providerName == "" {
		return append([]ModelInfo(nil), m.models...)
	}
	var out []ModelInfo
	for _, mi := range m.models {
		if m.providerName != "" && mi.Provider != m.providerName {
			continue
		}
		if strings.Contains(strings.ToLower(mi.ID), query) ||
			strings.Contains(strings.ToLower(mi.Display), query) ||
			strings.Contains(strings.ToLower(mi.Provider), query) {
			out = append(out, mi)
		}
	}
	return out
}

func (m *modelPickerModel) filteredProviders() []ProviderOption {
	query := strings.ToLower(strings.TrimSpace(m.query))
	var out []ProviderOption
	for _, option := range m.providers {
		if query == "" || strings.Contains(strings.ToLower(option.Name), query) {
			out = append(out, option)
		}
	}
	return out
}

// Overlay returns the picker rendered centered on a screen of the
// given dimensions. Used by the host model's View().
func (m *modelPickerModel) Overlay(width, height int) string {
	if m == nil {
		return ""
	}
	body := m.View()
	if width <= 0 || height <= 0 {
		return body
	}
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// View renders the picker as a bordered overlay.
func (m *modelPickerModel) View() string {
	if m == nil {
		return ""
	}

	width := m.width
	if width <= 0 {
		width = 80
	}
	boxWidth := width - 6
	if boxWidth > 90 {
		boxWidth = 90
	}
	if boxWidth < 60 {
		boxWidth = 60
	}

	var b strings.Builder
	if m.keyEntry {
		b.WriteString(modelPickerHeaderStyle.Render("Conectar " + m.providerName))
		b.WriteString("\n\n  Chave de API: " + strings.Repeat("•", len([]rune(m.keyValue))) + "▍")
		if accountauth.IsOAuthProvider(m.providerName) {
			b.WriteString("\n\n  Enter salvar chave  Ctrl+O conectar conta OAuth  Esc voltar")
		} else {
			b.WriteString("\n\n  Enter salvar com segurança  Esc voltar")
		}
		return modelPickerBoxStyle.Width(boxWidth).Render(b.String())
	}

	// Header.
	header := fmt.Sprintf("Escolher modelo: %s", m.query)
	if m.providerStage {
		header = fmt.Sprintf("Escolher provedor: %s", m.query)
		if m.connectMode {
			header = fmt.Sprintf("Conectar provedor: %s", m.query)
		}
	}
	b.WriteString(modelPickerHeaderStyle.Render(header))
	b.WriteString(modelPickerCursorStyle.Render("▍"))
	b.WriteString("\n")
	b.WriteString(modelPickerSepStyle.Render(strings.Repeat("─", boxWidth-4)))
	b.WriteString("\n\n")

	// Provider list.
	if m.providerStage {
		options := m.filteredProviders()
		if len(options) == 0 {
			b.WriteString("  Nenhum provedor encontrado\n")
		}
		for i, option := range options {
			marker := "  "
			if i == m.selected {
				marker = "► "
			}
			status := "pronto"
			if !option.Available {
				status = "configuração necessária"
			}
			b.WriteString(marker + option.Name + "  [" + status + "]\n")
			if option.Detail != "" {
				b.WriteString("    " + option.Detail + "\n")
			}
		}
		b.WriteString("\n  Atual: " + m.currentProvider + " / " + m.currentModel)
		b.WriteString("\n  ↑↓ navegar  Enter escolher  Esc cancelar")
		return modelPickerBoxStyle.Width(boxWidth).Render(b.String())
	}

	// Match list.
	if len(m.matches) == 0 {
		b.WriteString("  ")
		b.WriteString(modelPickerDimStyle.Render("(nenhum modelo)"))
		b.WriteString("\n")
	} else {
		shown := len(m.matches)
		if shown > modelPickerMaxResults {
			shown = modelPickerMaxResults
		}
		start := 0
		if m.selected >= shown {
			start = m.selected - shown + 1
		}
		for i := start; i < start+shown; i++ {
			b.WriteString(m.renderModelLine(m.matches[i], i == m.selected))
			b.WriteString("\n")
		}
	}

	// Footer with current model and key hints.
	b.WriteString("\n")
	if m.currentModel != "" {
		b.WriteString(modelPickerCurrentLabelStyle.Render("  ATUAL: "))
		b.WriteString(modelPickerCurrentValueStyle.Render(m.currentModel))
		b.WriteString("\n")
	}
	footer := "  ↑↓ navegar  Enter escolher  Esc voltar\n  Catálogo público; acesso depende da conta conectada"
	if m.catalogLoading {
		footer = "  ↑↓ navegar  Enter escolher  Esc voltar\n  Atualizando o catálogo em segundo plano..."
	} else if m.catalogOffline {
		footer = "  ↑↓ navegar  Enter escolher  Esc voltar\n  Catálogo indisponível; mostrando a lista local"
	}
	b.WriteString(modelPickerFooterStyle.Render(footer))

	return modelPickerBoxStyle.Width(boxWidth).Render(b.String())
}

// renderModelLine renders a single two-line model entry: name + badges
// on the first line, context+pricing+tools on the second.
func (m *modelPickerModel) renderModelLine(mi ModelInfo, selected bool) string {
	var b strings.Builder

	// Line 1: ► id [provider] quality speedIcon
	if selected {
		b.WriteString(modelPickerSelectedStyle.Render("► "))
	} else {
		b.WriteString("  ")
	}

	idStyle := modelPickerIDStyle
	if selected {
		idStyle = modelPickerSelectedStyle
	}
	b.WriteString(idStyle.Render(mi.ID))
	b.WriteString(" ")
	b.WriteString(modelPickerProviderStyle.Render("[" + mi.Provider + "]"))
	b.WriteString(" ")
	b.WriteString(qualityStyleFor(mi.Quality).Render(qualityLabel(mi.Quality)))
	b.WriteString(" ")
	b.WriteString(modelPickerSpeedStyle.Render(speedIconFor(mi.Speed)))

	if mi.ID == m.currentModel {
		b.WriteString(" ")
		b.WriteString(modelPickerCurrentBadgeStyle.Render("(ativo)"))
	}

	// Line 2: indented details
	b.WriteString("\n    ")
	if mi.MaxContext > 0 {
		b.WriteString(modelPickerDetailLabelStyle.Render(formatTokenContext(mi.MaxContext) + " contexto"))
		b.WriteString("  ")
	}
	if mi.InputCost > 0 || mi.OutputCost > 0 {
		b.WriteString(modelPickerDetailValueStyle.Render(fmt.Sprintf("$%.2f/$%.2f por 1M", mi.InputCost, mi.OutputCost)))
	} else {
		b.WriteString(modelPickerDimStyle.Render("preço não informado"))
	}
	b.WriteString("    ")
	b.WriteString(modelPickerDetailLabelStyle.Render("Ferramentas: "))
	if mi.SupportsTools {
		b.WriteString(modelPickerOkStyle.Render("✓"))
	} else {
		b.WriteString(modelPickerNoStyle.Render("✗"))
	}

	return b.String()
}

// formatTokenContext formats a token count using K/M suffixes.
func formatTokenContext(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%dk", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// speedIconFor maps a speed tier to an emoji.
func speedIconFor(speed string) string {
	switch strings.ToLower(speed) {
	case "fast":
		return "⚡"
	case "slow":
		return "🐢"
	default:
		return "◐"
	}
}

// qualityStyleFor returns the lipgloss style for a quality tier.
func qualityStyleFor(quality string) lipgloss.Style {
	switch strings.ToLower(quality) {
	case "frontier":
		return modelPickerFrontierStyle
	case "high":
		return modelPickerHighStyle
	case "medium":
		return modelPickerMediumStyle
	case "low":
		return modelPickerLowStyle
	}
	return modelPickerLowStyle
}

func qualityLabel(quality string) string {
	switch strings.ToLower(quality) {
	case "frontier":
		return "avançado"
	case "high":
		return "alta"
	case "medium":
		return "média"
	case "low":
		return "baixa"
	default:
		return quality
	}
}

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	modelPickerBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMagenta).
				Padding(1, 2)

	modelPickerHeaderStyle = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	modelPickerCursorStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	modelPickerSepStyle = lipgloss.NewStyle().
				Foreground(colorBorderDim)

	modelPickerSelectedStyle = lipgloss.NewStyle().
					Foreground(colorGreen).
					Bold(true)

	modelPickerIDStyle = lipgloss.NewStyle().
				Foreground(colorWhite).
				Bold(true)

	modelPickerProviderStyle = lipgloss.NewStyle().
					Foreground(colorBlue)

	modelPickerSpeedStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	modelPickerFrontierStyle = lipgloss.NewStyle().
					Foreground(colorMagenta).
					Bold(true)

	modelPickerHighStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	modelPickerMediumStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	modelPickerLowStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	modelPickerDetailLabelStyle = lipgloss.NewStyle().
					Foreground(colorSubtle)

	modelPickerDetailValueStyle = lipgloss.NewStyle().
					Foreground(colorWhiteDim)

	modelPickerOkStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	modelPickerNoStyle = lipgloss.NewStyle().
				Foreground(colorRed)

	modelPickerCurrentLabelStyle = lipgloss.NewStyle().
					Foreground(colorSubtle).
					Bold(true)

	modelPickerCurrentValueStyle = lipgloss.NewStyle().
					Foreground(colorCyan).
					Bold(true)

	modelPickerCurrentBadgeStyle = lipgloss.NewStyle().
					Foreground(colorCyan).
					Italic(true)

	modelPickerDimStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)

	modelPickerFooterStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)
)
