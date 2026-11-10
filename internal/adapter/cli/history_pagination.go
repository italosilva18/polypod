package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/costa/polypod/internal/session"
)

// Older transcript pages are loaded only while the user reads upward.
// Returning to the bottom drops that cache. The anchor page from /resume,
// unsaved turns, notices, and ask_user text stay in model.messages, so this
// is not virtual scrolling: a long live session still grows resident memory,
// and an expanded scrollback keeps every page that was loaded for reading
// and terminal copy.

func (m *model) resetHistoryPaging() {
	m.historyStart = 0
	m.historyExpanded = false
	m.historySessionID = ""
	m.historyBefore = 0
	m.historyRetain = 0
	m.historyLoadFailed = false
}

// adoptHistoryPaging installs the cursor for a replaced transcript.
// before is the persisted index of the first loaded message. An empty
// session ID means the replacement is not a reloadable page (branch,
// rewind, compact, or the live conversation) and must not be evicted.
func (m *model) adoptHistoryPaging(sessionID string, before int) {
	m.historyStart = max(0, len(m.messages)-historyPageSize)
	m.historyExpanded = false
	m.historySessionID = sessionID
	m.historyBefore = before
	m.historyLoadFailed = false
	if sessionID == "" {
		m.historyRetain = 0
		return
	}
	m.historyRetain = before
}

// prependHistoryPage reveals an older slice without moving the current text
// abruptly. Restored sessions fetch older display pages only when requested;
// the conversation store keeps the complete context for the model.
func (m *model) prependHistoryPage() {
	if !m.ready || (m.historyStart <= 0 && (m.historyBefore <= 0 || m.historyLoadFailed)) {
		return
	}
	previousLines := m.viewport.TotalLineCount()
	previousOffset := m.viewport.YOffset
	if m.historyStart > 0 {
		m.historyStart = max(0, m.historyStart-historyPageSize)
	} else if m.cmdDeps.loadOlderHistory != nil && m.historySessionID != "" {
		if err := m.prependPersistedHistory(); err != nil {
			m.messages = append(m.messages, chatEntry{
				role:      "system",
				content:   "Não foi possível carregar o histórico anterior: " + err.Error(),
				timestamp: time.Now(),
			})
			// Keep historyBefore. Zeroing it would make the next reload and the
			// older-message banner describe a different transcript position.
			// A changed manifest cannot be retried against this display page.
			// Transient I/O failures remain retryable with another PgUp.
			m.historyLoadFailed = errors.Is(err, session.ErrSessionConflict)
		}
	}
	m.historyExpanded = true
	m.viewport.SetContent(m.renderMessages())
	addedLines := max(0, m.viewport.TotalLineCount()-previousLines)
	m.viewport.SetYOffset(previousOffset + addedLines)
	m.viewport.ScrollUp(max(1, m.viewport.Height/2))
}

// prependPersistedHistory loads the page immediately before historyBefore.
// The page is installed only when it is contiguous with the resident
// persisted prefix, so a later eviction can ask for the same range again.
func (m *model) prependPersistedHistory() error {
	if m.cmdDeps.loadOlderHistory == nil || m.historySessionID == "" {
		return fmt.Errorf("paginação de sessão indisponível")
	}
	older, before, err := m.cmdDeps.loadOlderHistory(m.historySessionID, m.historyBefore, historyPageSize)
	if err != nil {
		return err
	}
	if before < 0 || before >= m.historyBefore || before+len(older) != m.historyBefore {
		return fmt.Errorf("o histórico salvo não confere com a página pedida")
	}
	for i := range older {
		older[i].loadedFromSession = true
		older[i].persistedIndex = before + i
	}
	m.messages = append(older, m.messages...)
	m.historyBefore = before
	return nil
}

// evictOldHistory drops persisted pages that were prepended above the anchor.
// It runs when the viewport is back at the bottom. Messages inside the
// rendered tail stay, so terminal selection of the visible bottom text and
// /copy of the latest assistant keep the same resident tail as before PgUp.
func (m *model) evictOldHistory() {
	if m.historySessionID == "" || m.historyExpanded || m.historyRetain <= m.historyBefore {
		return
	}
	drop := m.reloadablePrefix()
	if drop == 0 {
		return
	}
	// Drop the whole older cache or none of it. A partial drop would leave
	// historyBefore in the middle of a page while the rendered tail still
	// shows part of that cache. The anchor page is below this prefix.
	visibleFloor := 0
	if len(m.messages) > historyPageSize {
		visibleFloor = len(m.messages) - historyPageSize
	}
	if drop > visibleFloor {
		return
	}
	kept := make([]chatEntry, len(m.messages)-drop)
	copy(kept, m.messages[drop:])
	m.messages = kept
	m.historyBefore += drop
	// The failed fetch was for a cursor below the anchor. The restored
	// cursor can be read again.
	m.historyLoadFailed = false
	if m.historyStart >= drop {
		m.historyStart -= drop
	} else {
		m.historyStart = 0
	}
}

// reloadablePrefix counts the leading messages loaded from the persisted
// transcript below historyRetain. A hole, a local message, or a gap means
// the prefix is not safe to drop: it could not be reloaded unchanged.
func (m *model) reloadablePrefix() int {
	if m.historySessionID == "" || m.historyRetain <= m.historyBefore {
		return 0
	}
	n := 0
	for n < len(m.messages) {
		entry := &m.messages[n]
		if !entry.loadedFromSession || entry.persistedIndex >= m.historyRetain {
			break
		}
		if entry.persistedIndex != m.historyBefore+n {
			return 0
		}
		n++
	}
	if n == 0 || m.historyBefore+n != m.historyRetain {
		return 0
	}
	return n
}
