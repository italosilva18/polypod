package memory

import (
	"context"
	"errors"
	"testing"
)

func TestFactTemporalUpdateReviewAndForget(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewManagedStore(NewFileStore(dir))
	first := Memory{Topic: "user_city", Content: "Moro em Recife", Source: "dream", Tags: []string{"journal:1"}}
	second := Memory{Topic: "user_city", Content: "Agora moro em Salvador", Source: "dream", Tags: []string{"journal:2"}}
	for _, m := range []Memory{first, second, second} {
		if err := store.SaveWithMeta(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	review, err := store.Review(ctx, "user_city")
	if err != nil {
		t.Fatal(err)
	}
	if len(review) != 2 {
		t.Fatalf("expected current plus one revision, got %d", len(review))
	}
	results, err := store.SearchRelevant(ctx, "Recife", 10, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatal("superseded city leaked into recall")
	}
	results, err = store.SearchRelevant(ctx, "Salvador", 10, 0.2)
	if err != nil || len(results) != 1 {
		t.Fatalf("current fact missing: %v %v", results, err)
	}
	if err := store.Delete(ctx, "user_city"); err != nil {
		t.Fatal(err)
	}
	store = NewManagedStore(NewFileStore(dir))
	if err := store.SaveWithMeta(ctx, second); err != nil {
		t.Fatal(err)
	}
	active, err := store.List(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("forgotten fact resurrected: %v %v", active, err)
	}
	review, err = store.Review(ctx, "user_city")
	if err != nil || len(review) != 1 || review[0].Content != "" {
		t.Fatalf("forgotten derived content retained: %v %v", review, err)
	}
	if err := store.Save(ctx, "user_city", "Moro em Manaus"); err != nil {
		t.Fatal(err)
	}
	active, err = store.List(ctx)
	if err != nil || len(active) != 1 {
		t.Fatal("explicit save should revoke suppression")
	}
}

type testEmbedder struct {
	vectors [][]float64
	err     error
}

func (e testEmbedder) Embed(context.Context, []string) ([][]float64, error) { return e.vectors, e.err }
func TestHybridRetrievalOptionalAndUnrelatedQuery(t *testing.T) {
	ctx := context.Background()
	store := NewManagedStore(NewFileStore(t.TempDir()))
	if err := store.Save(ctx, "preference", "Prefiro codigo na linguagem Go"); err != nil {
		t.Fatal(err)
	}
	noMatch, err := store.SearchRelevant(ctx, "jardinagem", 5, 0.2)
	if err != nil || len(noMatch) != 0 {
		t.Fatal("unrelated lexical query matched")
	}
	store.SetEmbedder(testEmbedder{vectors: [][]float64{{1, 0}, {0, 1}}})
	noMatch, err = store.SearchRelevant(ctx, "jardinagem", 5, 0.2)
	if err != nil || len(noMatch) != 0 {
		t.Fatal("unrelated semantic query matched")
	}
	store.SetEmbedder(testEmbedder{vectors: [][]float64{{1, 0}, {1, 0.1}}})
	semantic, err := store.SearchRelevant(ctx, "programming language choice", 5, 0.2)
	if err != nil || len(semantic) != 1 {
		t.Fatalf("semantic paraphrase missing: %v", err)
	}
	store.SetEmbedder(testEmbedder{err: errors.New("provider offline")})
	lexical, err := store.SearchRelevant(ctx, "codigo", 5, 0.2)
	if err != nil || len(lexical) != 1 {
		t.Fatal("embedding failure lost lexical fallback")
	}
}
