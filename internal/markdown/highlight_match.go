package markdown

import (
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// matchHighlightStyle is the default style applied to substring/regex
// matches by HighlightMatches and HighlightRegex: black bold text on a
// yellow background, mimicking how grep/ripgrep highlight hits in a
// modern terminal.
var matchHighlightStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#F3F99D")).
	Foreground(lipgloss.Color("#000000")).
	Bold(true)

// fuzzyHighlightStyle is the default style applied to per-character
// fuzzy match positions by HighlightFuzzy: bold green, no background,
// to keep the surrounding path/identifier readable.
var fuzzyHighlightStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#5AF78E")).
	Bold(true)

// HighlightMatches returns text with each occurrence of any string in
// matches wrapped in ANSI color codes (black bold on yellow). Matches
// are case-sensitive plain substring matches and may overlap; the
// highlighter merges overlapping ranges so each character is styled at
// most once.
//
// Empty matches and a nil/empty slice yield text unchanged.
func HighlightMatches(text string, matches []string) string {
	if text == "" || len(matches) == 0 {
		return text
	}

	var spans []matchSpan
	for _, needle := range matches {
		if needle == "" {
			continue
		}
		from := 0
		for {
			idx := strings.Index(text[from:], needle)
			if idx < 0 {
				break
			}
			start := from + idx
			end := start + len(needle)
			spans = append(spans, matchSpan{start, end})
			from = end
		}
	}
	if len(spans) == 0 {
		return text
	}
	merged := mergeSpans(spans)
	return applySpans(text, merged, matchHighlightStyle)
}

// HighlightRegex returns text with each match of pattern wrapped in
// ANSI color codes. The same default match style as HighlightMatches
// is applied. A nil pattern returns text unchanged.
func HighlightRegex(text string, pattern *regexp.Regexp) string {
	if text == "" || pattern == nil {
		return text
	}
	indexes := pattern.FindAllStringIndex(text, -1)
	if len(indexes) == 0 {
		return text
	}
	spans := make([]matchSpan, 0, len(indexes))
	for _, idx := range indexes {
		if len(idx) >= 2 && idx[0] < idx[1] {
			spans = append(spans, matchSpan{idx[0], idx[1]})
		}
	}
	if len(spans) == 0 {
		return text
	}
	merged := mergeSpans(spans)
	return applySpans(text, merged, matchHighlightStyle)
}

// HighlightFuzzy highlights individual character matches from a fuzzy
// search. positions are byte indices into text; out-of-range or
// duplicate positions are ignored. Each highlighted character is
// rendered with the bold green fuzzy style. Negative positions are
// silently dropped.
func HighlightFuzzy(text string, positions []int) string {
	if text == "" || len(positions) == 0 {
		return text
	}
	// Deduplicate and sort.
	seen := make(map[int]bool, len(positions))
	clean := make([]int, 0, len(positions))
	for _, p := range positions {
		if p < 0 || p >= len(text) {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return text
	}
	sort.Ints(clean)

	var b strings.Builder
	last := 0
	for _, p := range clean {
		// Append unstyled text before this position. Use rune-safe
		// slicing by working in bytes; positions are documented as
		// byte indices.
		if p > last {
			b.WriteString(text[last:p])
		}
		// Determine the rune at p so we highlight the full UTF-8
		// codepoint, not a single byte.
		runeStart := p
		runeEnd := p + 1
		// Walk forward to find the end of the current rune.
		for runeEnd < len(text) && (text[runeEnd]&0xC0) == 0x80 {
			runeEnd++
		}
		b.WriteString(fuzzyHighlightStyle.Render(text[runeStart:runeEnd]))
		last = runeEnd
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return b.String()
}

// DiffHighlight describes a single (text, style) pair for
// HighlightMultiple. The Text is matched as a plain substring (case
// sensitive) and any occurrence is wrapped in the supplied style.
type DiffHighlight struct {
	Text  string
	Style lipgloss.Style
}

// HighlightMultiple wraps occurrences of each highlight's Text in its
// associated Style. Useful for showing search results that combine
// keyword hits, secondary context, and other annotations.
//
// Highlights are applied in the order they appear in the slice; if two
// highlights overlap, the first one wins for the overlapping range.
// Empty Text entries are skipped.
func HighlightMultiple(text string, highlights []DiffHighlight) string {
	if text == "" || len(highlights) == 0 {
		return text
	}

	type styledSpan struct {
		start, end int
		style      lipgloss.Style
	}
	var spans []styledSpan

	for _, h := range highlights {
		if h.Text == "" {
			continue
		}
		from := 0
		for {
			idx := strings.Index(text[from:], h.Text)
			if idx < 0 {
				break
			}
			start := from + idx
			end := start + len(h.Text)
			spans = append(spans, styledSpan{start, end, h.Style})
			from = end
		}
	}
	if len(spans) == 0 {
		return text
	}

	// Sort by start; on tie keep the earlier (higher-priority) entry
	// first so the overlap-resolution loop below favours it.
	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].start < spans[j].start
	})

	// Resolve overlaps: drop any span that starts before the previous
	// one ended (later spans lose to earlier ones).
	cleaned := spans[:0]
	prevEnd := 0
	for _, s := range spans {
		if s.start < prevEnd {
			continue
		}
		cleaned = append(cleaned, s)
		prevEnd = s.end
	}

	var b strings.Builder
	last := 0
	for _, s := range cleaned {
		if s.start > last {
			b.WriteString(text[last:s.start])
		}
		b.WriteString(s.style.Render(text[s.start:s.end]))
		last = s.end
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return b.String()
}

// ── Internal helpers ───────────────────────────────────────────────────────

// matchSpan is a half-open [start, end) byte range used internally by
// the match highlighters.
type matchSpan struct{ start, end int }

// mergeSpans takes possibly-overlapping spans and returns a sorted,
// merged slice with no overlaps.
func mergeSpans(in []matchSpan) []matchSpan {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].start < in[j].start })
	out := make([]matchSpan, 0, len(in))
	cur := in[0]
	for i := 1; i < len(in); i++ {
		s := in[i]
		if s.start <= cur.end {
			if s.end > cur.end {
				cur.end = s.end
			}
			continue
		}
		out = append(out, cur)
		cur = s
	}
	out = append(out, cur)
	return out
}

// applySpans wraps each span range in the given style and returns the
// resulting string. Spans must be sorted and non-overlapping.
func applySpans(text string, spans []matchSpan, style lipgloss.Style) string {
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + len(spans)*8)
	last := 0
	for _, s := range spans {
		if s.start < last || s.end > len(text) || s.start >= s.end {
			continue
		}
		if s.start > last {
			b.WriteString(text[last:s.start])
		}
		b.WriteString(style.Render(text[s.start:s.end]))
		last = s.end
	}
	if last < len(text) {
		b.WriteString(text[last:])
	}
	return b.String()
}
