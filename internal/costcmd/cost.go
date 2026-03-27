package costcmd

import (
	"fmt"
	"strings"
	"time"
)

// SessionStats holds statistics for the current session.
type SessionStats struct {
	StartedAt        time.Time
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	TotalCost        float64
	Requests         int
	ToolCalls        int
	Model            string
	LinesAdded       int
	LinesRemoved     int
}

// FormatCost renders /cost output.
func FormatCost(s SessionStats) string {
	var sb strings.Builder
	sb.WriteString("## Custo da sessao\n\n")

	duration := time.Since(s.StartedAt)
	fmt.Fprintf(&sb, "- **Modelo**: %s\n", s.Model)
	fmt.Fprintf(&sb, "- **Duracao**: %s\n", formatDuration(duration))
	fmt.Fprintf(&sb, "- **Requisicoes**: %d\n", s.Requests)
	fmt.Fprintf(&sb, "- **Tokens**: %d prompt + %d completion = **%d total**\n",
		s.PromptTokens, s.CompletionTokens, s.TotalTokens)
	fmt.Fprintf(&sb, "- **Custo**: **$%.4f USD**\n", s.TotalCost)

	if s.ToolCalls > 0 {
		fmt.Fprintf(&sb, "- **Tool calls**: %d\n", s.ToolCalls)
	}
	if s.LinesAdded > 0 || s.LinesRemoved > 0 {
		fmt.Fprintf(&sb, "- **Linhas**: +%d -%d\n", s.LinesAdded, s.LinesRemoved)
	}

	// Cost per request
	if s.Requests > 0 {
		fmt.Fprintf(&sb, "\n**Media**: $%.4f/req, %d tokens/req\n",
			s.TotalCost/float64(s.Requests),
			s.TotalTokens/s.Requests)
	}

	return sb.String()
}

// FormatStats renders /stats output with usage over time.
func FormatStats(dailyTokens map[string]int, dailyCost map[string]float64, totalSessions int) string {
	var sb strings.Builder
	sb.WriteString("## Estatisticas de uso\n\n")

	fmt.Fprintf(&sb, "**Total de sessoes**: %d\n\n", totalSessions)

	if len(dailyTokens) > 0 {
		sb.WriteString("### Ultimos 7 dias\n\n")
		sb.WriteString("| Data | Tokens | Custo |\n")
		sb.WriteString("|------|--------|-------|\n")

		// Show last 7 days
		for i := 6; i >= 0; i-- {
			date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
			tokens := dailyTokens[date]
			cost := dailyCost[date]
			bar := makeBar(tokens, 50000, 20)
			fmt.Fprintf(&sb, "| %s | %s %d | $%.4f |\n", date, bar, tokens, cost)
		}
	}

	return sb.String()
}

// FormatContext renders /context output showing context window breakdown.
func FormatContext(breakdown map[string]int, maxTokens int) string {
	var sb strings.Builder
	sb.WriteString("## Context Window\n\n")

	totalUsed := 0
	for _, v := range breakdown {
		totalUsed += v
	}

	pct := float64(totalUsed) / float64(maxTokens) * 100
	fmt.Fprintf(&sb, "**Uso**: %d / %d tokens (**%.0f%%**)\n\n", totalUsed, maxTokens, pct)

	// Progress bar
	barWidth := 40
	filled := int(pct / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	color := "\033[32m" // green
	if pct > 80 {
		color = "\033[31m" // red
	} else if pct > 50 {
		color = "\033[33m" // yellow
	}
	fmt.Fprintf(&sb, "%s%s\033[90m%s\033[0m %.0f%%\n\n",
		color, strings.Repeat("█", filled), strings.Repeat("░", barWidth-filled), pct)

	// Breakdown
	sb.WriteString("### Breakdown\n\n")
	order := []string{"system_prompt", "tools", "mcp_tools", "memory", "project_context", "history", "free"}
	names := map[string]string{
		"system_prompt":   "System prompt",
		"tools":           "Tool definitions",
		"mcp_tools":       "MCP tools",
		"memory":          "Memorias",
		"project_context": "Contexto do projeto",
		"history":         "Historico de mensagens",
		"free":            "Livre",
	}

	for _, key := range order {
		tokens, ok := breakdown[key]
		if !ok {
			continue
		}
		name := names[key]
		if name == "" {
			name = key
		}
		itemPct := float64(tokens) / float64(maxTokens) * 100
		fmt.Fprintf(&sb, "  %-25s %6d tokens  (%4.1f%%)\n", name, tokens, itemPct)
	}

	// Suggestions
	if pct > 80 {
		sb.WriteString("\n**Sugestoes**:\n")
		if breakdown["history"] > maxTokens/2 {
			sb.WriteString("- Use /compact para resumir o historico\n")
		}
		if breakdown["mcp_tools"] > 5000 {
			sb.WriteString("- Desconecte MCP servers nao utilizados\n")
		}
		if breakdown["memory"] > 3000 {
			sb.WriteString("- Limpe memorias antigas com delete_memory\n")
		}
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func makeBar(value, maxValue, width int) string {
	if maxValue <= 0 {
		return strings.Repeat("░", width)
	}
	filled := value * width / maxValue
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
