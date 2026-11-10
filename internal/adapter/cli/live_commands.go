package cli

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// liveCommandResultMsg returns a read-only session listing without blocking
// the Bubbletea update loop or interfering with the active model stream.
type liveCommandResultMsg struct {
	result commandResult
}

// liveCommandDuringStream is intentionally narrow. Other slash commands can
// change the active conversation, model, files, or permissions and keep the
// existing queued submission path until the foreground turn completes.
func liveCommandDuringStream(raw string) (matched bool, async bool) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return false, false
	}
	switch strings.ToLower(parts[0]) {
	case "/help", "/copy", "/cost":
		return true, false
	case "/sessions":
		if len(parts) == 1 {
			return true, true
		}
		if len(parts) != 2 {
			return false, false
		}
		if strings.EqualFold(parts[1], "live") {
			return true, true
		}
		count, err := strconv.Atoi(parts[1])
		return err == nil && count >= 1 && count <= 100, true
	default:
		return false, false
	}
}

func (m *model) submitLiveCommand(raw string) (bool, tea.Cmd) {
	matched, async := liveCommandDuringStream(raw)
	if !matched {
		return false, nil
	}
	m.history.Add(raw)
	m.history.ResetCursor()
	m.textarea.SetValue("")
	m.textarea.Reset()
	m.resetTab()
	if async {
		deps := m.cmdDeps
		return true, func() tea.Msg {
			return liveCommandResultMsg{result: handleSessions(strings.Fields(raw), deps)}
		}
	}
	m.showLiveCommandResult(processCommand(raw, m.cmdDeps, m.messages))
	return true, nil
}

func (m *model) showLiveCommandResult(result commandResult) {
	if result.output != "" {
		m.messages = append(m.messages, chatEntry{
			role: "system", content: result.output, timestamp: time.Now(),
		})
	}
	m.updateViewport()
}
