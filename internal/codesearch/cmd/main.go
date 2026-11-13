// polypod-context-eval measures retrieval variants against a fixed local fixture.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/costa/polypod/internal/codesearch"
	"github.com/costa/polypod/internal/memory"
)

type retrievalCase struct {
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
}

type fixture struct {
	Cases []retrievalCase `json:"cases"`
}

type result struct {
	Ranking       string   `json:"ranking"`
	Queries       int      `json:"queries"`
	Hits          int      `json:"hits"`
	RecallAt5     float64  `json:"recall_at_5"`
	Tokens        int      `json:"estimated_tokens"`
	MissedQueries []string `json:"missed_queries,omitempty"`
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := flag.String("root", "", "Diretório Git com índice de código ativado")
	fixturePath := flag.String("fixture", "", "JSON com cases: query e relevant (caminhos relativos)")
	model := flag.String("model", "", "Modelo local de embeddings; vazio executa apenas lexical/mapa")
	budget := flag.Int("token-budget", 2000, "Orçamento estimado de tokens por consulta")
	enableIndex := flag.Bool("enable-index", false, "Ativar o índice neste diretório antes da avaliação")
	flag.Parse()
	if *root == "" || *fixturePath == "" || *budget < 64 || *budget > 16000 {
		return fmt.Errorf("informe -root, -fixture e -token-budget entre 64 e 16000")
	}
	if *enableIndex {
		if err := codesearch.SetEnabled(*root, true); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(*fixturePath)
	if err != nil {
		return err
	}
	var set fixture
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil || len(set.Cases) == 0 {
		return fmt.Errorf("fixture inválida: %v", err)
	}
	for _, item := range set.Cases {
		if strings.TrimSpace(item.Query) == "" || len(item.Relevant) == 0 {
			return fmt.Errorf("consulta sem texto ou caminhos relevantes")
		}
		for _, path := range item.Relevant {
			if !filepath.IsLocal(path) {
				return fmt.Errorf("caminho relevante inválido: %q", path)
			}
		}
	}
	var engine *codesearch.Engine
	modes := []string{"lexical", "map"}
	if *model != "" {
		embedder, err := memory.NewHTTPEmbedder(memory.HTTPEmbeddingConfig{
			BaseURL: "http://127.0.0.1:11434/v1", Model: *model, Client: &http.Client{Timeout: 3 * time.Minute},
		})
		if err != nil {
			return err
		}
		engine = codesearch.New(embedder)
		modes = append(modes, "semantic", "hybrid")
	}
	results := make([]result, 0, len(modes))
	for _, mode := range modes {
		entry := result{Ranking: mode, Queries: len(set.Cases)}
		for _, item := range set.Cases {
			matches, err := engine.SearchContext(ctx, *root, item.Query, codesearch.ContextOptions{
				Limit: 5, TokenBudget: *budget, Ranking: mode, Semantic: *model != "",
			})
			if err != nil {
				return fmt.Errorf("%s / %q: %w", mode, item.Query, err)
			}
			hit := false
			for _, match := range matches {
				entry.Tokens += match.EstimatedTokens
				if slices.Contains(item.Relevant, match.Path) {
					hit = true
				}
			}
			if hit {
				entry.Hits++
			} else {
				entry.MissedQueries = append(entry.MissedQueries, item.Query)
			}
		}
		entry.RecallAt5 = float64(entry.Hits) / float64(entry.Queries)
		results = append(results, entry)
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}
