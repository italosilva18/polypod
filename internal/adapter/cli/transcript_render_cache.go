package cli

import (
	"fmt"
	"strings"
	"time"
)

// Keep the joined transcript bounded. The viewport and individual entries
// already hold copies of rendered text; a larger third copy would hurt long
// sessions more than the saved concatenation work helps them.
const maxStaticTranscriptCacheBytes = 4 << 20

type transcriptRenderCache struct {
	width       int
	start       int
	older       int
	count       int
	first       *chatEntry
	last        *chatEntry
	lastRole    string
	lastContent string
	lastStamp   time.Time
	lastElapsed time.Duration
	body        string
	valid       bool
}

// renderStaticTranscript joins the immutable message entries once while the
// stream grows. Without this cache every token redraw joins the same loaded
// history, even though only the trailing in-progress response changed.
func (m *model) renderStaticTranscript(width, start, older int) string {
	count := len(m.messages)
	var first, last *chatEntry
	if start < count {
		first = &m.messages[start]
		last = &m.messages[count-1]
	}
	cached := &m.transcriptCache
	if cached.valid && cached.width == width && cached.start == start &&
		cached.older == older && cached.count == count && cached.first == first &&
		cached.last == last && (last == nil ||
		(cached.lastRole == last.role && cached.lastContent == last.content &&
			cached.lastStamp.Equal(last.timestamp) && cached.lastElapsed == last.elapsed)) {
		return cached.body
	}

	var b strings.Builder
	if older > 0 {
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  ↑ %d mensagens anteriores; PgUp para carregar", older)))
		b.WriteString("\n\n")
	}
	separator := "  " + separatorStyle.Render(strings.Repeat("─", width/2)) + "\n\n"
	for i := start; i < count; i++ {
		entry := &m.messages[i]
		if entry.renderedWidth != width {
			entry.renderedBody = renderChatEntry(*entry, width)
			entry.renderedWidth = width
		}
		b.WriteString(entry.renderedBody)
		if i < count-1 && entry.role == "assistant" {
			b.WriteString(separator)
		}
	}
	body := b.String()
	if len(body) > maxStaticTranscriptCacheBytes {
		m.transcriptCache = transcriptRenderCache{}
		return body
	}
	m.transcriptCache = transcriptRenderCache{
		width: width, start: start, older: older, count: count,
		first: first, last: last, body: body, valid: true,
	}
	if last != nil {
		m.transcriptCache.lastRole = last.role
		m.transcriptCache.lastContent = last.content
		m.transcriptCache.lastStamp = last.timestamp
		m.transcriptCache.lastElapsed = last.elapsed
	}
	return body
}
