package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Quick Open file picker (Ctrl+P) ─────────────────────────────────────────
//
// quickOpenModel is a Bubbletea overlay that lets the user fuzzy-find any
// file in the project. It loads the project tree once on creation
// (respecting .gitignore and a hard-coded skip list), then updates the
// match list on every keystroke. Selecting a result returns the relative
// path so the caller can insert it as an "@path" mention in the textarea.

// quickOpenMaxFiles caps how many files we keep in memory. Beyond that
// the picker stops scanning to avoid pathologically slow startup on huge
// monorepos.
const quickOpenMaxFiles = 10000

// quickOpenMaxResults bounds how many matches are rendered at once.
const quickOpenMaxResults = 20

// fileMatch is a single fuzzy-match candidate, with a score and the
// indices of characters in Path that matched the query (for highlighting).
type fileMatch struct {
	Path      string
	Score     float64
	Highlight []int
}

// quickOpenModel holds the picker state.
type quickOpenModel struct {
	query    string
	rootDir  string
	files    []string
	matches  []fileMatch
	selected int
	width    int
	height   int

	// totalFiles is the unfiltered count, displayed in the footer.
	totalFiles int
}

// newQuickOpen builds a picker rooted at rootDir. The file index is
// loaded eagerly so the first keystroke responds instantly.
func newQuickOpen(rootDir string) *quickOpenModel {
	if rootDir == "" {
		if wd, err := os.Getwd(); err == nil {
			rootDir = wd
		} else {
			rootDir = "."
		}
	}
	q := &quickOpenModel{
		rootDir: rootDir,
	}
	q.files = q.loadFiles(rootDir)
	q.totalFiles = len(q.files)
	// Initial state: show the first quickOpenMaxResults files unsorted.
	q.matches = q.fuzzyMatch("")
	return q
}

// SetSize updates the cached terminal dimensions used by View().
func (m *quickOpenModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Update processes keyboard input. Returns the (possibly mutated)
// model, an optional command, and a non-empty string when the user
// confirmed a selection (the chosen path) — the caller is then
// responsible for closing the picker. An empty selection string
// combined with a nil command means "still open, no action".
//
// Esc cancels and is signalled by returning the sentinel "\x00cancel".
func (m *quickOpenModel) Update(msg tea.Msg) (*quickOpenModel, tea.Cmd, string) {
	if m == nil {
		return m, nil, ""
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil, ""

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			return m, nil, quickOpenCancelSentinel
		case tea.KeyEnter:
			if m.selected >= 0 && m.selected < len(m.matches) {
				return m, nil, m.matches[m.selected].Path
			}
			return m, nil, ""
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
				// Trim one rune (handle multi-byte safely).
				r := []rune(m.query)
				m.query = string(r[:len(r)-1])
				m.matches = m.fuzzyMatch(m.query)
				m.selected = 0
			}
			return m, nil, ""
		case tea.KeyRunes, tea.KeySpace:
			// Append typed runes to the query and recompute matches.
			var s string
			if msg.Type == tea.KeySpace {
				s = " "
			} else {
				s = string(msg.Runes)
			}
			if s != "" {
				m.query += s
				m.matches = m.fuzzyMatch(m.query)
				m.selected = 0
			}
			return m, nil, ""
		}
	}
	return m, nil, ""
}

// quickOpenCancelSentinel is returned by Update when the user presses
// Esc. It's a deliberately invalid path so the caller can distinguish
// "cancel" from "no action".
const quickOpenCancelSentinel = "\x00cancel"

// Overlay returns the picker rendered centered on a screen of the
// given dimensions. Used by the host model's View().
func (m *quickOpenModel) Overlay(width, height int) string {
	if m == nil {
		return ""
	}
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

// View renders the picker as a bordered block. The caller is expected
// to overlay this on top of the chat view (e.g. via lipgloss.Place).
func (m *quickOpenModel) View() string {
	if m == nil {
		return ""
	}

	width := m.width
	if width <= 0 {
		width = 80
	}
	// Cap the modal width so it stays comfortable on wide terminals.
	boxWidth := width - 6
	if boxWidth > 80 {
		boxWidth = 80
	}
	if boxWidth < 40 {
		boxWidth = 40
	}

	var b strings.Builder

	// Header line: emoji + label + query + cursor.
	header := fmt.Sprintf("Buscar arquivo: %s", m.query)
	b.WriteString(quickOpenHeaderStyle.Render(header))
	b.WriteString(quickOpenCursorStyle.Render("▍"))
	b.WriteString("\n")
	b.WriteString(quickOpenSepStyle.Render(strings.Repeat("─", boxWidth-4)))
	b.WriteString("\n")

	// Match list.
	if len(m.matches) == 0 {
		b.WriteString("\n  ")
		b.WriteString(quickOpenDimStyle.Render("(nenhum arquivo encontrado)"))
		b.WriteString("\n")
	} else {
		shown := len(m.matches)
		if shown > quickOpenMaxResults {
			shown = quickOpenMaxResults
		}
		for i := 0; i < shown; i++ {
			match := m.matches[i]
			line := m.renderMatchLine(match, i == m.selected, boxWidth-4)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Footer.
	b.WriteString("\n")
	b.WriteString(quickOpenFooterStyle.Render(
		fmt.Sprintf("  %d de %d arquivos", len(m.matches), m.totalFiles)))
	b.WriteString("\n")
	b.WriteString(quickOpenFooterStyle.Render(
		"  ↑↓ navegar  Enter inserir  Esc cancelar"))

	return quickOpenBoxStyle.Width(boxWidth).Render(b.String())
}

// renderMatchLine renders a single match row with highlight markup
// and selection arrow.
func (m *quickOpenModel) renderMatchLine(match fileMatch, selected bool, width int) string {
	var b strings.Builder
	if selected {
		b.WriteString(quickOpenSelectedStyle.Render("► "))
	} else {
		b.WriteString("  ")
	}
	// Truncate long paths from the left so the basename stays visible.
	path := match.Path
	maxPathWidth := width - 12 // leave room for the score column
	if maxPathWidth < 20 {
		maxPathWidth = 20
	}
	if len(path) > maxPathWidth {
		path = "…" + path[len(path)-maxPathWidth+1:]
	}
	rendered := highlightPath(path, match.Highlight, selected)
	b.WriteString(rendered)
	// Pad to the score column.
	visible := visibleLen(path) + 2
	if visible < width-8 {
		b.WriteString(strings.Repeat(" ", width-8-visible))
	}
	b.WriteString(quickOpenScoreStyle.Render(fmt.Sprintf("%.2f", match.Score)))
	return b.String()
}

// highlightPath wraps matched character ranges in the highlight style.
// Highlights are byte indices into the *original* match.Path; we
// recompute them against the (possibly truncated) display string by
// using the rune content of the truncated path. For simplicity, when
// the path was truncated we skip highlighting and just render plain.
func highlightPath(display string, highlight []int, selected bool) string {
	if len(highlight) == 0 {
		if selected {
			return quickOpenSelectedStyle.Render(display)
		}
		return quickOpenPathStyle.Render(display)
	}
	hi := make(map[int]bool, len(highlight))
	for _, idx := range highlight {
		hi[idx] = true
	}
	var b strings.Builder
	for i, r := range display {
		ch := string(r)
		switch {
		case hi[i] && selected:
			b.WriteString(quickOpenHighlightSelectedStyle.Render(ch))
		case hi[i]:
			b.WriteString(quickOpenHighlightStyle.Render(ch))
		case selected:
			b.WriteString(quickOpenSelectedStyle.Render(ch))
		default:
			b.WriteString(quickOpenPathStyle.Render(ch))
		}
	}
	return b.String()
}

// visibleLen returns the visible length of s in runes (ignoring ANSI
// escape sequences, which lipgloss adds).
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}

// loadFiles walks the project tree, honoring a built-in skip list and
// .gitignore directives. The result is sorted alphabetically.
func (m *quickOpenModel) loadFiles(rootDir string) []string {
	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
		"target":       true,
		"dist":         true,
		"build":        true,
		"__pycache__":  true,
		".venv":        true,
		".next":        true,
		".cache":       true,
		".idea":        true,
		".vscode":      true,
		"coverage":     true,
	}

	patterns := loadGitignorePatterns(rootDir)

	var files []string
	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip the rootDir itself.
		if path == rootDir {
			return nil
		}

		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] {
				return filepath.SkipDir
			}
			if matchAnyPattern(patterns, rel, true) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files matching .gitignore patterns.
		if matchAnyPattern(patterns, rel, false) {
			return nil
		}
		// Skip hidden top-level dotfiles by default? No — many users
		// want to find e.g. .env.example. Keep them.
		files = append(files, rel)
		if len(files) >= quickOpenMaxFiles {
			return errQuickOpenStop
		}
		return nil
	})

	sort.Strings(files)
	return files
}

// errQuickOpenStop is a sentinel error used to short-circuit the
// filepath.Walk once we've hit quickOpenMaxFiles. filepath.Walk treats
// any non-nil error from the callback as a stop signal, which is
// exactly what we want.
var errQuickOpenStop = quickOpenStopErr{}

type quickOpenStopErr struct{}

func (quickOpenStopErr) Error() string { return "quickopen: file cap reached" }

// loadGitignorePatterns reads .gitignore at rootDir and returns a list
// of normalized patterns. We only honor the top-level .gitignore for
// performance — full nested-gitignore semantics are unnecessary for a
// fuzzy file picker.
func loadGitignorePatterns(rootDir string) []string {
	f, err := os.Open(filepath.Join(rootDir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading "!" (negation) — we don't honour negation;
		// just skip those lines so we don't accidentally exclude
		// included files.
		if strings.HasPrefix(line, "!") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchAnyPattern reports whether the given relative path matches any
// of the .gitignore patterns. Supports the most common subset:
//   - "name" or "name/" matches that name anywhere in the tree
//   - "/name" matches only at the root
//   - "*.ext" globs match the basename
//   - "dir/*" matches direct children of dir
//
// This is intentionally simple — for full gitignore semantics we
// would need a dedicated library, but the simple form covers ~95%
// of real-world projects.
func matchAnyPattern(patterns []string, rel string, isDir bool) bool {
	if len(patterns) == 0 {
		return false
	}
	base := filepath.Base(rel)
	for _, pat := range patterns {
		if pat == "" {
			continue
		}
		// Directory-only pattern.
		dirOnly := strings.HasSuffix(pat, "/")
		if dirOnly {
			pat = strings.TrimSuffix(pat, "/")
			if !isDir {
				continue
			}
		}
		// Anchor at root.
		anchored := strings.HasPrefix(pat, "/")
		if anchored {
			pat = strings.TrimPrefix(pat, "/")
		}
		// Try the various match strategies.
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
		if !anchored {
			if matched, _ := filepath.Match(pat, base); matched {
				return true
			}
			// Match against any path segment.
			parts := strings.Split(rel, string(os.PathSeparator))
			for _, p := range parts {
				if p == pat {
					return true
				}
				if matched, _ := filepath.Match(pat, p); matched {
					return true
				}
			}
		}
	}
	return false
}

// fuzzyMatch scores every file in m.files against the query and returns
// the top quickOpenMaxResults matches sorted by descending score.
//
// Scoring rubric (higher is better):
//
//	+100  exact basename match
//	+ 80  basename starts with query
//	+ 60  basename contains query as a substring
//	+ 40  full relative path contains query as a substring
//	+ 20  fuzzy subsequence match (each query char appears in order)
//	+  4  bonus for shorter paths (so test files don't shadow main files)
//
// Matched character indices in the relative path are recorded so the
// view can highlight them.
func (m *quickOpenModel) fuzzyMatch(query string) []fileMatch {
	query = strings.ToLower(strings.TrimSpace(query))
	// Empty query: return the first quickOpenMaxResults files in sorted order.
	if query == "" {
		out := make([]fileMatch, 0, quickOpenMaxResults)
		for i, f := range m.files {
			if i >= quickOpenMaxResults {
				break
			}
			out = append(out, fileMatch{Path: f, Score: 0})
		}
		return out
	}

	var results []fileMatch

	for _, f := range m.files {
		lower := strings.ToLower(f)
		base := strings.ToLower(filepath.Base(f))

		score := 0.0
		var hi []int

		switch {
		case base == query:
			score += 100
			// Highlight the basename.
			start := strings.LastIndex(lower, base)
			for i := 0; i < len(base); i++ {
				hi = append(hi, start+i)
			}
		case strings.HasPrefix(base, query):
			score += 80
			start := strings.LastIndex(lower, base)
			for i := 0; i < len(query); i++ {
				hi = append(hi, start+i)
			}
		case strings.Contains(base, query):
			score += 60
			start := strings.Index(lower, query)
			for i := 0; i < len(query); i++ {
				hi = append(hi, start+i)
			}
		case strings.Contains(lower, query):
			score += 40
			start := strings.Index(lower, query)
			for i := 0; i < len(query); i++ {
				hi = append(hi, start+i)
			}
		default:
			// Fuzzy subsequence: every query char must appear in
			// order somewhere in the path.
			indices, ok := fuzzySubsequence(query, lower)
			if !ok {
				continue
			}
			score += 20
			hi = indices
		}

		// Length bonus: shorter paths score higher (4 → 0 over 100 chars).
		lenPenalty := float64(len(f)) * 0.04
		score -= lenPenalty
		if score < 0 {
			score = 0
		}
		// Normalize to a 0..1 display range.
		display := score / 100.0
		if display > 1 {
			display = 1
		}

		results = append(results, fileMatch{
			Path:      f,
			Score:     display,
			Highlight: hi,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > quickOpenMaxResults*2 {
		results = results[:quickOpenMaxResults*2]
	}
	return results
}

// fuzzySubsequence reports whether each character of query appears in
// target in the same order, and returns the byte indices of the
// matched characters in target.
func fuzzySubsequence(query, target string) ([]int, bool) {
	if query == "" {
		return nil, true
	}
	indices := make([]int, 0, len(query))
	qi := 0
	for i := 0; i < len(target) && qi < len(query); i++ {
		if target[i] == query[qi] {
			indices = append(indices, i)
			qi++
		}
	}
	if qi != len(query) {
		return nil, false
	}
	return indices, true
}

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	quickOpenBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorCyan).
				Padding(1, 2)

	quickOpenHeaderStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	quickOpenCursorStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	quickOpenSepStyle = lipgloss.NewStyle().
				Foreground(colorBorderDim)

	quickOpenPathStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	quickOpenSelectedStyle = lipgloss.NewStyle().
				Foreground(colorGreen).
				Bold(true)

	quickOpenHighlightStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	quickOpenHighlightSelectedStyle = lipgloss.NewStyle().
					Foreground(colorYellow).
					Background(colorAccent).
					Bold(true)

	quickOpenScoreStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)

	quickOpenDimStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)

	quickOpenFooterStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)
)
