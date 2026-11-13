package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type connectResultMsg struct {
	provider string
	err      error
}

func (m *model) openConnectPicker() {
	if m.cmdDeps.listProviders == nil || m.cmdDeps.connectProvider == nil {
		m.notification = "conexão de provedores indisponível"
		m.notifyLevel = "error"
		return
	}
	m.modelPicker = newModelPicker("", m.cmdDeps.listProviders()...)
	m.modelPicker.connectMode = true
	m.modelPicker.SetSize(m.width, m.height)
}

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
func (m *model) openModelPicker() tea.Cmd {
	if m.cmdDeps.setModel == nil {
		m.messages = append(m.messages, chatEntry{
			role:      "error",
			content:   "seletor de modelos indisponível: o serviço de IA não permite trocar o modelo",
			timestamp: time.Now(),
		})
		return nil
	}
	current := ""
	if m.cmdDeps.currentModel != nil {
		current = m.cmdDeps.currentModel()
	}
	if m.cmdDeps.listProviders != nil && m.cmdDeps.switchProvider != nil {
		m.modelPicker = newModelPicker(current, m.cmdDeps.listProviders()...)
		if m.cmdDeps.currentProvider != nil {
			m.modelPicker.currentProvider = m.cmdDeps.currentProvider()
		}
	} else {
		m.modelPicker = newModelPicker(current)
	}
	m.modelPicker.SetSize(m.width, m.height)
	if len(m.modelPicker.providers) > 0 {
		if time.Since(m.catalogCheckedAt) < time.Hour && len(m.catalogModels) > 0 {
			m.modelPicker.mergeCatalog(m.catalogModels)
			return nil
		}
		if time.Since(m.catalogCheckedAt) < 5*time.Minute {
			m.modelPicker.catalogOffline = true
			return nil
		}
		m.modelPicker.catalogLoading = true
		return loadModelCatalog(m.modelPicker)
	}
	return nil
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
		if picked == "\x1eollama" {
			m.notification = "Ollama não precisa de chave. Inicie o servidor local e use /model."
			m.notifyLevel = "info"
			m.updateViewport()
			return m, nil
		}
		if picked == "\x1eoauth" || strings.HasPrefix(picked, "\x1eoauth\x1f") {
			prov := "chatgpt"
			if strings.HasPrefix(picked, "\x1eoauth\x1f") {
				prov = strings.TrimPrefix(picked, "\x1eoauth\x1f")
			}
			if m.cmdDeps.connectOAuth != nil {
				login := m.cmdDeps.connectOAuth
				m.notification = fmt.Sprintf("Autorize a Poly na janela do navegador para conectar %s via OAuth.", prov)
				m.notifyLevel = "info"
				m.updateViewport()
				return m, func() tea.Msg { return connectResultMsg{provider: prov, err: login(context.Background(), prov)} }
			}
			if prov == "chatgpt" && m.cmdDeps.connectChatGPT != nil {
				login := m.cmdDeps.connectChatGPT
				m.notification = "Autorize a Poly na janela do navegador para conectar o ChatGPT."
				m.notifyLevel = "info"
				m.updateViewport()
				return m, func() tea.Msg { return connectResultMsg{provider: "chatgpt", err: login(context.Background())} }
			}
			m.notification = "login OAuth indisponível para " + prov
			m.notifyLevel = "error"
			return m, nil
		}
		if strings.HasPrefix(picked, "\x1econnect\x1f") {
			parts := strings.SplitN(strings.TrimPrefix(picked, "\x1econnect\x1f"), "\x1f", 2)
			if len(parts) != 2 || m.cmdDeps.connectProvider == nil {
				m.notification = "dados de conexão inválidos"
				m.notifyLevel = "error"
			} else if err := m.cmdDeps.connectProvider(parts[0], parts[1]); err != nil {
				m.notification = "erro ao conectar provedor: " + err.Error()
				m.notifyLevel = "error"
			} else {
				m.notification = "Provedor " + parts[0] + " conectado. Use /model para escolher um modelo."
				m.notifyLevel = "success"
			}
			m.updateViewport()
			return m, nil
		}
		if parts := strings.SplitN(picked, "\x1f", 2); len(parts) == 2 && m.cmdDeps.switchProvider != nil {
			if err := m.cmdDeps.switchProvider(parts[0], parts[1]); err != nil {
				m.notification = fmt.Sprintf("erro ao trocar provedor: %v", err)
				m.notifyLevel = "error"
				m.updateViewport()
				return m, clearNotifyAfter(5*time.Second, m.notification)
			}
			m.notification = fmt.Sprintf("provedor: %s · modelo: %s", parts[0], parts[1])
		} else {
			if m.cmdDeps.setModel != nil {
				m.cmdDeps.setModel(picked)
			}
			m.notification = fmt.Sprintf("modelo trocado: %s", picked)
		}
		// Refresh the rich status bar so the new model name shows up
		// immediately on the next render pass.
		m.refreshStatusBar()
		m.notifyLevel = "success"
		m.updateViewport()
		return m, clearNotifyAfter(3*time.Second, m.notification)
	}
}
