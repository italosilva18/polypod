package cli

import (
	"github.com/charmbracelet/glamour"
)

// RenderMarkdown renders markdown content for terminal display.
func RenderMarkdown(content string, width int) string {
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	out, err := r.Render(content)
	if err != nil {
		return content
	}
	return out
}
