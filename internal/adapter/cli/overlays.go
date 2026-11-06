package cli

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// openConfigForm builds and installs the /config interactive overlay.
// The form is wired to the live config snapshot exposed via cmdDeps; if
// no config provider is available the function emits an error message
// in the chat instead of opening an empty form.
func (m *model) openConfigForm() {
	if m.cmdDeps.configSnapshot == nil {
		m.messages = append(m.messages, chatEntry{
			role:      "error",
			content:   "config nao disponivel: configure o adaptador CLI com SetConfig(...) antes de usar /config",
			timestamp: time.Now(),
		})
		return
	}
	cfg := m.cmdDeps.configSnapshot()
	if cfg == nil {
		m.messages = append(m.messages, chatEntry{
			role:      "error",
			content:   "config nao carregada",
			timestamp: time.Now(),
		})
		return
	}
	path := ""
	if m.cmdDeps.configPath != nil {
		path = m.cmdDeps.configPath()
	}
	var agents []string
	if m.cmdDeps.listAgents != nil {
		agents = m.cmdDeps.listAgents()
	}
	m.configForm = newConfigForm(cfg, path, agents)
	m.configForm.SetSize(m.width, m.height)
}

// handleConfigFormKey delegates a key event to the active config form,
// then reacts to the configFormResult (close/save/reload/notify).
func (m model) handleConfigFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.configForm == nil {
		return m, nil
	}
	form, _, res := m.configForm.Update(msg)
	m.configForm = form

	if res.Notify != "" {
		m.notification = res.Notify
		if res.Saved {
			m.notifyLevel = "success"
		} else {
			m.notifyLevel = "info"
		}
	}
	if res.Reload {
		// After a reload from disk we don't need to do anything else;
		// the form has rebuilt its field list internally.
	}
	if res.Close {
		m.configForm = nil
		m.updateViewport()
	}
	return m, nil
}

// handleHistorySearchKey delegates a key event to the active history
// search overlay. When the user picks an entry, the chosen text is
// placed back into the textarea and the overlay is dismissed.
func (m model) handleHistorySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.historySearch == nil {
		return m, nil
	}
	hs, _, picked := m.historySearch.Update(msg)
	m.historySearch = hs

	switch picked {
	case "":
		// Continue searching.
		return m, nil
	case "\x00cancel":
		// User cancelled.
		m.historySearch = nil
		m.updateViewport()
		return m, nil
	default:
		// User confirmed a selection.
		m.historySearch = nil
		m.textarea.SetValue(picked)
		m.textarea.CursorEnd()
		m.updateViewport()
		return m, nil
	}
}
