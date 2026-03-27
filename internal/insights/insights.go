package insights

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SessionData holds data for analysis.
type SessionData struct {
	Duration     time.Duration
	Requests     int
	Tokens       int
	Cost         float64
	ToolCalls    map[string]int // tool name → count
	ErrorCount   int
	TopFiles     map[string]int // file → edit count
}

// GenerateInsightsPrompt creates an AI prompt to analyze session data.
func GenerateInsightsPrompt(data SessionData) string {
	var sb strings.Builder
	sb.WriteString("Analise os dados da sessao de uso do Polypod e gere insights uteis.\n\n")

	sb.WriteString("## Dados da sessao\n\n")
	fmt.Fprintf(&sb, "- Duracao: %s\n", formatDuration(data.Duration))
	fmt.Fprintf(&sb, "- Requisicoes: %d\n", data.Requests)
	fmt.Fprintf(&sb, "- Tokens: %d\n", data.Tokens)
	fmt.Fprintf(&sb, "- Custo: $%.4f\n", data.Cost)
	fmt.Fprintf(&sb, "- Erros: %d\n", data.ErrorCount)

	if len(data.ToolCalls) > 0 {
		sb.WriteString("\n### Tools mais usadas\n")
		type tc struct {
			name  string
			count int
		}
		var sorted []tc
		for n, c := range data.ToolCalls {
			sorted = append(sorted, tc{n, c})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
		for _, t := range sorted {
			fmt.Fprintf(&sb, "- %s: %d chamadas\n", t.name, t.count)
		}
	}

	if len(data.TopFiles) > 0 {
		sb.WriteString("\n### Arquivos mais editados\n")
		type fe struct {
			path  string
			count int
		}
		var sorted []fe
		for p, c := range data.TopFiles {
			sorted = append(sorted, fe{p, c})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
		for i, f := range sorted {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&sb, "- %s: %d edicoes\n", f.path, f.count)
		}
	}

	sb.WriteString("\n## Gere insights sobre:\n")
	sb.WriteString("1. Padroes de uso (quais tools sao mais/menos usadas)\n")
	sb.WriteString("2. Eficiencia (tokens por requisicao, custo por acao)\n")
	sb.WriteString("3. Pontos de friccao (erros frequentes, retries)\n")
	sb.WriteString("4. Sugestoes de otimizacao (skills subutilizadas, workflows melhores)\n")
	sb.WriteString("5. Resumo executivo da sessao\n")

	return sb.String()
}

// FormatQuickInsights generates non-AI insights from data alone.
func FormatQuickInsights(data SessionData) string {
	var sb strings.Builder
	sb.WriteString("## Insights da sessao\n\n")

	// Efficiency
	if data.Requests > 0 {
		avgTokens := data.Tokens / data.Requests
		avgCost := data.Cost / float64(data.Requests)
		fmt.Fprintf(&sb, "**Eficiencia**: %d tokens/req, $%.4f/req\n\n", avgTokens, avgCost)
	}

	// Error rate
	if data.Requests > 0 {
		errorRate := float64(data.ErrorCount) / float64(data.Requests) * 100
		if errorRate > 10 {
			fmt.Fprintf(&sb, "**Atencao**: taxa de erro alta (%.0f%%)\n\n", errorRate)
		}
	}

	// Tool usage analysis
	totalCalls := 0
	for _, c := range data.ToolCalls {
		totalCalls += c
	}
	if totalCalls > 0 {
		readCalls := data.ToolCalls["read_file"] + data.ToolCalls["read_files"] + data.ToolCalls["list_directory"]
		writeCalls := data.ToolCalls["edit_file"] + data.ToolCalls["create_file"]
		fmt.Fprintf(&sb, "**Ratio read/write**: %d leituras / %d escritas\n", readCalls, writeCalls)
	}

	// Duration analysis
	if data.Duration > 30*time.Minute {
		sb.WriteString("\n**Dica**: sessao longa — considere usar /compact para liberar contexto\n")
	}

	return sb.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
