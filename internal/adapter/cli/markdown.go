package cli

import (
	"sync"

	"github.com/charmbracelet/glamour"
)

// A single renderer is enough for the TUI's current width. Its parser is reused
// across viewport updates, and the lock also keeps concurrent callers safe.
var markdownRenderer struct {
	sync.Mutex
	width    int
	renderer *glamour.TermRenderer
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
	}
	out, err := markdownRenderer.renderer.Render(content)
	if err != nil {
		return content
	}
	return out
}
