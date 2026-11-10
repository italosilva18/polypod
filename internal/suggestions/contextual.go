package suggestions

import (
	"strings"
	"time"
)

// SuggestFollowUps offers short next actions from the actual conversation and
// project state. It does not call a model or replay the previous user prompt.
func SuggestFollowUps(lastUser, lastAssistant string, hints ContextHints) []Suggestion {
	user := strings.ToLower(strings.TrimSpace(lastUser))
	assistant := strings.ToLower(strings.TrimSpace(lastAssistant))
	items := make([]Suggestion, 0, 6)
	add := func(text string, score float64) {
		if ShouldFilter(text) {
			return
		}
		for _, existing := range items {
			if strings.EqualFold(existing.Text, text) {
				return
			}
		}
		items = append(items, Suggestion{Text: text, Source: SourceContext, Score: score, CreatedAt: time.Now()})
	}

	if containsAny(assistant, "erro", "falhou", "falha", "não consegui", "nao consegui") || len(hints.RecentErrors) > 0 {
		add("Investigue a causa da falha e tente novamente", 0.95)
	}
	if containsAny(user, "analise", "análise", "avalie", "audite", "verifique") {
		add("Implemente a melhoria prioritária da análise", 0.85)
		add("Mostre as evidências e os limites da análise", 0.75)
	}
	if containsAny(user, "compare", "comparação", "comparar", "diferença", "diferenças") {
		add("Organize as diferenças em uma tabela", 0.82)
	}
	if containsAny(user, "implemente", "implementa", "corrija", "conserte", "ajuste", "adicione") ||
		containsAny(assistant, "implementei", "corrigi", "adicionei", "atualizei") {
		add("Revise o diff e explique os riscos", 0.81)
		add("Liste o que ainda falta para concluir", 0.7)
	}
	switch hints.GitStatus {
	case "staged":
		add("Revise o diff preparado para commit", 0.8)
	case "modified":
		add("Mostre o diff das alterações atuais", 0.77)
	}
	if strings.TrimSpace(hints.CurrentTask) != "" {
		add("Verifique o progresso da tarefa atual", 0.68)
	}
	if len(items) == 0 {
		add("Resuma o progresso desta sessão", 0.55)
		add("Liste as próximas ações concretas", 0.5)
	}
	return RankSuggestions(items, 4)
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}
