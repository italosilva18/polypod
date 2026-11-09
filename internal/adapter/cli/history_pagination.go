package cli

// prependHistoryPage reveals an older slice without moving the current text
// abruptly. The complete transcript remains in m.messages for commands and
// the conversation store; pagination only limits what is rendered each frame.
func (m *model) prependHistoryPage() {
	if !m.ready || m.historyStart <= 0 {
		return
	}
	previousLines := m.viewport.TotalLineCount()
	previousOffset := m.viewport.YOffset
	m.historyStart = max(0, m.historyStart-historyPageSize)
	m.historyExpanded = true
	m.viewport.SetContent(m.renderMessages())
	addedLines := max(0, m.viewport.TotalLineCount()-previousLines)
	m.viewport.SetYOffset(previousOffset + addedLines)
	m.viewport.ScrollUp(max(1, m.viewport.Height/2))
}
