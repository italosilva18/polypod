package markdown

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Enhanced terminal renderer for markdown.
//
// This file layers an ANSI-aware renderer on top of the simple parser
// already defined in markdown.go. It supports:
//
//   * Headers with box-drawing rules (H1 ══, H2 ──, H3 ··)
//   * Bold / italic / bold-italic (**x**, *x*, ***x***)
//   * Inline code with inverted background
//   * Hyperlinks via OSC 8 (links)(/links) with underline
//   * Tables with per-column alignment and width
//   * Blockquotes with "│" left bar
//   * Task lists ([ ] / [x])
//   * Nested lists (ordered + unordered)
//   * Horizontal rules as a line of dashes
//   * Fenced code blocks with syntax highlighting via chroma
//
// The renderer is deliberately forgiving: unknown constructs pass through
// verbatim so the output is never worse than the input.

// ANSI SGR codes used by the renderer. Kept explicit to avoid pulling in
// yet another escape-code dependency.
const (
	ansiReset    = "\x1b[0m"
	ansiBold     = "\x1b[1m"
	ansiItalic   = "\x1b[3m"
	ansiUnder    = "\x1b[4m"
	ansiInverse  = "\x1b[7m"
	ansiDim      = "\x1b[2m"
	ansiFgCyan   = "\x1b[36m"
	ansiFgYellow = "\x1b[33m"
	ansiFgGreen  = "\x1b[32m"
	ansiFgBlue   = "\x1b[34m"
	ansiFgGrey   = "\x1b[90m"
)

// DefaultRenderWidth is the column count used by Render when no explicit
// width is supplied. 100 is a reasonable compromise for modern terminals.
const DefaultRenderWidth = 100

// Render converts markdown to terminal-formatted text with ANSI colours.
// This is the main entry point the rest of the codebase should use for
// pretty output.
func Render(md string) string {
	return RenderWithWidth(md, DefaultRenderWidth)
}

// RenderWithWidth renders markdown wrapping soft-wrapped paragraphs at
// the given terminal width. A width <= 0 disables wrapping.
func RenderWithWidth(md string, width int) string {
	if width <= 0 {
		width = 0 // 0 = no wrapping
	}
	r := &renderer{width: width}
	return r.render(md)
}

// ---------------------------------------------------------------------
// Renderer state and block dispatch
// ---------------------------------------------------------------------

type renderer struct {
	width int
	out   strings.Builder
}

func (r *renderer) render(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	lines := strings.Split(md, "\n")

	i := 0
	prevWroteBlock := false
	for i < len(lines) {
		line := lines[i]

		// Blank line separator.
		if strings.TrimSpace(line) == "" {
			i++
			if prevWroteBlock {
				r.out.WriteString("\n")
				prevWroteBlock = false
			}
			continue
		}

		// Fenced code block.
		if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
			fenceLine := strings.TrimLeft(line, " ")
			lang := strings.TrimSpace(strings.TrimPrefix(fenceLine, "```"))
			var body strings.Builder
			i++
			for i < len(lines) && !strings.HasPrefix(strings.TrimLeft(lines[i], " "), "```") {
				if body.Len() > 0 {
					body.WriteByte('\n')
				}
				body.WriteString(lines[i])
				i++
			}
			if i < len(lines) {
				i++ // skip closing fence
			}
			r.writeCodeBlock(lang, body.String())
			prevWroteBlock = true
			continue
		}

		// Heading (setext style: next line is === or ---).
		if i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if next != "" && isAllChar(next, '=') {
				r.writeHeading(strings.TrimSpace(line), 1)
				i += 2
				prevWroteBlock = true
				continue
			}
			if next != "" && isAllChar(next, '-') && !isAllChar(strings.TrimSpace(line), '-') {
				// Avoid eating horizontal rules that sit on their own line.
				if strings.TrimSpace(line) != "" && !isListLine(line) {
					r.writeHeading(strings.TrimSpace(line), 2)
					i += 2
					prevWroteBlock = true
					continue
				}
			}
		}

		// ATX heading.
		if depth, text, ok := parseATXHeading(line); ok {
			r.writeHeading(text, depth)
			i++
			prevWroteBlock = true
			continue
		}

		// Horizontal rule.
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 3 && (isAllChar(trimmed, '-') || isAllChar(trimmed, '*') || isAllChar(trimmed, '_')) {
			r.writeHR()
			i++
			prevWroteBlock = true
			continue
		}

		// Table: current line looks like "| a | b |" and next line is a
		// separator row.
		if isTableHeader(line) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			end := i + 2
			for end < len(lines) && isTableRow(lines[end]) {
				end++
			}
			r.writeTable(lines[i:end])
			i = end
			prevWroteBlock = true
			continue
		}

		// Blockquote.
		if isBlockquoteLine(line) {
			end := i
			for end < len(lines) && isBlockquoteLine(lines[end]) {
				end++
			}
			r.writeBlockquote(lines[i:end])
			i = end
			prevWroteBlock = true
			continue
		}

		// Lists (task, ordered, unordered).
		if isListLine(line) {
			end := i
			for end < len(lines) && (isListLine(lines[end]) || isListContinuation(lines[end])) {
				end++
			}
			r.writeList(lines[i:end])
			i = end
			prevWroteBlock = true
			continue
		}

		// Paragraph: gather consecutive non-blank, non-special lines.
		end := i
		for end < len(lines) {
			l := lines[end]
			if strings.TrimSpace(l) == "" {
				break
			}
			if _, _, ok := parseATXHeading(l); ok {
				break
			}
			if strings.HasPrefix(strings.TrimLeft(l, " "), "```") {
				break
			}
			if isBlockquoteLine(l) || isListLine(l) {
				break
			}
			if isTableHeader(l) && end+1 < len(lines) && isTableSeparator(lines[end+1]) {
				break
			}
			end++
		}
		r.writeParagraph(lines[i:end])
		prevWroteBlock = true
		i = end
	}

	return strings.TrimRight(r.out.String(), "\n") + "\n"
}

// ---------------------------------------------------------------------
// Block writers
// ---------------------------------------------------------------------

func (r *renderer) writeHeading(text string, depth int) {
	inline := renderInline(text)
	visibleLen := visibleWidth(inline)
	switch depth {
	case 1:
		rule := strings.Repeat("═", max(visibleLen, 1))
		r.out.WriteString(ansiBold + ansiFgCyan + inline + ansiReset + "\n")
		r.out.WriteString(ansiFgCyan + rule + ansiReset + "\n")
	case 2:
		rule := strings.Repeat("─", max(visibleLen, 1))
		r.out.WriteString(ansiBold + ansiFgYellow + inline + ansiReset + "\n")
		r.out.WriteString(ansiFgYellow + rule + ansiReset + "\n")
	case 3:
		rule := strings.Repeat("·", max(visibleLen, 1))
		r.out.WriteString(ansiBold + ansiFgGreen + inline + ansiReset + "\n")
		r.out.WriteString(ansiFgGreen + rule + ansiReset + "\n")
	default:
		prefix := strings.Repeat("#", depth) + " "
		r.out.WriteString(ansiBold + prefix + inline + ansiReset + "\n")
	}
}

func (r *renderer) writeHR() {
	width := r.width
	if width <= 0 {
		width = 60
	}
	r.out.WriteString(ansiFgGrey + strings.Repeat("─", width) + ansiReset + "\n")
}

func (r *renderer) writeParagraph(lines []string) {
	joined := strings.Join(lines, " ")
	joined = strings.TrimSpace(joined)
	rendered := renderInline(joined)
	if r.width > 0 {
		rendered = wrapAnsiAware(rendered, r.width)
	}
	r.out.WriteString(rendered)
	r.out.WriteString("\n")
}

func (r *renderer) writeBlockquote(lines []string) {
	for _, l := range lines {
		content := stripBlockquoteMarker(l)
		inline := renderInline(content)
		r.out.WriteString(ansiFgGrey + "│ " + ansiReset + ansiItalic + inline + ansiReset + "\n")
	}
}

func (r *renderer) writeCodeBlock(lang, body string) {
	highlighted := HighlightCode(body, lang)
	// Ensure exactly one trailing newline after the block.
	highlighted = strings.TrimRight(highlighted, "\n")
	for _, line := range strings.Split(highlighted, "\n") {
		r.out.WriteString("    ")
		r.out.WriteString(line)
		r.out.WriteString("\n")
	}
}

// ---------------------------------------------------------------------
// Lists (ordered, unordered, task, nested)
// ---------------------------------------------------------------------

type listItem struct {
	indent  int
	marker  string // "-", "*", "1.", …
	task    int    // 0 = not a task, 1 = unchecked, 2 = checked
	content string
}

var (
	bulletRe  = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	orderedRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	taskRe    = regexp.MustCompile(`^\[( |x|X)\]\s+(.*)$`)
)

func isListLine(line string) bool {
	return bulletRe.MatchString(line) || orderedRe.MatchString(line)
}

func isListContinuation(line string) bool {
	// Indented continuation of a list item (2+ leading spaces, no marker).
	if line == "" {
		return false
	}
	if !strings.HasPrefix(line, "  ") {
		return false
	}
	return !isListLine(line)
}

func parseListItem(line string) (listItem, bool) {
	if m := bulletRe.FindStringSubmatch(line); m != nil {
		li := listItem{
			indent:  len(m[1]),
			marker:  m[2],
			content: m[3],
		}
		if tm := taskRe.FindStringSubmatch(m[3]); tm != nil {
			if tm[1] == " " {
				li.task = 1
			} else {
				li.task = 2
			}
			li.content = tm[2]
		}
		return li, true
	}
	if m := orderedRe.FindStringSubmatch(line); m != nil {
		return listItem{
			indent:  len(m[1]),
			marker:  m[2] + ".",
			content: m[3],
		}, true
	}
	return listItem{}, false
}

func (r *renderer) writeList(lines []string) {
	for _, line := range lines {
		li, ok := parseListItem(line)
		if !ok {
			// Continuation: render under the previous bullet with extra
			// indent.
			content := strings.TrimLeft(line, " ")
			r.out.WriteString("    ")
			r.out.WriteString(renderInline(content))
			r.out.WriteString("\n")
			continue
		}
		// Normalise indent: 2 spaces per level.
		level := li.indent / 2
		prefix := strings.Repeat("  ", level)

		var marker string
		switch {
		case li.task == 1:
			marker = ansiFgYellow + "☐" + ansiReset
		case li.task == 2:
			marker = ansiFgGreen + "☑" + ansiReset
		case li.marker == "-" || li.marker == "*" || li.marker == "+":
			marker = ansiFgCyan + "•" + ansiReset
		default:
			marker = ansiFgCyan + li.marker + ansiReset
		}
		r.out.WriteString(prefix)
		r.out.WriteString(marker)
		r.out.WriteString(" ")
		r.out.WriteString(renderInline(li.content))
		r.out.WriteString("\n")
	}
}

// ---------------------------------------------------------------------
// Tables
// ---------------------------------------------------------------------

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func isTableHeader(line string) bool {
	return isTableRow(line) && strings.Count(line, "|") >= 2
}

func isTableSeparator(line string) bool {
	if !isTableRow(line) {
		return false
	}
	cells := splitTableRow(line)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			return false
		}
		// Allow leading/trailing ":" for alignment.
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" {
			continue
		}
		for _, r := range c {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

type colAlign int

const (
	alignLeft colAlign = iota
	alignRight
	alignCenter
)

func parseAlignments(sep string) []colAlign {
	cells := splitTableRow(sep)
	out := make([]colAlign, len(cells))
	for i, c := range cells {
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = alignCenter
		case right:
			out[i] = alignRight
		default:
			out[i] = alignLeft
		}
	}
	return out
}

func (r *renderer) writeTable(lines []string) {
	if len(lines) < 2 {
		return
	}
	headers := splitTableRow(lines[0])
	aligns := parseAlignments(lines[1])
	// Normalise alignment length to header count.
	if len(aligns) < len(headers) {
		padded := make([]colAlign, len(headers))
		copy(padded, aligns)
		aligns = padded
	}
	var rows [][]string
	for _, l := range lines[2:] {
		rows = append(rows, splitTableRow(l))
	}
	r.out.WriteString(renderTableWithAligns(headers, rows, aligns, r.width))
	// Output ends with a newline already.
}

// RenderTable renders a markdown table with aligned columns. If width is
// zero or positive but insufficient, the table uses its natural width.
// Headers are bolded, cells pass through renderInline for inline styles.
func RenderTable(headers []string, rows [][]string, width int) string {
	cols := len(headers)
	if cols == 0 {
		return ""
	}
	aligns := make([]colAlign, cols)
	return renderTableWithAligns(headers, rows, aligns, width)
}

func renderTableWithAligns(headers []string, rows [][]string, aligns []colAlign, _width int) string {
	cols := len(headers)
	widths := make([]int, cols)
	renderedHeaders := make([]string, cols)
	for i, h := range headers {
		renderedHeaders[i] = renderInline(h)
		widths[i] = visibleWidth(renderedHeaders[i])
	}
	renderedRows := make([][]string, len(rows))
	for ri, row := range rows {
		rr := make([]string, cols)
		for ci := 0; ci < cols; ci++ {
			var cell string
			if ci < len(row) {
				cell = row[ci]
			}
			rr[ci] = renderInline(cell)
			if w := visibleWidth(rr[ci]); w > widths[ci] {
				widths[ci] = w
			}
		}
		renderedRows[ri] = rr
	}

	var sb strings.Builder

	writeRow := func(cells []string, bold bool) {
		sb.WriteString(ansiFgGrey + "│" + ansiReset)
		for i, c := range cells {
			pad := padCell(c, widths[i], aligns[i])
			if bold {
				sb.WriteString(" " + ansiBold + pad + ansiReset + " ")
			} else {
				sb.WriteString(" " + pad + " ")
			}
			sb.WriteString(ansiFgGrey + "│" + ansiReset)
		}
		sb.WriteString("\n")
	}

	writeSep := func(left, mid, right string) {
		sb.WriteString(ansiFgGrey)
		sb.WriteString(left)
		for i, w := range widths {
			sb.WriteString(strings.Repeat("─", w+2))
			if i < len(widths)-1 {
				sb.WriteString(mid)
			}
		}
		sb.WriteString(right)
		sb.WriteString(ansiReset + "\n")
	}

	writeSep("┌", "┬", "┐")
	writeRow(renderedHeaders, true)
	writeSep("├", "┼", "┤")
	for _, row := range renderedRows {
		writeRow(row, false)
	}
	writeSep("└", "┴", "┘")
	return sb.String()
}

func padCell(s string, width int, align colAlign) string {
	vis := visibleWidth(s)
	if vis >= width {
		return s
	}
	pad := width - vis
	switch align {
	case alignRight:
		return strings.Repeat(" ", pad) + s
	case alignCenter:
		left := pad / 2
		right := pad - left
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
	default:
		return s + strings.Repeat(" ", pad)
	}
}

// ---------------------------------------------------------------------
// Inline styles (bold, italic, code, links, etc.)
// ---------------------------------------------------------------------

var (
	inlineLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	inlineImageRe      = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	inlineCodeRe       = regexp.MustCompile("`([^`\n]+)`")
	inlineBoldItalicRe = regexp.MustCompile(`\*\*\*([^*\n]+)\*\*\*`)
	inlineBoldRe       = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	inlineItalicRe     = regexp.MustCompile(`(^|[^*])\*([^*\n]+)\*`)
	inlineItalicUnder  = regexp.MustCompile(`(^|[^_])_([^_\n]+)_`)
)

// renderInline applies ANSI styling for inline markdown constructs.
// Order matters: we handle code first (its contents must not be further
// formatted), then images, links, bold-italic, bold, italic.
func renderInline(s string) string {
	// Protect inline code spans so their contents aren't touched by
	// subsequent regexes. We swap them for a placeholder, then restore.
	var codeSpans []string
	s = inlineCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		content := strings.TrimSuffix(strings.TrimPrefix(m, "`"), "`")
		codeSpans = append(codeSpans, content)
		return fmt.Sprintf("\x00CODE%d\x00", len(codeSpans)-1)
	})

	// Images: render as "🖼 alt" in dim style (terminals rarely inline).
	s = inlineImageRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := inlineImageRe.FindStringSubmatch(m)
		alt := sub[1]
		if alt == "" {
			alt = "image"
		}
		return ansiDim + "[image: " + alt + "]" + ansiReset
	})

	// Links: render as OSC 8 hyperlink if possible; always underline text.
	s = inlineLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := inlineLinkRe.FindStringSubmatch(m)
		text, url := sub[1], sub[2]
		return osc8Link(url, ansiUnder+ansiFgBlue+text+ansiReset)
	})

	// Bold + italic: ***x***
	s = inlineBoldItalicRe.ReplaceAllString(s, ansiBold+ansiItalic+"$1"+ansiReset)
	// Bold: **x**
	s = inlineBoldRe.ReplaceAllString(s, ansiBold+"$1"+ansiReset)
	// Italic: *x*  (require non-* before so we don't eat leftover bolds)
	s = inlineItalicRe.ReplaceAllString(s, "$1"+ansiItalic+"$2"+ansiReset)
	// Italic: _x_
	s = inlineItalicUnder.ReplaceAllString(s, "$1"+ansiItalic+"$2"+ansiReset)

	// Restore code spans with inverse-video styling.
	for i, c := range codeSpans {
		placeholder := fmt.Sprintf("\x00CODE%d\x00", i)
		s = strings.Replace(s, placeholder, ansiInverse+" "+c+" "+ansiReset, 1)
	}
	return s
}

// osc8Link wraps text in an OSC 8 hyperlink escape so compatible
// terminals (iTerm, WezTerm, recent GNOME Terminal, etc.) make it
// clickable while still showing the styled text inline.
func osc8Link(url, text string) string {
	// ESC ] 8 ;; URL ST text ESC ] 8 ;; ST
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// ---------------------------------------------------------------------
// ANSI-aware text width and wrapping
// ---------------------------------------------------------------------

// ansiRe matches any ANSI CSI (colour, cursor, etc.) escape sequence
// and OSC 8 hyperlink markers, so visible width and wrapping can skip
// over them.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\]8;[^\x1b]*\x1b\\`)

// visibleWidth returns the printable width of s after stripping ANSI
// escape sequences. It counts runes, not bytes, for Unicode correctness.
func visibleWidth(s string) int {
	stripped := ansiRe.ReplaceAllString(s, "")
	return utf8.RuneCountInString(stripped)
}

// wrapAnsiAware soft-wraps text at the given width. Width is counted in
// visible columns; ANSI sequences are preserved verbatim across wraps.
// This is a simple word-based wrapper suitable for prose paragraphs.
func wrapAnsiAware(s string, width int) string {
	if width <= 0 {
		return s
	}
	// Split on spaces; ANSI sequences live inside words.
	words := strings.Split(s, " ")
	var line strings.Builder
	var out strings.Builder
	lineWidth := 0

	for i, w := range words {
		ww := visibleWidth(w)
		if i == 0 {
			line.WriteString(w)
			lineWidth = ww
			continue
		}
		if lineWidth+1+ww > width {
			out.WriteString(line.String())
			out.WriteString("\n")
			line.Reset()
			line.WriteString(w)
			lineWidth = ww
		} else {
			line.WriteString(" ")
			line.WriteString(w)
			lineWidth += 1 + ww
		}
	}
	if line.Len() > 0 {
		out.WriteString(line.String())
	}
	return out.String()
}

// ---------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------

func parseATXHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	depth := 0
	for depth < len(trimmed) && trimmed[depth] == '#' {
		depth++
	}
	if depth == 0 || depth > 6 {
		return 0, "", false
	}
	if depth < len(trimmed) && trimmed[depth] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(trimmed[depth:])
	// Drop trailing "###" closers if present.
	text = strings.TrimRight(text, "# ")
	return depth, text, true
}

func isBlockquoteLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return strings.HasPrefix(trimmed, "> ") || trimmed == ">"
}

func stripBlockquoteMarker(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if trimmed == ">" {
		return ""
	}
	if strings.HasPrefix(trimmed, "> ") {
		return trimmed[2:]
	}
	return strings.TrimPrefix(trimmed, ">")
}

func isAllChar(s string, c byte) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
