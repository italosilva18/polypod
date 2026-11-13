package codesearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type conceptEmbedder struct{}

func (conceptEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "sign in") || strings.Contains(text, "AuthenticateUser") {
			result[i] = []float64{1, 0}
		} else {
			result[i] = []float64{0, 1}
		}
	}
	return result, nil
}

func TestSearchContextRespectsBudgetAndShowsEvidence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "authentication.go", "package p\nfunc AuthenticateUser(password string) bool { return password != \"\" }\n")
	for _, name := range []string{"database.go", "settings.go", "queue.go", "server.go", "client.go", "cache.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\nfunc Unrelated() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	results, err := New(conceptEmbedder{}).SearchContext(context.Background(), root, "who may sign in", ContextOptions{Limit: 5, TokenBudget: 110, Semantic: true, Ranking: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Path != "authentication.go" {
		t.Fatalf("relevant file missing: %+v", results)
	}
	used := 0
	for _, result := range results {
		used += result.EstimatedTokens
	}
	if used > 110 {
		t.Fatalf("token budget exceeded: %d", used)
	}
	if !strings.Contains(FormatContext(results), "semântico") || results[0].StartLine < 1 {
		t.Fatalf("evidence missing: %+v", results)
	}
}

func TestSearchContextLexicalOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "parser.go", "package p\nfunc ParseInvoiceAmount(raw string) int { return len(raw) }\n")
	results, err := (*Engine)(nil).SearchContext(context.Background(), root, "ParseInvoiceAmount", ContextOptions{Limit: 2, TokenBudget: 100, Semantic: false, Ranking: "hybrid"})
	if err != nil || len(results) != 1 || results[0].Path != "parser.go" {
		t.Fatalf("lexical context: %+v, %v", results, err)
	}
	if !strings.Contains(strings.Join(results[0].Reasons, ","), "símbolo") {
		t.Fatalf("symbol evidence missing: %+v", results[0])
	}
}

func TestSearchContextDefaultsToHybridWithoutEmbeddings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "parser.go", "package p\nfunc ParseInvoiceAmount(raw string) int { return len(raw) }\n")
	results, err := (*Engine)(nil).SearchContext(context.Background(), root, "ParseInvoiceAmount", ContextOptions{TokenBudget: 100})
	if err != nil || len(results) != 1 || results[0].Path != "parser.go" ||
		!strings.Contains(strings.Join(results[0].Reasons, ","), "lexical") ||
		!strings.Contains(strings.Join(results[0].Reasons, ","), "mapa") ||
		strings.Contains(strings.Join(results[0].Reasons, ","), "semântico") {
		t.Fatalf("default should use hybrid without requiring embeddings: %+v, %v", results, err)
	}
}

func TestSearchContextExplicitLexicalOmitsMapAndSemantic(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "parser.go", "package p\nfunc ParseInvoiceAmount(raw string) int { return len(raw) }\n")
	results, err := (*Engine)(nil).SearchContext(context.Background(), root, "ParseInvoiceAmount", ContextOptions{TokenBudget: 100, Ranking: "lexical"})
	if err != nil || len(results) != 1 || results[0].Reasons[0] != "lexical" ||
		strings.Contains(strings.Join(results[0].Reasons, ","), "semântico") ||
		strings.Contains(strings.Join(results[0].Reasons, ","), "mapa") {
		t.Fatalf("explicit lexical should only report lexical reason: %+v, %v", results, err)
	}
}

func TestSearchContextFairSlotBudgetFitsTopFiveDistinctFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	body := func(sym string) string {
		return "package p\nfunc " + sym + "() {\n" + strings.Repeat("\t// lookup context agent tool filler line for chunk budget test\n", 75) + "}\n"
	}
	root := prepareSearchProject(t, "f1.go", body("AlphaTool"))
	for _, item := range []struct{ name, sym string }{
		{"f2.go", "BetaTool"},
		{"f3.go", "GammaTool"},
		{"f4.go", "DeltaTool"},
		{"f5_target.go", "ExposeLookupCallable"},
	} {
		if err := os.WriteFile(filepath.Join(root, item.name), []byte(body(item.sym)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	results, err := (*Engine)(nil).SearchContext(context.Background(), root, "lookup context agent tool", ContextOptions{Limit: 5, TokenBudget: 2000, Ranking: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 distinct files within 2000 token budget, got %d: %+v", len(results), results)
	}
	used := 0
	for _, r := range results {
		used += r.EstimatedTokens
	}
	if used > 2000 {
		t.Fatalf("token budget exceeded: %d", used)
	}
}

func TestSearchContextCoversDistinctFilesBeforeRepeatingChunks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "long.go", "package p\n"+strings.Repeat("func invoiceWorker() {}\n", 400))
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package p\nfunc Invoice() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := (*Engine)(nil).SearchContext(context.Background(), root, "invoice", ContextOptions{Limit: 5, TokenBudget: 1000, Ranking: "lexical"})
	if err != nil || len(results) < 2 || results[0].Path != "long.go" || results[1].Path != "target.go" {
		t.Fatalf("one long file crowded out relevant second file: %+v, %v", results, err)
	}
}

func TestSearchContextAblationSeparatesSemanticFromLexical(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := prepareSearchProject(t, "authentication.go", "package p\nfunc AuthenticateUser(password string) bool { return password != \"\" }\n")
	engine := New(conceptEmbedder{})
	query := "who may sign in"
	lexical, err := engine.SearchContext(context.Background(), root, query, ContextOptions{Ranking: "lexical", TokenBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := engine.SearchContext(context.Background(), root, query, ContextOptions{Ranking: "semantic", TokenBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical) != 0 || len(semantic) != 1 || semantic[0].Path != "authentication.go" {
		t.Fatalf("ablation lexical=%+v semantic=%+v", lexical, semantic)
	}
	if _, err := (*Engine)(nil).SearchContext(context.Background(), root, query, ContextOptions{Ranking: "semantic"}); err == nil {
		t.Fatal("semantic mode silently ran without embeddings")
	}
}
