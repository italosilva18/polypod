package cli

import "time"

// prependHistoryPage reveals an older slice without moving the current text
// abruptly. Restored sessions fetch older display pages only when requested;
// the conversation store keeps the complete context for the model.
func (m *model) prependHistoryPage() {
	if !m.ready || (m.historyStart <= 0 && m.historyBefore <= 0) {
		return
	}
	previousLines := m.viewport.TotalLineCount()
	previousOffset := m.viewport.YOffset
	if m.historyStart > 0 {
		m.historyStart = max(0, m.historyStart-historyPageSize)
	} else if m.cmdDeps.loadOlderHistory != nil && m.historySessionID != "" {
		entries, before, err := m.cmdDeps.loadOlderHistory(m.historySessionID, m.historyBefore, historyPageSize)
		if err != nil {
			m.messages = append(m.messages, chatEntry{
				role: "system", content: "Não foi possível carregar o histórico anterior: " + err.Error(), timestamp: time.Now(),
			})
			m.historyBefore = 0
		} else {
			m.messages = append(entries, m.messages...)
			m.historyBefore = before
		}
	}
	m.historyExpanded = true
	m.viewport.SetContent(m.renderMessages())
	addedLines := max(0, m.viewport.TotalLineCount()-previousLines)
	m.viewport.SetYOffset(previousOffset + addedLines)
	m.viewport.ScrollUp(max(1, m.viewport.Height/2))
}
