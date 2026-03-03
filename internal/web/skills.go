package web

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers web-related skills in the skill registry.
func RegisterSkills(reg *skill.Registry) {
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

	reg.Register(&skill.Skill{
		Name:        "web_search",
		Description: "Pesquisar na internet usando DuckDuckGo. Retorna titulo, URL e resumo dos top 5 resultados.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"query": {Type: jsonschema.String, Description: "Termos de busca (ex: clima em Sao Paulo)"},
			},
			Required: []string{"query"},
		},
		Execute: func(args map[string]string) (string, error) {
			query := args["query"]
			if query == "" {
				return "", fmt.Errorf("query e obrigatoria")
			}
			results, err := WebSearch(query)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "Nenhum resultado encontrado para: " + query, nil
			}
			var sb strings.Builder
			for i, r := range results {
				sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
			}
			return sb.String(), nil
		},
	})
}
