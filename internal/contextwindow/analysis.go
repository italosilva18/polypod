package contextwindow

import (
	"fmt"
	"strings"
)

// ContextSection describes a single logical slice of the context window.
type ContextSection struct {
	Name    string  // "System Prompt", "History", "Tools", "Project Context", "Memory"
	Tokens  int     // tokens consumed by this section
	Percent float64 // percent of the total used context (not of max)
	Color   string  // "green", "yellow", "red" — semantic priority
}

// ContextAnalysis is a detailed breakdown of context window usage, suitable
// for rendering via the /context slash command.
type ContextAnalysis struct {
	TotalUsed       int
	TotalMax        int
	UsagePercent    float64 // percent of TotalMax that is used
	Sections        []ContextSection
	Warnings        []string
	Recommendations []string
}

// AnalyzeContext builds a detailed breakdown of context window usage from the
// supplied per-category token counts. It does not mutate the Manager; it
// simply reads the configured model limits.
//
// Sections with zero tokens are omitted from the result so the display stays
// compact. Percentages are computed against the total used tokens (not the
// model max) so that they always sum to ~100% within the distribution view.
func (m *Manager) AnalyzeContext(
	systemPromptTokens int,
	historyTokens int,
	toolTokens int,
	projectTokens int,
	memoryTokens int,
) *ContextAnalysis {
	m.mu.RLock()
	maxTokens := m.limits.MaxContextTokens
	autoThresh := m.autoCompactThreshold
	critThresh := m.criticalThreshold
	m.mu.RUnlock()

	total := systemPromptTokens + historyTokens + toolTokens + projectTokens + memoryTokens

	usagePct := 0.0
	if maxTokens > 0 {
		usagePct = float64(total) / float64(maxTokens) * 100
	}

	a := &ContextAnalysis{
		TotalUsed:    total,
		TotalMax:     maxTokens,
		UsagePercent: usagePct,
	}

	// Build sections in a stable display order.
	rawSections := []struct {
		name   string
		tokens int
	}{
		{"System Prompt", systemPromptTokens},
		{"History", historyTokens},
		{"Tools", toolTokens},
		{"Project Context", projectTokens},
		{"Memory", memoryTokens},
	}

	for _, rs := range rawSections {
		if rs.tokens <= 0 {
			continue
		}
		pct := 0.0
		if total > 0 {
			pct = float64(rs.tokens) / float64(total) * 100
		}
		a.Sections = append(a.Sections, ContextSection{
			Name:    rs.name,
			Tokens:  rs.tokens,
			Percent: pct,
			Color:   sectionColor(rs.name, pct),
		})
	}

	// Build warnings based on usage level relative to the configured
	// thresholds.
	ratio := 0.0
	if maxTokens > 0 {
		ratio = float64(total) / float64(maxTokens)
	}

	switch {
	case ratio >= critThresh:
		a.Warnings = append(a.Warnings,
			"Uso CRITICO: janela de contexto quase cheia")
		a.Recommendations = append(a.Recommendations,
			"Execute /compact imediatamente para liberar espaco",
			"Remova arquivos de /add-dir que nao forem essenciais")
	case ratio >= autoThresh:
		a.Warnings = append(a.Warnings,
			"Uso ALTO: considere compactar a historia em breve")
		a.Recommendations = append(a.Recommendations,
			"Execute /compact antes de adicionar mais contexto",
			"Revise a memoria persistente com /memory")
	case ratio >= 0.50:
		a.Warnings = append(a.Warnings,
			"Uso moderado, ainda ha bastante espaco")
		a.Recommendations = append(a.Recommendations,
			"Considere /compact se continuar longa conversa",
			"Use /add-dir para incluir mais contexto")
	default:
		a.Warnings = append(a.Warnings,
			"Uso normal, ainda ha espaco")
		a.Recommendations = append(a.Recommendations,
			"Use /add-dir para incluir mais contexto",
			"Use /memory para salvar fatos importantes")
	}

	// Specific recommendations based on which section dominates the context.
	if dominant := dominantSection(a.Sections); dominant != nil {
		switch dominant.Name {
		case "History":
			if dominant.Percent > 60 {
				a.Recommendations = append(a.Recommendations,
					"History domina o contexto — /compact pode ajudar")
			}
		case "Tools":
			if dominant.Percent > 40 {
				a.Recommendations = append(a.Recommendations,
					"Tools ocupam muito espaco — considere desabilitar MCPs nao usados")
			}
		case "Project Context":
			if dominant.Percent > 50 {
				a.Recommendations = append(a.Recommendations,
					"Project Context pesado — remova diretorios irrelevantes de /add-dir")
			}
		}
	}

	return a
}

// sectionColor assigns a semantic color to a section based on how much of the
// used context it occupies. These colors are advisory hints for the
// renderer — FormatAnalysis uses them to tint the bars.
func sectionColor(name string, pct float64) string {
	switch {
	case pct >= 60:
		return "red"
	case pct >= 30:
		return "yellow"
	default:
		return "green"
	}
}

// dominantSection returns the section that uses the largest share of the
// context, or nil if there are no sections.
func dominantSection(sections []ContextSection) *ContextSection {
	if len(sections) == 0 {
		return nil
	}
	best := &sections[0]
	for i := 1; i < len(sections); i++ {
		if sections[i].Percent > best.Percent {
			best = &sections[i]
		}
	}
	return best
}

// ── ANSI color helpers (kept self-contained, no external deps) ──────────────

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiCyan   = "\033[36m"
	ansiGrey   = "\033[90m"
)

// colorize wraps s with the ANSI sequence for the given logical color name.
func colorize(color, s string) string {
	var prefix string
	switch color {
	case "green":
		prefix = ansiGreen
	case "yellow":
		prefix = ansiYellow
	case "red":
		prefix = ansiRed
	case "cyan":
		prefix = ansiCyan
	case "grey", "dim":
		prefix = ansiGrey
	default:
		return s
	}
	return prefix + s + ansiReset
}

// FormatAnalysis returns a nicely formatted terminal display of the analysis.
//
// Example output:
//
//	Contexto: 45.2k / 128k tokens (35%)
//	━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//
//	DISTRIBUICAO:
//	  System Prompt      2.3k  ( 5%)  ██
//	  History           28.7k  (63%)  ████████████████
//	  ...
//
//	AVISOS:
//	  ℹ Uso normal, ainda ha espaco
//
//	RECOMENDACOES:
//	  • Considere /compact se continuar longa conversa
func FormatAnalysis(a *ContextAnalysis) string {
	if a == nil {
		return ""
	}

	var b strings.Builder

	// Header line: "Contexto: 45.2k / 128k tokens (35%)"
	header := fmt.Sprintf("Contexto: %s / %s tokens (%.0f%%)",
		formatTokensShort(a.TotalUsed),
		formatTokensShort(a.TotalMax),
		a.UsagePercent,
	)
	headerColor := overallColor(a.UsagePercent)
	b.WriteString(colorize(headerColor, ansiBold+header+ansiReset))
	b.WriteString("\n")
	b.WriteString(colorize("grey", strings.Repeat("━", 32)))
	b.WriteString("\n\n")

	// DISTRIBUICAO section
	if len(a.Sections) > 0 {
		b.WriteString(ansiBold)
		b.WriteString("DISTRIBUICAO:")
		b.WriteString(ansiReset)
		b.WriteString("\n")

		// Compute column widths for alignment.
		maxNameLen := 0
		for _, s := range a.Sections {
			if len(s.Name) > maxNameLen {
				maxNameLen = len(s.Name)
			}
		}

		// Fixed bar width. 25 chars represents 100% of local usage.
		const barWidth = 25

		for _, s := range a.Sections {
			namePad := strings.Repeat(" ", maxNameLen-len(s.Name))
			filled := int(s.Percent / 100.0 * barWidth)
			if filled < 1 && s.Percent > 0 {
				filled = 1
			}
			if filled > barWidth {
				filled = barWidth
			}
			bar := strings.Repeat("█", filled)

			line := fmt.Sprintf("  %s%s  %6s  (%3.0f%%)  %s",
				s.Name,
				namePad,
				formatTokensShort(s.Tokens),
				s.Percent,
				colorize(s.Color, bar),
			)
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// AVISOS section
	if len(a.Warnings) > 0 {
		b.WriteString(ansiBold)
		b.WriteString("AVISOS:")
		b.WriteString(ansiReset)
		b.WriteString("\n")
		for _, w := range a.Warnings {
			icon, color := warningIconColor(a.UsagePercent)
			b.WriteString("  ")
			b.WriteString(colorize(color, icon+" "+w))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// RECOMENDACOES section
	if len(a.Recommendations) > 0 {
		b.WriteString(ansiBold)
		b.WriteString("RECOMENDACOES:")
		b.WriteString(ansiReset)
		b.WriteString("\n")
		for _, r := range a.Recommendations {
			b.WriteString("  ")
			b.WriteString(colorize("cyan", "•"))
			b.WriteString(" ")
			b.WriteString(r)
			b.WriteString("\n")
		}
	}

	return b.String()
}

// overallColor picks a semantic color for the header line based on overall
// usage percent of the model max.
func overallColor(pct float64) string {
	switch {
	case pct >= 95:
		return "red"
	case pct >= 80:
		return "yellow"
	default:
		return "green"
	}
}

// warningIconColor picks an icon + color for the top-level warning line.
func warningIconColor(pct float64) (string, string) {
	switch {
	case pct >= 95:
		return "✗", "red"
	case pct >= 80:
		return "⚠", "yellow"
	case pct >= 50:
		return "ℹ", "cyan"
	default:
		return "ℹ", "green"
	}
}

// formatTokensShort renders a token count as e.g. "123", "4.5k", "1.2M".
func formatTokensShort(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
