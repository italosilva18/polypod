package cli

import (
	"testing"

	"github.com/charmbracelet/glamour"
)

func TestRenderMarkdownMatchesFreshRenderer(t *testing.T) {
	contents := []string{
		"# Título\n\nUm parágrafo com **ênfase** e [link](https://example.com).",
		"- primeiro item\n- segundo item\n\n```go\nfmt.Println(\"olá\")\n```",
	}
	for _, width := range []int{0, 32, 80, 120, 32} {
		for _, content := range contents {
			effectiveWidth := width
			if effectiveWidth <= 0 {
				effectiveWidth = 80
			}
			fresh, err := glamour.NewTermRenderer(
				glamour.WithStylePath("dark"),
				glamour.WithWordWrap(effectiveWidth),
			)
			if err != nil {
				t.Fatal(err)
			}
			want, err := fresh.Render(content)
			if err != nil {
				t.Fatal(err)
			}
			if got := RenderMarkdown(content, width); got != want {
				t.Errorf("width %d: rendered output differs from a fresh renderer\ngot:  %q\nwant: %q", width, got, want)
			}
		}
	}
}

var benchmarkMarkdownOutput string

func BenchmarkRenderMarkdown(b *testing.B) {
	const content = "# Answer\n\nA paragraph with **bold text** and a [link](https://example.com).\n\n- first item\n- second item"
	const width = 80
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkMarkdownOutput = RenderMarkdown(content, width)
		}
	})
	b.Run("fresh", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r, err := glamour.NewTermRenderer(
				glamour.WithStylePath("dark"),
				glamour.WithWordWrap(width),
			)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkMarkdownOutput, err = r.Render(content)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
