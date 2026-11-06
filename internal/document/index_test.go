package document

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTestIndex(t *testing.T) (*Index, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "documents.db")
	idx, err := OpenIndex(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	})
	return idx, path
}

func TestIndexFilePersistsUpdatesAndDeletes(t *testing.T) {
	ctx := context.Background()
	idx, indexPath := openTestIndex(t)
	if info, err := os.Stat(indexPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("index mode = %v, %v", info, err)
	}
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("A reunião de terça definiu a cor azul."), 0600); err != nil {
		t.Fatal(err)
	}
	doc, changed, err := idx.IndexFile(ctx, path, Options{})
	if err != nil || !changed || doc.Scope != LocalScope || doc.Chunks != 1 {
		t.Fatalf("first index = %+v, changed=%t, err=%v", doc, changed, err)
	}
	for _, name := range []string{indexPath, indexPath + "-wal", indexPath + "-shm"} {
		if info, err := os.Stat(name); err == nil && info.Mode().Perm()&0077 != 0 {
			t.Fatalf("index file has open permissions: %s %o", filepath.Base(name), info.Mode().Perm())
		}
	}
	if _, changed, err := idx.IndexFile(ctx, path, Options{}); err != nil || changed {
		t.Fatalf("unchanged source: changed=%t, err=%v", changed, err)
	}
	hits, err := idx.Search(ctx, "cor azul", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 || hits[0].SourceID != doc.SourceID || hits[0].Citation == "" {
		t.Fatalf("search: %+v, %v", hits, err)
	}
	firstCitation := hits[0].Citation
	if err := os.WriteFile(path, []byte("A reunião de terça definiu a cor verde."), 0600); err != nil {
		t.Fatal(err)
	}
	updated, changed, err := idx.IndexFile(ctx, path, Options{})
	if err != nil || !changed || updated.SourceHash == doc.SourceHash {
		t.Fatalf("update = %+v, changed=%t, err=%v", updated, changed, err)
	}
	hits, err = idx.Search(ctx, "cor azul", []string{LocalScope}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale chunk survived: %+v, %v", hits, err)
	}
	hits, err = idx.Search(ctx, "cor verde", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 || hits[0].Citation == firstCitation {
		t.Fatalf("new citation = %+v, %v", hits, err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	idx2, err := OpenIndex(indexPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer idx2.Close()
	hits, err = idx2.Search(ctx, "cor verde", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("after reopen: %+v, %v", hits, err)
	}
	removed, err := idx2.DeleteSource(ctx, LocalScope, updated.SourceID)
	if err != nil || !removed {
		t.Fatalf("delete changed=%t, err=%v", removed, err)
	}
	hits, err = idx2.Search(ctx, "cor verde", []string{LocalScope}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted chunk found: %+v, %v", hits, err)
	}
}

func TestIndexScopesFailClosedAndCitationsHideChat(t *testing.T) {
	ctx := context.Background()
	idx, _ := openTestIndex(t)
	chatA := "private-chat-A@example"
	chatB := "private-chat-B@example"
	for _, scope := range []string{chatA, chatB, LocalScope} {
		if _, _, err := idx.IndexText(ctx, scope, "attachment-1", "Relatório confidencial de vendas de março", ".pdf"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := idx.Search(ctx, "vendas", nil, 10); err == nil {
		t.Fatal("empty scope searched all documents")
	}
	hits, err := idx.Search(ctx, "vendas", []string{chatA}, 10)
	if err != nil || len(hits) != 1 || hits[0].Scope != chatA || strings.Contains(hits[0].Citation, chatA) {
		t.Fatalf("scope search = %+v, %v", hits, err)
	}
	read, err := idx.ReadChunk(ctx, chatA, "attachment-1", hits[0].Ordinal)
	if err != nil || read.Citation != hits[0].Citation {
		t.Fatalf("read cited chunk = %+v, %v", read, err)
	}
	if _, err := idx.ReadChunk(ctx, chatB, "unknown", 1); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("wrong-scope read err=%v", err)
	}
	hits, err = idx.Search(ctx, "vendas", []string{chatA, LocalScope}, 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("shared local scope = %+v, %v", hits, err)
	}
	status, err := idx.Status(ctx, chatB)
	if err != nil || status.Documents != 1 || status.Chunks != 1 {
		t.Fatalf("scope status = %+v, %v", status, err)
	}
	if _, err := idx.Status(ctx, ""); err == nil {
		t.Fatal("empty scope exposed status")
	}
}

func TestIndexRejectsInvalidReplacementWithoutLosingOldText(t *testing.T) {
	ctx := context.Background()
	idx, _ := openTestIndex(t)
	if _, _, err := idx.IndexText(ctx, "chat", "msg", "O protocolo é seguro", ".txt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.IndexText(ctx, "chat", "msg", strings.Repeat("x", MaxOutputBytes+1), ".txt"); err == nil {
		t.Fatal("oversized replacement accepted")
	}
	hits, err := idx.Search(ctx, "protocolo", []string{"chat"}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("old version lost: %+v, %v", hits, err)
	}
	if _, _, err := idx.IndexText(ctx, "", "msg", "texto", ".txt"); err == nil {
		t.Fatal("empty scope accepted")
	}
	if _, _, err := idx.IndexText(ctx, "chat", "", "texto", ".txt"); err == nil {
		t.Fatal("empty source ID accepted")
	}
}

type indexEmbedder struct {
	fail bool
	name string
}

func (e indexEmbedder) Name() string {
	if e.name == "" {
		return "test-v1"
	}
	return e.name
}

func (e indexEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	if e.fail {
		return nil, errors.New("provider unavailable")
	}
	out := make([][]float64, len(texts))
	for i, text := range texts {
		switch {
		case strings.Contains(strings.ToLower(text), "cachorro") || strings.Contains(strings.ToLower(text), "canino"):
			out[i] = []float64{1, 0}
		default:
			out[i] = []float64{0, 1}
		}
	}
	return out, nil
}

func TestIndexSemanticSearchAndLexicalFallback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	idx, err := OpenIndex(path, indexEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.IndexText(ctx, LocalScope, "animals", "O cachorro dormiu no sofá", ".txt"); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(ctx, "canino", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 || hits[0].SourceID != "animals" {
		t.Fatalf("semantic search = %+v, %v", hits, err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	degraded, err := OpenIndex(path, indexEmbedder{fail: true})
	if err != nil {
		t.Fatal(err)
	}
	defer degraded.Close()
	hits, err = degraded.Search(ctx, "cachorro", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("lexical fallback = %+v, %v", hits, err)
	}
}

func TestIndexDoesNotMixEmbeddingModels(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	first, err := OpenIndex(path, indexEmbedder{name: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.IndexText(ctx, LocalScope, "animals", "O cachorro dormiu no sofá", ".txt"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenIndex(path, indexEmbedder{name: "model-b"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	hits, err := second.Search(ctx, "canino", []string{LocalScope}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("old model vector was used: %+v, %v", hits, err)
	}
	if _, changed, err := second.IndexText(ctx, LocalScope, "animals", "O cachorro dormiu no sofá", ".txt"); err != nil || !changed {
		t.Fatalf("model reindex changed=%t, err=%v", changed, err)
	}
	hits, err = second.Search(ctx, "canino", []string{LocalScope}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("new model vector missing: %+v, %v", hits, err)
	}
}
