package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	cachedEmbeddingBatchSize = 32
	cachedEmbeddingMaxTexts  = 100_000
	cachedEmbeddingMaxDims   = 8192
)

// CachedEmbedder stores only hashes and vectors in a private SQLite database.
// The inner embedder must expose a stable Name, so model changes cannot reuse
// vectors produced by a different model. Close does not close the inner embedder.
type CachedEmbedder struct {
	inner    Embedder
	name     string
	modelKey [32]byte
	db       *sql.DB
	gate     chan struct{}
	closed   bool // protected by gate
}

var _ Embedder = (*CachedEmbedder)(nil)

func NewCachedEmbedder(inner Embedder, path string) (*CachedEmbedder, error) {
	if inner == nil {
		return nil, errors.New("embedder interno obrigatório")
	}
	named, ok := inner.(interface{ Name() string })
	if !ok || strings.TrimSpace(named.Name()) == "" {
		return nil, errors.New("embedder sem identidade estável de modelo")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("caminho do cache de embeddings obrigatório")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolver cache de embeddings: %w", err)
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("criar diretório do cache de embeddings: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("diretório do cache de embeddings inválido")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("proteger diretório do cache de embeddings: %w", err)
	}
	if info, err := os.Lstat(abs); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("cache de embeddings deve ser arquivo comum, não link simbólico")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspecionar cache de embeddings: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("abrir cache de embeddings: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("fechar arquivo do cache de embeddings: %w", err)
	}
	if err := os.Chmod(abs, 0600); err != nil {
		return nil, fmt.Errorf("proteger arquivo do cache de embeddings: %w", err)
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("abrir SQLite do cache de embeddings: %w", err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=FULL`,
		`PRAGMA secure_delete=ON`,
		`CREATE TABLE IF NOT EXISTS embedding_cache (
			model_sha256 BLOB NOT NULL CHECK(length(model_sha256)=32),
			text_sha256 BLOB NOT NULL CHECK(length(text_sha256)=32),
			dimensions INTEGER NOT NULL CHECK(dimensions>0 AND dimensions<=8192),
			vector BLOB NOT NULL CHECK(length(vector)=dimensions*8),
			PRIMARY KEY(model_sha256,text_sha256)
		) WITHOUT ROWID`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("preparar cache de embeddings: %w", err)
		}
	}
	name := named.Name()
	return &CachedEmbedder{inner: inner, name: name, modelKey: sha256.Sum256([]byte(name)), db: db,
		gate: make(chan struct{}, 1)}, nil
}

// Name preserves the model identity expected by document and memory indexes.
func (e *CachedEmbedder) Name() string { return e.name }

// Embed preserves input order and splits arbitrarily large requests into small
// cache and model batches. A failed batch returns no partial output.
func (e *CachedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > cachedEmbeddingMaxTexts {
		return nil, errors.New("muitos textos para embeddings em cache")
	}
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len(text) > 128_000 {
			return nil, errors.New("texto de embedding vazio ou muito longo")
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case e.gate <- struct{}{}:
		defer func() { <-e.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e.closed {
		return nil, errors.New("cache de embeddings fechado")
	}
	vectors := make([][]float64, len(texts))
	dimensions := 0
	for start := 0; start < len(texts); start += cachedEmbeddingBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+cachedEmbeddingBatchSize, len(texts))
		batch, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		for i, vector := range batch {
			if dimensions == 0 {
				dimensions = len(vector)
			} else if len(vector) != dimensions {
				return nil, errors.New("dimensões inconsistentes no cache de embeddings")
			}
			vectors[start+i] = vector
		}
	}
	return vectors, nil
}

func (e *CachedEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	keys := make([][32]byte, len(texts))
	unique := make([][32]byte, 0, len(texts))
	seen := make(map[[32]byte]bool, len(texts))
	for i, text := range texts {
		key := sha256.Sum256([]byte(text))
		keys[i] = key
		if !seen[key] {
			unique = append(unique, key)
			seen[key] = true
		}
	}
	cached, err := e.readBatch(ctx, unique)
	if err != nil {
		return nil, err
	}
	missingKeys := make([][32]byte, 0, len(texts))
	missingTexts := make([]string, 0, len(texts))
	for i, key := range keys {
		if _, ok := cached[key]; !ok && !seenMissing(missingKeys, key) {
			missingKeys = append(missingKeys, key)
			missingTexts = append(missingTexts, texts[i])
		}
	}
	if len(missingTexts) > 0 {
		computed, err := e.inner.Embed(ctx, missingTexts)
		if err != nil {
			return nil, fmt.Errorf("calcular embeddings não armazenados: %w", err)
		}
		if len(computed) != len(missingTexts) {
			return nil, errors.New("embedder interno retornou lote incompleto")
		}
		dimensions := len(computed[0])
		for _, vector := range computed {
			if len(vector) != dimensions || !validCachedEmbeddingVector(vector) {
				return nil, errors.New("embedder interno retornou vetor inválido")
			}
		}
		if err := e.writeBatch(ctx, missingKeys, computed); err != nil {
			return nil, err
		}
		for i, key := range missingKeys {
			cached[key] = computed[i]
		}
	}
	result := make([][]float64, len(texts))
	for i, key := range keys {
		vector, ok := cached[key]
		if !ok {
			return nil, errors.New("embedding ausente após preenchimento do cache")
		}
		result[i] = append([]float64(nil), vector...)
	}
	return result, nil
}

func seenMissing(keys [][32]byte, key [32]byte) bool {
	for _, existing := range keys {
		if existing == key {
			return true
		}
	}
	return false
}

func (e *CachedEmbedder) readBatch(ctx context.Context, keys [][32]byte) (map[[32]byte][]float64, error) {
	result := make(map[[32]byte][]float64, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(keys)+1)
	args = append(args, e.modelKey[:])
	for i := range keys {
		args = append(args, keys[i][:])
	}
	query := `SELECT text_sha256,dimensions,vector FROM embedding_cache
		WHERE model_sha256=? AND text_sha256 IN (` + strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",") + `)`
	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("consultar cache de embeddings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rawKey, rawVector []byte
		var dimensions int
		if err := rows.Scan(&rawKey, &dimensions, &rawVector); err != nil {
			return nil, fmt.Errorf("ler vetor do cache de embeddings: %w", err)
		}
		if len(rawKey) != sha256.Size {
			continue
		}
		vector, ok := decodeCachedEmbeddingVector(rawVector, dimensions)
		if !ok {
			continue // recompute corrupt rows and replace them below
		}
		var key [32]byte
		copy(key[:], rawKey)
		result[key] = vector
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consultar cache de embeddings: %w", err)
	}
	return result, nil
}

func (e *CachedEmbedder) writeBatch(ctx context.Context, keys [][32]byte, vectors [][]float64) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciar gravação do cache de embeddings: %w", err)
	}
	defer tx.Rollback()
	for i, key := range keys {
		blob := encodeCachedEmbeddingVector(vectors[i])
		if _, err := tx.ExecContext(ctx, `INSERT INTO embedding_cache(model_sha256,text_sha256,dimensions,vector)
			VALUES(?,?,?,?) ON CONFLICT(model_sha256,text_sha256)
			DO UPDATE SET dimensions=excluded.dimensions,vector=excluded.vector`,
			e.modelKey[:], key[:], len(vectors[i]), blob); err != nil {
			return fmt.Errorf("gravar vetor no cache de embeddings: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirmar cache de embeddings: %w", err)
	}
	return nil
}

func validCachedEmbeddingVector(vector []float64) bool {
	if len(vector) == 0 || len(vector) > cachedEmbeddingMaxDims {
		return false
	}
	var norm float64
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
		norm += value * value
	}
	return norm > 0 && !math.IsInf(norm, 0)
}

func encodeCachedEmbeddingVector(vector []float64) []byte {
	blob := make([]byte, len(vector)*8)
	for i, value := range vector {
		binary.LittleEndian.PutUint64(blob[i*8:], math.Float64bits(value))
	}
	return blob
}

func decodeCachedEmbeddingVector(blob []byte, dimensions int) ([]float64, bool) {
	if dimensions <= 0 || dimensions > cachedEmbeddingMaxDims || len(blob) != dimensions*8 {
		return nil, false
	}
	vector := make([]float64, dimensions)
	for i := range vector {
		vector[i] = math.Float64frombits(binary.LittleEndian.Uint64(blob[i*8:]))
	}
	return vector, validCachedEmbeddingVector(vector)
}

// Close releases the cache database without closing the caller-owned embedder.
func (e *CachedEmbedder) Close() error {
	e.gate <- struct{}{}
	defer func() { <-e.gate }()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.db.Close()
}
