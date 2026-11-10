package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// KeyBinding describes a keyboard shortcut and its behavior.
type KeyBinding struct {
	Key         tea.KeyType
	Label       string // human-readable key name
	Description string
	Handler     func(m *model) (model, tea.Cmd)
}

// DefaultKeyBindings returns the complete set of keyboard shortcuts.
// Each handler receives the current model and returns the updated model
// and an optional tea.Cmd for side effects.
func DefaultKeyBindings() []KeyBinding {
	return []KeyBinding{
		{
			Key:         tea.KeyCtrlC,
			Label:       "Ctrl+C",
			Description: "cancelar geracao / sair",
			Handler:     handleKeyCtrlC,
		},
		{
			Key:         tea.KeyCtrlB,
			Label:       "Ctrl+B",
			Description: "minimizar/expandir saída do turno ativo",
			Handler:     handleKeyCtrlB,
		},
		{
			Key:         tea.KeyCtrlG,
			Label:       "Ctrl+G",
			Description: "iniciar/parar gravação de voz",
			Handler:     handleKeyCtrlG,
		},
		{
			Key:         tea.KeyCtrlD,
			Label:       "Ctrl+D",
			Description: "sair do CLI",
			Handler:     handleKeyCtrlD,
		},
		{
			Key:         tea.KeyCtrlL,
			Label:       "Ctrl+L",
			Description: "limpar tela",
			Handler:     handleKeyCtrlL,
		},
		{
			Key:         tea.KeyCtrlR,
			Label:       "Ctrl+R",
			Description: "buscar no historico",
			Handler:     handleKeyCtrlR,
		},
		{
			Key:         tea.KeyCtrlP,
			Label:       "Ctrl+P",
			Description: "abrir picker de arquivos (quick open)",
			Handler:     handleKeyCtrlP,
		},
		{
			Key:         tea.KeyCtrlY,
			Label:       "Ctrl+Y",
			Description: "abrir picker de modelos",
			Handler:     handleKeyCtrlY,
		},
		{
			Key:         tea.KeyCtrlZ,
			Label:       "Ctrl+Z",
			Description: "desfazer ultima edicao",
			Handler:     handleKeyCtrlZ,
		},
		{
			Key:         tea.KeyTab,
			Label:       "Tab",
			Description: "completar comandos e caminhos",
			Handler:     handleKeyTab,
		},
		{
			Key:         tea.KeyUp,
			Label:       "Up",
			Description: "comando anterior no historico",
			Handler:     handleKeyUp,
		},
		{
			Key:         tea.KeyDown,
			Label:       "Down",
			Description: "proximo comando no historico",
			Handler:     handleKeyDown,
		},
		{
			Key:         tea.KeyEsc,
			Label:       "Esc",
			Description: "cancelar input / limpar",
			Handler:     handleKeyEsc,
		},
		{
			Key:         tea.KeyPgUp,
			Label:       "PgUp",
			Description: "rolar viewport para cima",
			Handler:     handleKeyPgUp,
		},
		{
			Key:         tea.KeyPgDown,
			Label:       "PgDown",
			Description: "rolar viewport para baixo",
			Handler:     handleKeyPgDown,
		},
	}
}

func handleKeyCtrlB(m *model) (model, tea.Cmd) {
	if m.state == stateStreaming && !m.waitingForUserAnswer {
		m.streamHidden = !m.streamHidden
		m.updateViewport()
	}
	return *m, nil
}

func handleKeyCtrlG(m *model) (model, tea.Cmd) {
	if m.voiceRecording {
		return m.stopVoiceCapture()
	}
	return m.beginVoiceCapture(0, true)
}

// keybindingMap builds a lookup table from key type to handler for O(1) dispatch.
func keybindingMap() map[tea.KeyType]func(m *model) (model, tea.Cmd) {
	bindings := DefaultKeyBindings()
	km := make(map[tea.KeyType]func(m *model) (model, tea.Cmd), len(bindings))
	for _, kb := range bindings {
		km[kb.Key] = kb.Handler
	}
	return km
}

// DispatchKey looks up and executes the handler for the given key type.
// Returns the updated model, command, and whether the key was handled.
func DispatchKey(m *model, keyType tea.KeyType) (model, tea.Cmd, bool) {
	bindings := keybindingMap()
	handler, ok := bindings[keyType]
	if !ok {
		return *m, nil, false
	}
	updated, cmd := handler(m)
	return updated, cmd, true
}

// FormatKeybindingsHelp renders a formatted help string showing all
// available keyboard shortcuts, suitable for display in the viewport.
func FormatKeybindingsHelp() string {
	bindings := DefaultKeyBindings()

	var b strings.Builder
	b.WriteString(cmdTitleStyle.Render("Atalhos de teclado"))
	b.WriteString("\n\n")

	maxLabel := 0
	for _, kb := range bindings {
		if len(kb.Label) > maxLabel {
			maxLabel = len(kb.Label)
		}
	}

	for _, kb := range bindings {
		padding := strings.Repeat(" ", maxLabel-len(kb.Label)+2)
		b.WriteString("  ")
		b.WriteString(welcomeKeyStyle.Render(kb.Label))
		b.WriteString(padding)
		b.WriteString(cmdLabelStyle.Render(kb.Description))
		b.WriteString("\n")
	}

	return b.String()
}

// ── Key handlers ────────────────────────────────────────────────────────────

func handleKeyCtrlC(m *model) (model, tea.Cmd) {
	if m.state == stateStreaming {
		// Cancel the current generation
		m.state = stateIdle
		content := m.streaming.String()
		if content != "" {
			m.messages = append(m.messages, chatEntry{
				role:    "assistant",
				content: content + "\n\n*(cancelado)*",
			})
		}
		m.streaming.Reset()
		m.streamCh = nil
		m.updateViewport()
		return *m, nil
	}
	return *m, tea.Quit
}

func handleKeyCtrlD(m *model) (model, tea.Cmd) {
	return *m, tea.Quit
}

func handleKeyCtrlL(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.messages = nil
	m.updateViewport()
	return *m, nil
}

func handleKeyCtrlR(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	// Show a hint that history search is triggered via the input prefix
	// and populate input with a search indicator
	current := m.textarea.Value()
	if !strings.HasPrefix(current, "/") {
		// Display history entries as a system message for browsing
		if m.history != nil && len(m.history.entries) > 0 {
			var b strings.Builder
			b.WriteString(cmdTitleStyle.Render("Historico"))
			b.WriteString(cmdLabelStyle.Render(fmt.Sprintf(" (%d entradas)", len(m.history.entries))))
			b.WriteString("\n\n")

			// Show last 20 entries in reverse order
			start := len(m.history.entries) - 20
			if start < 0 {
				start = 0
			}
			for i := len(m.history.entries) - 1; i >= start; i-- {
				entry := m.history.entries[i]
				idx := fmt.Sprintf("%3d", i+1)
				b.WriteString("  ")
				b.WriteString(subtleStyle.Render(idx))
				b.WriteString("  ")
				b.WriteString(cmdValueStyle.Render(truncate(entry, 70)))
				b.WriteString("\n")
			}

			b.WriteString("\n")
			b.WriteString(cmdLabelStyle.Render("  Use Up/Down para navegar no historico"))

			m.messages = append(m.messages, chatEntry{
				role:    "system",
				content: b.String(),
			})
			m.updateViewport()
		}
	}
	return *m, nil
}

func handleKeyCtrlZ(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	// Undo: remove the last assistant+user message pair if available
	if len(m.messages) >= 2 {
		lastIdx := len(m.messages) - 1
		if m.messages[lastIdx].role == "assistant" && lastIdx > 0 && m.messages[lastIdx-1].role == "user" {
			m.messages = m.messages[:lastIdx-1]
			m.updateViewport()
			return *m, nil
		}
	}
	// If just one message, try to remove it
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.role == "assistant" || last.role == "system" || last.role == "error" {
			m.messages = m.messages[:len(m.messages)-1]
			m.updateViewport()
		}
	}
	return *m, nil
}

func handleKeyTab(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.tryTabComplete()
	return *m, nil
}

func handleKeyUp(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.resetTab()
	if prev, ok := m.history.Previous(); ok {
		m.textarea.SetValue(prev)
		m.textarea.CursorEnd()
	}
	return *m, nil
}

func handleKeyDown(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.resetTab()
	if next, ok := m.history.Next(); ok {
		m.textarea.SetValue(next)
		m.textarea.CursorEnd()
	}
	return *m, nil
}

func handleKeyEsc(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.textarea.SetValue("")
	m.textarea.Reset()
	m.resetTab()
	return *m, nil
}

func handleKeyPgUp(m *model) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	return *m, cmd
}

func handleKeyPgDown(m *model) (model, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	return *m, cmd
}

// handleKeyCtrlP opens the Quick Open file picker overlay. Only valid
// when the model is idle (no streaming, no overlay already active).
func handleKeyCtrlP(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.openQuickOpen()
	return *m, nil
}

// handleKeyCtrlY opens the model picker overlay. Only valid when the
// model is idle.
func handleKeyCtrlY(m *model) (model, tea.Cmd) {
	if m.state != stateIdle {
		return *m, nil
	}
	m.openModelPicker()
	return *m, nil
}
