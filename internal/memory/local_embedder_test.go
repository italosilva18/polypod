package memory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const localTestWorker = `
import json, sys
print('{"ready":true}', flush=True)
for line in sys.stdin:
    texts = json.loads(line)["texts"]
    vectors = [[1.0, float(text)] + [0.0] * 382 for text in texts]
    print(json.dumps({"vectors": vectors}), flush=True)
`

func writeLocalTestSnapshot(t *testing.T, cache, revision, weights, tokenizer string) string {
	t.Helper()
	snapshot := filepath.Join(cache, localEmbeddingRepo, "snapshots", revision)
	if err := os.MkdirAll(snapshot, 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		localEmbeddingModelFile:   weights,
		"tokenizer.json":          tokenizer,
		"config.json":             "{}",
		"tokenizer_config.json":   "{}",
		"special_tokens_map.json": "{}",
	} {
		if err := os.WriteFile(filepath.Join(snapshot, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func setLocalTestRevision(t *testing.T, cache, revision string) {
	t.Helper()
	refs := filepath.Join(cache, localEmbeddingRepo, "refs")
	if err := os.MkdirAll(refs, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refs, "main"), []byte(revision), 0600); err != nil {
		t.Fatal(err)
	}
}

func newLocalTestEmbedder(t *testing.T, worker string) *LocalEmbedder {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	cache := t.TempDir()
	revision := strings.Repeat("a", 40)
	writeLocalTestSnapshot(t, cache, revision, "test weights", "{}")
	setLocalTestRevision(t, cache, revision)
	e, err := NewLocalEmbedder(LocalEmbeddingConfig{PythonBinary: python, CacheDir: cache, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	e.worker = worker
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestLocalEmbedderUsesActiveSnapshotAndAllArtifacts(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	cache := t.TempDir()
	oldRevision := strings.Repeat("a", 40)
	newRevision := strings.Repeat("b", 40)
	oldSnapshot := writeLocalTestSnapshot(t, cache, oldRevision, "old weights", "old tokenizer")
	newSnapshot := writeLocalTestSnapshot(t, cache, newRevision, "new weights", "new tokenizer")
	setLocalTestRevision(t, cache, newRevision)
	makeEmbedder := func() *LocalEmbedder {
		t.Helper()
		e, err := NewLocalEmbedder(LocalEmbeddingConfig{PythonBinary: python, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	active := makeEmbedder()
	if err := os.WriteFile(filepath.Join(oldSnapshot, localEmbeddingModelFile), []byte("changed old weights"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := makeEmbedder().Name(); got != active.Name() {
		t.Fatal("inactive snapshot changed model identity")
	}
	if err := os.WriteFile(filepath.Join(newSnapshot, "tokenizer.json"), []byte("changed tokenizer"), 0600); err != nil {
		t.Fatal(err)
	}
	withTokenizerChange := makeEmbedder()
	if withTokenizerChange.Name() == active.Name() {
		t.Fatal("active tokenizer change did not change model identity")
	}
	if err := os.WriteFile(filepath.Join(newSnapshot, "config.json"), []byte("{\"changed\":true}"), 0600); err != nil {
		t.Fatal(err)
	}
	if makeEmbedder().Name() == withTokenizerChange.Name() {
		t.Fatal("active config change did not change model identity")
	}
	setLocalTestRevision(t, cache, oldRevision)
	if makeEmbedder().Name() == withTokenizerChange.Name() {
		t.Fatal("switching active revision did not change model identity")
	}
}

func TestExpandLocalEmbeddingHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := expandLocalEmbeddingHome("~/embedding-venv/bin/python")
	if err != nil || got != filepath.Join(home, "embedding-venv/bin/python") {
		t.Fatalf("home expansion failed: %q, %v", got, err)
	}
	got, err = expandLocalEmbeddingHome("/tmp/model-cache")
	if err != nil || got != "/tmp/model-cache" {
		t.Fatal("absolute path changed")
	}
}

func TestLocalEmbedderKeepsOrderAndWorkerAcrossBatches(t *testing.T) {
	e := newLocalTestEmbedder(t, localTestWorker)
	texts := make([]string, 33)
	for i := range texts {
		texts[i] = strconv.Itoa(i + 1)
	}
	vectors, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 33 || vectors[0][1] != 1 || vectors[31][1] != 32 || vectors[32][1] != 33 {
		t.Fatal("batch order was lost")
	}
	pid := e.cmd.Process.Pid
	more, err := e.Embed(context.Background(), []string{"34"})
	if err != nil || len(more) != 1 || more[0][1] != 34 || e.cmd.Process.Pid != pid {
		t.Fatal("local worker was not reused")
	}
}

func TestLocalEmbedderInvalidResponsePreservesLexicalSearch(t *testing.T) {
	worker := `
import json, sys
print('{"ready":true}', flush=True)
for line in sys.stdin:
    print(json.dumps({"vectors": [[1.0] for _ in json.loads(line)["texts"]]}), flush=True)
`
	e := newLocalTestEmbedder(t, worker)
	store := NewManagedStore(NewFileStore(t.TempDir()))
	if err := store.Save(context.Background(), "linguagem", "Prefiro codigo em Go"); err != nil {
		t.Fatal(err)
	}
	store.SetEmbedder(e)
	results, err := store.SearchRelevant(context.Background(), "codigo", 5, 0.2)
	if err != nil || len(results) != 1 {
		t.Fatalf("lexical fallback missing after invalid local vector: %v", err)
	}
}

func TestLocalEmbedderDeadlineStopsWorkerAndCanRestart(t *testing.T) {
	worker := `
import json, sys, time
print('{"ready":true}', flush=True)
for line in sys.stdin:
    time.sleep(10)
`
	e := newLocalTestEmbedder(t, worker)
	e.timeout = 200 * time.Millisecond
	if _, err := e.Embed(context.Background(), []string{"1"}); err == nil {
		t.Fatal("worker timeout was not enforced")
	}
	if e.cmd != nil {
		t.Fatal("timed out worker remains running")
	}
	e.worker = localTestWorker
	e.timeout = 3 * time.Second
	vectors, err := e.Embed(context.Background(), []string{"1"})
	if err != nil || len(vectors) != 1 {
		t.Fatalf("worker did not restart: %v", err)
	}
}

func TestLocalEmbedderRealMultilingualModel(t *testing.T) {
	python := os.Getenv("POLYPOD_TEST_LOCAL_EMBEDDING_PYTHON")
	cache := os.Getenv("POLYPOD_TEST_LOCAL_EMBEDDING_CACHE")
	if python == "" || cache == "" {
		t.Skip("private FastEmbed model not configured for integration test")
	}
	e, err := NewLocalEmbedder(LocalEmbeddingConfig{PythonBinary: python, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !strings.HasPrefix(e.Name(), "fastembed-multilingual-minilm-") {
		t.Fatal("missing stable model identity")
	}
	vectors, err := e.Embed(context.Background(), []string{
		"O preço da gasolina aumentou.",
		"A gasolina ficou mais cara.",
		"O gato dorme no sofá.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 || len(vectors[0]) != localEmbeddingDimensions {
		t.Fatal("wrong embedding dimensions")
	}
	related, ok := cosine(vectors[0], vectors[1])
	if !ok {
		t.Fatal("invalid related similarity")
	}
	unrelated, ok := cosine(vectors[0], vectors[2])
	if !ok || related <= unrelated+0.1 {
		t.Fatalf("Portuguese semantic ranking failed: related=%.3f unrelated=%.3f", related, unrelated)
	}
	store := NewManagedStore(NewFileStore(t.TempDir()))
	if err := store.Save(context.Background(), "combustivel", "O preço da gasolina aumentou."); err != nil {
		t.Fatal(err)
	}
	store.SetEmbedder(e)
	results, err := store.SearchRelevant(context.Background(), "Fuel prices rose.", 5, 0.2)
	if err != nil || len(results) != 1 {
		t.Fatalf("cross-language semantic memory lookup failed: %v", err)
	}
}
