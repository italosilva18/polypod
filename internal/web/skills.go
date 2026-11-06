package web

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers web-related skills in the skill registry.
// Uses the default DuckDuckGo engine for search.
func RegisterSkills(reg *skill.Registry) {
	RegisterSkillsWithEngine(reg, &DuckDuckGoEngine{})
}

// RegisterSkillsWithEngine registers web-related skills using the given search engine.
func RegisterSkillsWithEngine(reg *skill.Registry, engine SearchEngine) {
	reg.Register(&skill.Skill{
		Name:        "fetch_url",
		Description: "Buscar e extrair o conteudo de texto de uma URL. Retorna o texto visivel da pagina.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"url": {Type: jsonschema.String, Description: "URL completa para buscar (ex: https://example.com)"},
			},
			Required: []string{"url"},
		},
		Execute: func(args map[string]string) (string, error) {
			u := args["url"]
			if u == "" {
				return "", fmt.Errorf("url e obrigatoria")
			}
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				u = "https://" + u
			}
			return FetchURL(u)
		},
	})

	reg.Register(newWebSearchSkill(engine))
}

// newWebSearchSkill creates a skill.Skill backed by the given SearchEngine.
func newWebSearchSkill(engine SearchEngine) *skill.Skill {
	ws := &WebSearchSkill{Engine: engine}
	return ws.AsSkill()
}

// AsSkill converts WebSearchSkill into a *skill.Skill for registration.
func (ws *WebSearchSkill) AsSkill() *skill.Skill {
	return &skill.Skill{
		Name:        "web_search",
		Description: "Pesquisar na internet. Retorna titulo, URL e resumo dos resultados.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"query":       {Type: jsonschema.String, Description: "Termos de busca (ex: clima em Sao Paulo)"},
				"max_results": {Type: jsonschema.String, Description: "Numero maximo de resultados (padrao: 5)"},
			},
			Required: []string{"query"},
		},
		Execute: func(args map[string]string) (string, error) {
			query := args["query"]
			if query == "" {
				return "", fmt.Errorf("query e obrigatoria")
			}

			maxResults := defaultMaxResults
			if v := args["max_results"]; v != "" {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					maxResults = n
				}
			}

			results, err := ws.Engine.Search(context.Background(), query, maxResults)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "Nenhum resultado encontrado para: " + query, nil
			}
			return FormatResults(results), nil
		},
	}
}

// RegisterSearchSkills registers the web_search skill backed by the given
// engine. The registry parameter accepts anything with a Register method
// (typically *skill.Registry).
func RegisterSearchSkills(reg interface{ Register(any) }, engine SearchEngine) {
	ws := &WebSearchSkill{Engine: engine}
	reg.Register(ws.AsSkill())
}

// FormatResults formats search results into a human-readable string.
func FormatResults(results []SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		pos := r.Position
		if pos == 0 {
			pos = i + 1
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", pos, r.Title, r.URL, r.Snippet))
	}
	return sb.String()
}
