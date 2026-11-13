package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStatusBarShowsSelectedProviderAtCommonWidths(t *testing.T) {
	bar := NewStatusBar()
	bar.Update(StatusBarState{Visible: true, Mode: "edit", Agent: "default", Provider: "nvidia", Model: "nvidia/test-model"})
	for _, width := range []int{50, 80, 120} {
		plain := ansi.Strip(bar.Render(width))
		if !strings.Contains(plain, "nvidia") {
			t.Errorf("largura %d omitiu o provedor: %q", width, plain)
		}
	}
}
