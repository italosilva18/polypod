package cli

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// openQuickOpen builds and installs the Ctrl+P quick-open file picker
// overlay rooted at the current working directory. If the cwd cannot
// be resolved we fall back to "." so the picker still loads (no error
// message — it's a transient overlay, not a hard failure).
func (m *model) openQuickOpen() {
	root, err := os.Getwd()
	if err != nil || root == "" {
		root = "."
	}
	m.quickOpen = newQuickOpen(root)
	m.quickOpen.SetSize(m.width, m.height)
}

// openModelPicker builds and installs the model picker overlay,
// pre-populating the "current model" footer from the deps callback.
// When no setModel callback is wired we emit a system message instead
// — there is no point opening a picker that cannot apply its choice.
func (m *model) openModelPicker() {
	if m.cmdDeps.setModel == nil {
		m.messages = append(m.messages, chatEntry{
			role:      "error",
			content:   "model picker indisponivel: o servico AI nao expoe SetModel",
			timestamp: time.Now(),
		})
		return
	}
	current := ""
	if m.cmdDeps.currentModel != nil {
		current = m.cmdDeps.currentModel()
	}
	m.modelPicker = newModelPicker(current)
	m.modelPicker.SetSize(m.width, m.height)
}

// handleQuickOpenKey delegates a key event to the active quick-open
// picker and reacts to its return value: empty = still open, sentinel
// = cancel, anything else = the chosen relative path.
//
// On confirm, the picker inserts "@<path>" into the textarea so the
// usual mention-expansion pipeline can resolve it before the next
// AI turn.
func (m model) handleQuickOpenKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.quickOpen == nil {
		return m, nil
	}
	picker, _, picked := m.quickOpen.Update(msg)
	m.quickOpen = picker

	switch picked {
	case "":
		return m, nil
	case quickOpenCancelSentinel:
		m.quickOpen = nil
		m.updateViewport()
		return m, nil
	default:
		// Confirmed: insert "@<path>" at the textarea cursor. We
		// prepend a leading space when the textarea already has
		// content and does not end in whitespace, so the mention
		// stays a separate token.
		m.quickOpen = nil
		current := m.textarea.Value()
		insert := "@" + picked
		if current != "" {
			last := current[len(current)-1]
			if last != ' ' && last != '\t' && last != '\n' {
				insert = " " + insert
			}
		}
		m.textarea.InsertString(insert)
		m.notification = fmt.Sprintf("inserido: @%s", picked)
		m.notifyLevel = "success"
		m.updateViewport()
		return m, clearNotifyAfter(2*time.Second, m.notification)
	}
}

// handleModelPickerKey delegates a key event to the active model
// picker. On confirm, the chosen model ID is pushed to the AI client
// via deps.setModel and a status notification is shown so the user
// knows the swap took effect.
func (m model) handleModelPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelPicker == nil {
		return m, nil
	}
	picker, _, picked := m.modelPicker.Update(msg)
	m.modelPicker = picker

	switch picked {
	case "":
		return m, nil
	case modelPickerCancelSentinel:
		m.modelPicker = nil
		m.updateViewport()
		return m, nil
	default:
		m.modelPicker = nil
		if m.cmdDeps.setModel != nil {
			m.cmdDeps.setModel(picked)
		}
		// Refresh the rich status bar so the new model name shows up
		// immediately on the next render pass.
		m.refreshStatusBar()
		m.notification = fmt.Sprintf("modelo trocado: %s", picked)
		m.notifyLevel = "success"
		m.updateViewport()
		return m, clearNotifyAfter(3*time.Second, m.notification)
	}
}
