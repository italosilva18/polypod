package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Message Selector ──────────────────────────────────────────────────────────
//
// MessageSelectorModel is a Bubbletea component that allows the user to scroll
// through conversation messages and select one using arrow keys and Enter.

// SelectorMessage is a display-oriented snapshot of a single conversation
// message, decoupled from any internal message type.
type SelectorMessage struct {
	Role    string
	Preview string // first 80 chars of the message content
	Index   int
}

// MessageSelectorModel holds the state for the message selector overlay.
type MessageSelectorModel struct {
	messages []SelectorMessage
	cursor   int
	visible  bool
	width    int
}

// NewMessageSelector builds a MessageSelectorModel from the given messages.
func NewMessageSelector(messages []SelectorMessage) MessageSelectorModel {
	return MessageSelectorModel{
		messages: messages,
		cursor:   0,
		visible:  true,
		width:    80,
	}
}

// Init satisfies tea.Model. No initial commands are needed.
func (m MessageSelectorModel) Init() tea.Cmd {
	return nil
}

// SelectedMsg is delivered when the user confirms a selection.
type SelectedMsg struct {
	Message SelectorMessage
}

// MessageSelectorClosedMsg is delivered when the user dismisses the selector.
type MessageSelectorClosedMsg struct{}

// Update handles keyboard navigation for the message selector.
func (m MessageSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyDown:
			if m.cursor < len(m.messages)-1 {
				m.cursor++
			}
		case tea.KeyEnter:
			if len(m.messages) > 0 {
				return m, func() tea.Msg {
					return SelectedMsg{Message: m.messages[m.cursor]}
				}
			}
		case tea.KeyEsc:
			m.visible = false
			return m, func() tea.Msg {
				return MessageSelectorClosedMsg{}
			}
		}
	}

	return m, nil
}

// View renders the message selector as a scrollable list overlay.
func (m MessageSelectorModel) View() string {
	if !m.visible {
		return ""
	}

	boxWidth := m.width - 6
	if boxWidth > 100 {
		boxWidth = 100
	}
	if boxWidth < 50 {
		boxWidth = 50
	}

	var b strings.Builder

	// Header.
	b.WriteString(msgSelHeaderStyle.Render("Selecionar mensagem"))
	b.WriteString("\n")
	b.WriteString(msgSelSepStyle.Render(strings.Repeat("─", boxWidth-4)))
	b.WriteString("\n\n")

	if len(m.messages) == 0 {
		b.WriteString("  ")
		b.WriteString(msgSelDimStyle.Render("(sem mensagens)"))
		b.WriteString("\n")
	} else {
		for i, msg := range m.messages {
			selected := i == m.cursor
			b.WriteString(m.renderLine(msg, selected))
			b.WriteString("\n")
		}
	}

	// Footer with key hints.
	b.WriteString("\n")
	b.WriteString(msgSelFooterStyle.Render("  ↑↓ navegar  Enter selecionar  Esc fechar"))

	return msgSelBoxStyle.Width(boxWidth).Render(b.String())
}

// renderLine renders a single message entry.
func (m MessageSelectorModel) renderLine(msg SelectorMessage, selected bool) string {
	var b strings.Builder

	roleStyle := msgSelRoleStyle
	if selected {
		b.WriteString(msgSelSelectedStyle.Render("► "))
		roleStyle = msgSelSelectedStyle
	} else {
		b.WriteString("  ")
	}

	roleLabel := formatRole(msg.Role)
	b.WriteString(roleStyle.Render(fmt.Sprintf("[%s]", roleLabel)))
	b.WriteString(" ")

	preview := msg.Preview
	if len(preview) == 0 {
		preview = "(vazio)"
	}

	if selected {
		b.WriteString(msgSelSelectedStyle.Render(preview))
	} else {
		b.WriteString(msgSelPreviewStyle.Render(preview))
	}

	return b.String()
}

// formatRole returns a human-readable label for the message role.
func formatRole(role string) string {
	switch strings.ToLower(role) {
	case "user":
		return "usuário"
	case "assistant":
		return "assistente"
	case "system":
		return "sistema"
	}
	return role
}

// ── Thinking Toggle ───────────────────────────────────────────────────────────
//
// ThinkingToggleModel is a Bubbletea component that shows or hides an extended
// thinking block. Press 't' to toggle the expanded view.

// ThinkingToggleModel holds the state for the thinking toggle component.
type ThinkingToggleModel struct {
	expanded bool
	content  string
}

// NewThinkingToggle builds a ThinkingToggleModel with the given thinking content.
func NewThinkingToggle(thinkingContent string) ThinkingToggleModel {
	return ThinkingToggleModel{
		expanded: false,
		content:  thinkingContent,
	}
}

// Init satisfies tea.Model. No initial commands are needed.
func (m ThinkingToggleModel) Init() tea.Cmd {
	return nil
}

// Update handles key events for the thinking toggle.
func (m ThinkingToggleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyRunes && string(msg.Runes) == "t" {
			m.expanded = !m.expanded
		}
	}
	return m, nil
}

// View renders the thinking block, collapsed or expanded.
func (m ThinkingToggleModel) View() string {
	if !m.expanded {
		return thinkingCollapsedStyle.Render("▶ pensamento (pressione t para expandir)")
	}
	return thinkingExpandedStyle.Render(m.content)
}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	msgSelBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(1, 2)

	msgSelHeaderStyle = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	msgSelSepStyle = lipgloss.NewStyle().
			Foreground(colorBorderDim)

	msgSelSelectedStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	msgSelRoleStyle = lipgloss.NewStyle().
			Foreground(colorCyan)

	msgSelPreviewStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	msgSelDimStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)

	msgSelFooterStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	thinkingCollapsedStyle = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Italic(true)

	thinkingExpandedStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(colorSubtle).
				PaddingLeft(1)
)
