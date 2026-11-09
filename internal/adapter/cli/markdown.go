package cli

import (
	"sync"

	"github.com/charmbracelet/glamour"
)

const (
	markdownCacheMaxEntries     = 128
	markdownCacheMaxInputBytes  = 16 << 10
	markdownCacheMaxOutputBytes = 64 << 10
)

// A single renderer is enough for the TUI's current width. Its parser is reused
// across viewport updates. Completed messages are rendered again whenever the
// viewport changes, so cache their immutable output for the current width.
// The lock also keeps concurrent callers safe.
var markdownRenderer struct {
	sync.Mutex
	width     int
	renderer  *glamour.TermRenderer
	cache     map[string]string
	cacheKeys [markdownCacheMaxEntries]string
	cacheNext int
	cacheSize int
}

// RenderMarkdown renders markdown content for terminal display.
func RenderMarkdown(content string, width int) string {
	if width <= 0 {
		width = 80
	}

	markdownRenderer.Lock()
	defer markdownRenderer.Unlock()
	if markdownRenderer.renderer == nil || markdownRenderer.width != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithStylePath("dark"),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return content
		}
		markdownRenderer.renderer = r
		markdownRenderer.width = width
		markdownRenderer.cache = nil
		markdownRenderer.cacheKeys = [markdownCacheMaxEntries]string{}
		markdownRenderer.cacheNext = 0
		markdownRenderer.cacheSize = 0
	}
	if cached, ok := markdownRenderer.cache[content]; ok {
		return cached
	}
	out, err := markdownRenderer.renderer.Render(content)
	if err != nil {
		return content
	}
	if len(content) <= markdownCacheMaxInputBytes && len(out) <= markdownCacheMaxOutputBytes {
		if markdownRenderer.cache == nil {
			markdownRenderer.cache = make(map[string]string, markdownCacheMaxEntries)
		}
		if markdownRenderer.cacheSize == markdownCacheMaxEntries {
			delete(markdownRenderer.cache, markdownRenderer.cacheKeys[markdownRenderer.cacheNext])
		} else {
			markdownRenderer.cacheSize++
		}
		markdownRenderer.cacheKeys[markdownRenderer.cacheNext] = content
		markdownRenderer.cacheNext = (markdownRenderer.cacheNext + 1) % markdownCacheMaxEntries
		markdownRenderer.cache[content] = out
	}
	return out
}
