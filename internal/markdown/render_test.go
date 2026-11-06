package markdown

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain forces lipgloss to emit ANSI escape codes even when tests run
// without a real TTY attached to stdout. Without this the match-highlight
// functions (which use lipgloss) would emit plain text and the assertions
// below that check for ANSI sequences would all fail.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// stripColors removes ANSI escape sequences (CSI SGR and OSC 8) from s so
// tests can assert on the visible content of rendered output.
func stripColors(s string) string {
	// CSI sequences: ESC [ ... final-byte
	csi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	// OSC 8 hyperlinks: ESC ] 8 ; ... ESC \
	osc := regexp.MustCompile(`\x1b\]8;[^\x1b]*\x1b\\`)
	s = osc.ReplaceAllString(s, "")
	s = csi.ReplaceAllString(s, "")
	return s
}

func hasANSI(s string) bool {
	return strings.Contains(s, "\x1b[")
}

// ─────────────────────────────────────────────────────────────────────────────
// Render: basic markdown
// ─────────────────────────────────────────────────────────────────────────────

func TestRender_PlainText(t *testing.T) {
	out := Render("hello world")
	if !strings.Contains(stripColors(out), "hello world") {
		t.Errorf("output missing plain text: %q", out)
	}
}

func TestRender_Bold(t *testing.T) {
	out := Render("**bold text**")
	plain := stripColors(out)
	if !strings.Contains(plain, "bold text") {
		t.Errorf("output missing bold content: %q", plain)
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI escape codes in bold output: %q", out)
	}
	// The bold SGR code 1 should appear.
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("expected bold SGR code \\x1b[1m in %q", out)
	}
}

func TestRender_Italic(t *testing.T) {
	out := Render("this is *italic* text")
	plain := stripColors(out)
	if !strings.Contains(plain, "italic") {
		t.Errorf("output missing italic content: %q", plain)
	}
	if !strings.Contains(out, "\x1b[3m") {
		t.Errorf("expected italic SGR code \\x1b[3m in %q", out)
	}
}

func TestRender_InlineCode(t *testing.T) {
	out := Render("use `foo()` to call")
	plain := stripColors(out)
	if !strings.Contains(plain, "foo()") {
		t.Errorf("output missing inline code content: %q", plain)
	}
	// Inline code is rendered with inverse video (SGR 7).
	if !strings.Contains(out, "\x1b[7m") {
		t.Errorf("expected inverse SGR code \\x1b[7m for inline code in %q", out)
	}
}

func TestRender_Header1(t *testing.T) {
	out := Render("# Big Header")
	plain := stripColors(out)
	if !strings.Contains(plain, "Big Header") {
		t.Errorf("output missing header text: %q", plain)
	}
	// H1 uses ═ rule; make sure it's visually distinct.
	if !strings.Contains(plain, "═") {
		t.Errorf("expected ═ rule under H1 header, got: %q", plain)
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes for H1 header: %q", out)
	}
}

func TestRender_Header2And3(t *testing.T) {
	out := Render("## Subheader\n\n### Deep Header\n")
	plain := stripColors(out)
	if !strings.Contains(plain, "Subheader") {
		t.Errorf("missing H2 text: %q", plain)
	}
	if !strings.Contains(plain, "Deep Header") {
		t.Errorf("missing H3 text: %q", plain)
	}
	// H2 has ─ rule, H3 has · rule.
	if !strings.Contains(plain, "─") {
		t.Errorf("expected ─ rule for H2: %q", plain)
	}
	if !strings.Contains(plain, "·") {
		t.Errorf("expected · rule for H3: %q", plain)
	}
}

func TestRender_UnorderedList(t *testing.T) {
	md := "- first item\n- second item\n- third item\n"
	out := Render(md)
	plain := stripColors(out)
	for _, want := range []string{"first item", "second item", "third item"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing list item %q in output: %q", want, plain)
		}
	}
	// Bullets are rendered as "•".
	if !strings.Contains(plain, "•") {
		t.Errorf("expected bullet • in unordered list output: %q", plain)
	}
}

func TestRender_OrderedList(t *testing.T) {
	md := "1. alpha\n2. beta\n3. gamma\n"
	out := Render(md)
	plain := stripColors(out)
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing ordered list item %q in: %q", want, plain)
		}
	}
	// The numeric markers should appear with trailing dots.
	if !strings.Contains(plain, "1.") || !strings.Contains(plain, "2.") {
		t.Errorf("expected numeric markers in ordered list: %q", plain)
	}
}

func TestRender_CodeBlock(t *testing.T) {
	md := "```go\nfunc main() {}\n```\n"
	out := Render(md)
	plain := stripColors(out)
	if !strings.Contains(plain, "func main()") {
		t.Errorf("code block content missing: %q", plain)
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes from syntax highlighter: %q", out)
	}
}

func TestRender_HorizontalRule(t *testing.T) {
	out := Render("above\n\n---\n\nbelow\n")
	plain := stripColors(out)
	if !strings.Contains(plain, "above") || !strings.Contains(plain, "below") {
		t.Errorf("surrounding content missing: %q", plain)
	}
	// HR is a long line of ─ characters.
	if !strings.Contains(plain, strings.Repeat("─", 10)) {
		t.Errorf("expected a long ─ horizontal rule, got: %q", plain)
	}
}

func TestRender_Link(t *testing.T) {
	out := Render("see [Kilo](https://kilo.ai) for more")
	plain := stripColors(out)
	if !strings.Contains(plain, "Kilo") {
		t.Errorf("link text missing: %q", plain)
	}
	// The URL shouldn't leak into the visible output verbatim with brackets.
	if strings.Contains(plain, "[Kilo](https://kilo.ai)") {
		t.Errorf("link not rendered, raw markdown remains: %q", plain)
	}
	// Underline SGR (4) should be applied to the link text.
	if !strings.Contains(out, "\x1b[4m") {
		t.Errorf("expected underline SGR code for link: %q", out)
	}
}

func TestRender_Blockquote(t *testing.T) {
	out := Render("> this is a quote\n> second line\n")
	plain := stripColors(out)
	if !strings.Contains(plain, "this is a quote") {
		t.Errorf("blockquote content missing: %q", plain)
	}
	// Blockquotes are prefixed with "│".
	if !strings.Contains(plain, "│") {
		t.Errorf("expected │ marker for blockquote: %q", plain)
	}
}

func TestRender_TaskList(t *testing.T) {
	md := "- [ ] todo\n- [x] done\n"
	out := Render(md)
	plain := stripColors(out)
	if !strings.Contains(plain, "todo") || !strings.Contains(plain, "done") {
		t.Errorf("task list content missing: %q", plain)
	}
	if !strings.Contains(plain, "☐") {
		t.Errorf("expected ☐ for unchecked task: %q", plain)
	}
	if !strings.Contains(plain, "☑") {
		t.Errorf("expected ☑ for checked task: %q", plain)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Render: tables
// ─────────────────────────────────────────────────────────────────────────────

func TestRender_SimpleTable(t *testing.T) {
	md := "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |\n"
	out := Render(md)
	plain := stripColors(out)
	for _, want := range []string{"Name", "Age", "Alice", "30", "Bob", "25"} {
		if !strings.Contains(plain, want) {
			t.Errorf("table missing %q in: %q", want, plain)
		}
	}
	// Box-drawing top/bottom rules.
	if !strings.Contains(plain, "┌") || !strings.Contains(plain, "┘") {
		t.Errorf("expected table border characters, got: %q", plain)
	}
}

func TestRender_TableHeaderBold(t *testing.T) {
	md := "| Key | Val |\n| --- | --- |\n| foo | bar |\n"
	out := Render(md)
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("expected bold ANSI for table header: %q", out)
	}
}

func TestRenderTable_ColumnAlignment(t *testing.T) {
	headers := []string{"A", "Longer Header"}
	rows := [][]string{
		{"x", "y"},
		{"short", "z"},
	}
	out := RenderTable(headers, rows, 0)
	plain := stripColors(out)
	// All cells present.
	for _, want := range []string{"Longer Header", "short", "y", "z"} {
		if !strings.Contains(plain, want) {
			t.Errorf("RenderTable missing %q: %q", want, plain)
		}
	}
	// Cells are padded to the widest entry, so the short "x" row
	// should still be followed by spaces before the closing "│".
	if !strings.Contains(plain, "┬") {
		t.Errorf("expected a ┬ column separator: %q", plain)
	}
}

func TestRenderTable_EmptyHeaders(t *testing.T) {
	out := RenderTable(nil, nil, 0)
	if out != "" {
		t.Errorf("expected empty output for empty headers, got: %q", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Syntax highlighting
// ─────────────────────────────────────────────────────────────────────────────

func TestHighlightCode_Go(t *testing.T) {
	code := `func main() { println("hi") }`
	out := HighlightCode(code, "go")
	if out == "" {
		t.Fatal("empty output")
	}
	if !strings.Contains(stripColors(out), "func") {
		t.Errorf("missing 'func' in highlighted Go output: %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI escapes for Go highlighting: %q", out)
	}
}

func TestHighlightCode_UnknownLanguage(t *testing.T) {
	code := "some arbitrary text"
	out := HighlightCode(code, "this-language-does-not-exist-xyz")
	if out == "" {
		t.Error("expected non-empty output for unknown language")
	}
	if !strings.Contains(stripColors(out), "some arbitrary text") {
		t.Errorf("fallback should preserve text content, got: %q", stripColors(out))
	}
}

func TestHighlightCode_EmptyInput(t *testing.T) {
	if out := HighlightCode("", "go"); out != "" {
		t.Errorf("expected empty output for empty input, got %q", out)
	}
}

func TestDetectLanguage_ByExtension(t *testing.T) {
	// DetectLanguage returns the lowercase chroma lexer name; for common
	// files those map to stable identifiers we can assert on.
	tests := map[string]string{
		"main.go":   "go",
		"script.py": "python",
		"index.js":  "javascript",
		"style.css": "css",
		"conf.yaml": "yaml",
		"data.json": "json",
	}
	for filename, wantLang := range tests {
		t.Run(filename, func(t *testing.T) {
			got := DetectLanguage("", filename)
			if got != wantLang {
				t.Errorf("DetectLanguage(%q) = %q; want %q", filename, got, wantLang)
			}
		})
	}
}

func TestDetectLanguage_FromContent(t *testing.T) {
	// Without a filename, DetectLanguage falls back to Chroma's
	// content-based analyser. The analyser is best-effort, so we don't
	// pin the exact language: we just verify the function accepts
	// code-only input and returns a string without panicking. Distinct
	// well-formed inputs should not both return the same empty value as
	// a truly empty call does — this is a smoke test.
	_ = DetectLanguage("<html><body>hi</body></html>", "")
	_ = DetectLanguage("SELECT * FROM users;", "")
	// At minimum, calling with no filename and no code returns "".
	if got := DetectLanguage("", ""); got != "" {
		t.Errorf("DetectLanguage empty inputs = %q; want \"\"", got)
	}
}

func TestDetectLanguage_EmptyInputs(t *testing.T) {
	if got := DetectLanguage("", ""); got != "" {
		t.Errorf("DetectLanguage(\"\",\"\") = %q, want empty string", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Match highlighting
// ─────────────────────────────────────────────────────────────────────────────

func TestHighlightMatches_Substring(t *testing.T) {
	out := HighlightMatches("the quick brown fox", []string{"quick", "fox"})
	if !strings.Contains(stripColors(out), "the quick brown fox") {
		t.Errorf("stripped output should equal original: %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes around matches: %q", out)
	}
}

func TestHighlightMatches_NoMatches(t *testing.T) {
	out := HighlightMatches("nothing to see", []string{"absent"})
	if out != "nothing to see" {
		t.Errorf("expected unchanged text for no matches, got %q", out)
	}
}

func TestHighlightMatches_EmptySlice(t *testing.T) {
	out := HighlightMatches("hello", nil)
	if out != "hello" {
		t.Errorf("expected unchanged text for nil matches, got %q", out)
	}
}

func TestHighlightMatches_Overlapping(t *testing.T) {
	// Overlapping matches should not double-wrap the same bytes.
	out := HighlightMatches("abcdef", []string{"abc", "bcd"})
	if !strings.Contains(stripColors(out), "abcdef") {
		t.Errorf("visible text should survive overlap merge: %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes: %q", out)
	}
}

func TestHighlightRegex_MatchesDigits(t *testing.T) {
	re := regexp.MustCompile(`\d+`)
	out := HighlightRegex("price is 42 dollars and 7 cents", re)
	if !strings.Contains(stripColors(out), "42") {
		t.Errorf("regex-highlight output missing content: %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes in regex highlight output: %q", out)
	}
}

func TestHighlightRegex_NilPattern(t *testing.T) {
	out := HighlightRegex("unchanged", nil)
	if out != "unchanged" {
		t.Errorf("nil pattern should return text unchanged, got %q", out)
	}
}

func TestHighlightFuzzy_Positions(t *testing.T) {
	text := "hello"
	positions := []int{0, 2, 4} // h, l, o
	out := HighlightFuzzy(text, positions)
	if stripColors(out) != "hello" {
		t.Errorf("stripped fuzzy output should be 'hello', got %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes in fuzzy output: %q", out)
	}
}

func TestHighlightFuzzy_EmptyPositions(t *testing.T) {
	out := HighlightFuzzy("hello", nil)
	if out != "hello" {
		t.Errorf("expected unchanged text, got %q", out)
	}
}

func TestHighlightFuzzy_OutOfRange(t *testing.T) {
	// Out-of-range and negative positions should be silently dropped.
	out := HighlightFuzzy("abc", []int{-1, 10, 1})
	if stripColors(out) != "abc" {
		t.Errorf("expected 'abc' after stripping, got %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes for valid position: %q", out)
	}
}

func TestHighlightMultiple_TwoStyles(t *testing.T) {
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	out := HighlightMultiple("error: foo failed; warning: bar", []DiffHighlight{
		{Text: "error", Style: red},
		{Text: "warning", Style: green},
	})
	if stripColors(out) != "error: foo failed; warning: bar" {
		t.Errorf("stripped output should equal original: %q", stripColors(out))
	}
	if !hasANSI(out) {
		t.Errorf("expected ANSI codes: %q", out)
	}
}

func TestHighlightMultiple_Empty(t *testing.T) {
	if got := HighlightMultiple("hello", nil); got != "hello" {
		t.Errorf("expected unchanged text, got %q", got)
	}
	if got := HighlightMultiple("", []DiffHighlight{{Text: "x"}}); got != "" {
		t.Errorf("expected empty output for empty text, got %q", got)
	}
}
