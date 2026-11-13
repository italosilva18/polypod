package cli

import (
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// historyMatch is a single fuzzy-matched history entry.
type historyMatch struct {
	Index     int
	Text      string
	Score     float64
	Highlight []int // byte positions of matched chars (for rendering)
}

// historySearchModel implements an interactive Ctrl+R search overlay.
type historySearchModel struct {
	query    string
	history  []string // newest first
	matches  []historyMatch
	selected int

	width  int
	height int
}

// newHistorySearch builds a search model from a history slice. The
// caller passes the entries in chronological order; we reverse them so
// the most-recent appears first.
func newHistorySearch(history []string) *historySearchModel {
	rev := make([]string, len(history))
	for i, h := range history {
		rev[len(history)-1-i] = h
	}
	m := &historySearchModel{history: rev}
	m.matches = m.Search("")
	return m
}

// SetSize is called by the host model on window resize.
func (m *historySearchModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Update processes a key event. Returns the (possibly updated) model,
// an optional command, and the picked text. The picked string is empty
// unless the user confirmed a selection with Enter or cancelled with
// Esc.
//
// The third return value uses these conventions:
//
//	""           – nothing yet, continue
//	"<text>"     – user pressed Enter on this entry
//	"\x00cancel" – user pressed Esc; host should close overlay
func (m *historySearchModel) Update(msg tea.Msg) (*historySearchModel, tea.Cmd, string) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, ""
	}

	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, nil, "\x00cancel"
	case tea.KeyEnter:
		if len(m.matches) == 0 {
			return m, nil, "\x00cancel"
		}
		if m.selected < 0 || m.selected >= len(m.matches) {
			m.selected = 0
		}
		return m, nil, m.matches[m.selected].Text
	case tea.KeyUp, tea.KeyCtrlP:
		if m.selected > 0 {
			m.selected--
		}
		return m, nil, ""
	case tea.KeyDown, tea.KeyCtrlN:
		if m.selected < len(m.matches)-1 {
			m.selected++
		}
		return m, nil, ""
	case tea.KeyBackspace:
		if len(m.query) > 0 {
			r := []rune(m.query)
			m.query = string(r[:len(r)-1])
			m.matches = m.Search(m.query)
			m.selected = 0
		}
		return m, nil, ""
	case tea.KeyCtrlR:
		// Cycle through matches: shifted Ctrl+R behavior.
		if len(m.matches) > 0 {
			m.selected = (m.selected + 1) % len(m.matches)
		}
		return m, nil, ""
	case tea.KeyRunes, tea.KeySpace:
		m.query += string(keyMsg.Runes)
		m.matches = m.Search(m.query)
		m.selected = 0
		return m, nil, ""
	}
	return m, nil, ""
}

// Search runs the fuzzy matcher over the entire history and returns
// up to 50 matches sorted by descending score. An empty query returns
// the most-recent entries unfiltered.
func (m *historySearchModel) Search(query string) []historyMatch {
	if strings.TrimSpace(query) == "" {
		out := make([]historyMatch, 0, len(m.history))
		for i, entry := range m.history {
			out = append(out, historyMatch{Index: i, Text: entry, Score: 0})
			if len(out) >= 50 {
				break
			}
		}
		return out
	}

	q := strings.ToLower(query)
	var results []historyMatch
	for i, entry := range m.history {
		score, hl, ok := fuzzyScore(strings.ToLower(entry), q)
		if !ok {
			continue
		}
		results = append(results, historyMatch{
			Index:     i,
			Text:      entry,
			Score:     score,
			Highlight: hl,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > 50 {
		results = results[:50]
	}
	return results
}

// fuzzyScore implements a small fzf-style scoring function. It returns
// the score, the byte positions of matched characters, and a boolean
// indicating whether all query chars were found in order.
//
// Scoring rules:
//   - +10 for a contiguous match
//   - +6  for a match at a word boundary (after space, _, /, -, etc.)
//   - +3  for a base match
//   - +5  bonus when the entry begins with the query (prefix)
//   - −1  per character of distance between non-contiguous matches
func fuzzyScore(text, query string) (float64, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	if len(query) > len(text) {
		return 0, nil, false
	}

	// Quick prefix bonus
	prefixBonus := 0.0
	if strings.HasPrefix(text, query) {
		prefixBonus = 5
	}

	highlights := make([]int, 0, len(query))
	score := prefixBonus

	ti := 0
	prevMatched := false
	prevPos := -2
	for qi := 0; qi < len(query); qi++ {
		qc := query[qi]
		found := false
		for ti < len(text) {
			tc := text[ti]
			if tc == qc {
				highlights = append(highlights, ti)

				charScore := 3.0
				// Word-boundary bonus.
				if ti == 0 || isWordBoundary(text[ti-1]) {
					charScore += 3
				}
				// Contiguous-match bonus.
				if prevMatched && prevPos == ti-1 {
					charScore += 7
				}
				// Distance penalty.
				if prevPos >= 0 {
					gap := ti - prevPos - 1
					if gap > 0 {
						charScore -= float64(gap) * 0.25
					}
				}
				score += charScore
				prevMatched = true
				prevPos = ti
				ti++
				found = true
				break
			}
			prevMatched = false
			ti++
		}
		if !found {
			return 0, nil, false
		}
	}
	return score, highlights, true
}

func isWordBoundary(b byte) bool {
	if b == ' ' || b == '\t' || b == '\n' || b == '/' ||
		b == '_' || b == '-' || b == '.' || b == ':' || b == '\\' {
		return true
	}
	// Treat lower→upper transitions as boundaries (handled by caller
	// inspecting case). Here we just check for common ASCII separators.
	return !unicode.IsLetter(rune(b)) && !unicode.IsDigit(rune(b))
}

// ── View ────────────────────────────────────────────────────────────────────

func (m *historySearchModel) View() string {
	var b strings.Builder

	// Header with cursor.
	b.WriteString(historySearchHeaderStyle.Render("Buscar histórico: "))
	b.WriteString(historySearchQueryStyle.Render(m.query + "▏"))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", historySearchBoxWidth-4)))
	b.WriteString("\n\n")

	if len(m.matches) == 0 {
		b.WriteString(historySearchEmptyStyle.Render("  (sem resultados)"))
		b.WriteString("\n")
	} else {
		// Show up to 12 matches around the selected index.
		maxRows := 12
		start := 0
		if m.selected >= maxRows {
			start = m.selected - maxRows + 1
		}
		end := start + maxRows
		if end > len(m.matches) {
			end = len(m.matches)
		}

		for i := start; i < end; i++ {
			match := m.matches[i]
			isSel := i == m.selected
			if isSel {
				b.WriteString(historySearchArrowStyle.Render("► "))
			} else {
				b.WriteString("  ")
			}
			line := truncate(match.Text, historySearchBoxWidth-8)
			if isSel {
				b.WriteString(historySearchSelectedStyle.Render(line))
			} else {
				b.WriteString(historySearchValueStyle.Render(line))
			}
			b.WriteString("\n")
		}
		if end < len(m.matches) {
			b.WriteString(historySearchHintStyle.Render("  ..."))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(historySearchHintStyle.Render("  ↑↓ navegar  Enter selecionar  Esc cancelar"))

	return historySearchBoxStyle.Render(b.String())
}

// Overlay returns the search rendered centered on a screen of the
// given size.
func (m *historySearchModel) Overlay(width, height int) string {
	body := m.View()
	if width <= 0 || height <= 0 {
		return body
	}
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// ── Styles ──────────────────────────────────────────────────────────────────

const historySearchBoxWidth = 60

var (
	historySearchBoxStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(colorMagenta).
				Padding(1, 2).
				Width(historySearchBoxWidth)

	historySearchHeaderStyle = lipgloss.NewStyle().
					Foreground(colorMagenta).
					Bold(true)

	historySearchQueryStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	historySearchArrowStyle = lipgloss.NewStyle().
				Foreground(colorMagenta).
				Bold(true)

	historySearchSelectedStyle = lipgloss.NewStyle().
					Foreground(colorGreen).
					Bold(true)

	historySearchValueStyle = lipgloss.NewStyle().
				Foreground(colorWhite)

	historySearchHintStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	historySearchEmptyStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)
)
