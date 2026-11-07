package memory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

type cacheTestEmbedder struct {
	name       string
	offset     float64
	calls      int
	batchSizes []int
	fail       bool
}

func (f *cacheTestEmbedder) Name() string { return f.name }
func (f *cacheTestEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	f.calls++
	f.batchSizes = append(f.batchSizes, len(texts))
	if f.fail {
		return nil, errors.New("model unavailable")
	}
	vectors := make([][]float64, len(texts))
	for i, text := range texts {
		var sum int
		for _, b := range []byte(text) {
			sum += int(b)
		}
		vectors[i] = []float64{float64(sum) + f.offset, float64(len(text)) + 1}
	}
	return vectors, nil
}

func TestCachedEmbedderRestartOrderAndPrivateStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-cache")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "embedding-cache.db")
	inner := &cacheTestEmbedder{name: "model-a"}
	cache, err := NewCachedEmbedder(inner, path)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"private memory alpha", "private memory beta", "private memory alpha"}
	first, err := cache.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 || inner.batchSizes[0] != 2 || !reflect.DeepEqual(first[0], first[2]) || reflect.DeepEqual(first[0], first[1]) {
		t.Fatalf("wrong order or duplicate handling: vectors=%v, batches=%v", first, inner.batchSizes)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("cache directory mode: %v, %v", info, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache file mode: %v, %v", info, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private memory")) || bytes.Contains(raw, []byte("model-a")) {
		t.Fatal("plaintext text or model name found in cache database")
	}
	reopenedInner := &cacheTestEmbedder{name: "model-a", fail: true}
	reopened, err := NewCachedEmbedder(reopenedInner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, err := reopened.Embed(context.Background(), texts)
	if err != nil || reopenedInner.calls != 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("cache did not survive restart: vectors=%v, calls=%d, err=%v", second, reopenedInner.calls, err)
	}
}

func TestCachedEmbedderSeparatesModelsAndRetriesFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedding-cache.db")
	aInner := &cacheTestEmbedder{name: "model-a", fail: true}
	a, err := NewCachedEmbedder(aInner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if vectors, err := a.Embed(context.Background(), []string{"same text"}); err == nil || vectors != nil {
		t.Fatalf("failed model returned vectors=%v, err=%v", vectors, err)
	}
	aInner.fail = false
	aVectors, err := a.Embed(context.Background(), []string{"same text"})
	if err != nil || aInner.calls != 2 {
		t.Fatalf("failed embedding was cached: vectors=%v, calls=%d, err=%v", aVectors, aInner.calls, err)
	}
	aInner.fail = true
	if _, err := a.Embed(context.Background(), []string{"same text"}); err != nil || aInner.calls != 2 {
		t.Fatalf("successful result not cached: calls=%d, err=%v", aInner.calls, err)
	}
	bInner := &cacheTestEmbedder{name: "model-b", offset: 500}
	b, err := NewCachedEmbedder(bInner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	bVectors, err := b.Embed(context.Background(), []string{"same text"})
	if err != nil || bInner.calls != 1 || reflect.DeepEqual(aVectors, bVectors) || b.Name() != "model-b" {
		t.Fatalf("models shared vectors: a=%v, b=%v, calls=%d, err=%v", aVectors, bVectors, bInner.calls, err)
	}
}

func TestCachedEmbedderSupportsMoreThanTenThousandTextsInBoundedBatches(t *testing.T) {
	inner := &cacheTestEmbedder{name: "model-large"}
	cache, err := NewCachedEmbedder(inner, filepath.Join(t.TempDir(), "embedding-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	texts := make([]string, 10_005)
	for i := range texts {
		texts[i] = "entry-" + strconv.Itoa(i%65)
	}
	vectors, err := cache.Embed(context.Background(), texts)
	if err != nil || len(vectors) != len(texts) || !reflect.DeepEqual(vectors[0], vectors[65]) || inner.calls != 3 {
		t.Fatalf("large cache request failed: vectors=%d, model calls=%d, err=%v", len(vectors), inner.calls, err)
	}
	for _, size := range inner.batchSizes {
		if size > cachedEmbeddingBatchSize {
			t.Fatalf("inner batch exceeded %d: %d", cachedEmbeddingBatchSize, size)
		}
	}
}

func TestCachedEmbedderRequiresStableModelIdentity(t *testing.T) {
	if _, err := NewCachedEmbedder(&unnamedCacheTestEmbedder{}, filepath.Join(t.TempDir(), "cache.db")); err == nil {
		t.Fatal("unnamed embedder accepted")
	}
}

func TestCachedEmbedderRealModelSurvivesRestart(t *testing.T) {
	python := os.Getenv("POLYPOD_TEST_LOCAL_EMBEDDING_PYTHON")
	modelCache := os.Getenv("POLYPOD_TEST_LOCAL_EMBEDDING_CACHE")
	if python == "" || modelCache == "" {
		t.Skip("private FastEmbed model not configured for integration test")
	}
	inner, err := NewLocalEmbedder(LocalEmbeddingConfig{PythonBinary: python, CacheDir: modelCache})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "embedding-cache.db")
	cache, err := NewCachedEmbedder(inner, path)
	if err != nil {
		inner.Close()
		t.Fatal(err)
	}
	texts := []string{"O preço da gasolina aumentou.", "O gato dorme no sofá."}
	want, err := cache.Embed(context.Background(), texts)
	if err != nil || len(want) != 2 || len(want[0]) != localEmbeddingDimensions {
		cache.Close()
		inner.Close()
		t.Fatalf("real model failed: vectors=%d, err=%v", len(want), err)
	}
	modelName := inner.Name()
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inner.Close(); err != nil {
		t.Fatal(err)
	}
	// A deliberately unavailable model proves that reopen reads SQLite rather
	// than recomputing the same text.
	unavailable := &cacheTestEmbedder{name: modelName, fail: true}
	reopened, err := NewCachedEmbedder(unavailable, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Embed(context.Background(), texts)
	if err != nil || unavailable.calls != 0 || !reflect.DeepEqual(want, got) {
		t.Fatalf("real vectors not persisted: calls=%d, err=%v", unavailable.calls, err)
	}
}

type unnamedCacheTestEmbedder struct{}

func (*unnamedCacheTestEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	return nil, nil
}
