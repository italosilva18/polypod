package cli

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// modelPickerModel holds picker state.
type modelPickerModel struct {
	query        string
	models       []ModelInfo
	matches      []ModelInfo
	selected     int
	width        int
	height       int
	currentModel string
}

// modelPickerCancelSentinel is returned by Update when the user presses
// Esc. The caller treats this as "close picker, no change".
const modelPickerCancelSentinel = "\x00cancel"

// newModelPicker builds a picker, marking the given currentModel so it
// can be displayed in the footer.
func newModelPicker(currentModel string) *modelPickerModel {
	p := &modelPickerModel{
		currentModel: currentModel,
	}
	p.models = p.loadModels()
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
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			return m, nil, modelPickerCancelSentinel
		case tea.KeyEnter:
			if m.selected >= 0 && m.selected < len(m.matches) {
				return m, nil, m.matches[m.selected].ID
			}
			return m, nil, ""
		case tea.KeyUp, tea.KeyCtrlP:
			if m.selected > 0 {
				m.selected--
			}
			return m, nil, ""
		case tea.KeyDown, tea.KeyCtrlN:
			if m.selected < len(m.matches)-1 {
				m.selected++
			}
			return m, nil, ""
		case tea.KeyBackspace:
			if len(m.query) > 0 {
				r := []rune(m.query)
				m.query = string(r[:len(r)-1])
				m.matches = m.filter(m.query)
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
				m.matches = m.filter(m.query)
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
	if query == "" {
		return append([]ModelInfo(nil), m.models...)
	}
	var out []ModelInfo
	for _, mi := range m.models {
		if strings.Contains(strings.ToLower(mi.ID), query) ||
			strings.Contains(strings.ToLower(mi.Display), query) ||
			strings.Contains(strings.ToLower(mi.Provider), query) {
			out = append(out, mi)
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

	// Header.
	header := fmt.Sprintf("🤖 Model Picker: %s", m.query)
	b.WriteString(modelPickerHeaderStyle.Render(header))
	b.WriteString(modelPickerCursorStyle.Render("▍"))
	b.WriteString("\n")
	b.WriteString(modelPickerSepStyle.Render(strings.Repeat("─", boxWidth-4)))
	b.WriteString("\n\n")

	// Match list.
	if len(m.matches) == 0 {
		b.WriteString("  ")
		b.WriteString(modelPickerDimStyle.Render("(no matches)"))
		b.WriteString("\n")
	} else {
		shown := len(m.matches)
		if shown > modelPickerMaxResults {
			shown = modelPickerMaxResults
		}
		for i := 0; i < shown; i++ {
			b.WriteString(m.renderModelLine(m.matches[i], i == m.selected))
			b.WriteString("\n")
		}
	}

	// Footer with current model and key hints.
	b.WriteString("\n")
	if m.currentModel != "" {
		b.WriteString(modelPickerCurrentLabelStyle.Render("  CURRENT: "))
		b.WriteString(modelPickerCurrentValueStyle.Render(m.currentModel))
		b.WriteString("\n")
	}
	b.WriteString(modelPickerFooterStyle.Render(
		"  ↑↓ navigate  Enter select  Esc cancel"))

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
	b.WriteString(qualityStyleFor(mi.Quality).Render(mi.Quality))
	b.WriteString(" ")
	b.WriteString(modelPickerSpeedStyle.Render(speedIconFor(mi.Speed)))

	if mi.ID == m.currentModel {
		b.WriteString(" ")
		b.WriteString(modelPickerCurrentBadgeStyle.Render("(active)"))
	}

	// Line 2: indented details
	b.WriteString("\n    ")
	b.WriteString(modelPickerDetailLabelStyle.Render(formatTokenContext(mi.MaxContext) + " ctx"))
	b.WriteString("  ")
	b.WriteString(modelPickerDetailValueStyle.Render(
		fmt.Sprintf("$%.2f/$%.2f per 1M", mi.InputCost, mi.OutputCost)))
	b.WriteString("    ")
	b.WriteString(modelPickerDetailLabelStyle.Render("Tools: "))
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
