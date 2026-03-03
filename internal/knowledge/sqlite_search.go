package knowledge

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"sort"
)

// SQLiteVectorSearch performs semantic search with embeddings stored as BLOBs in SQLite.
// Cosine similarity is computed in Go.
type SQLiteVectorSearch struct {
	db       *sql.DB
	embedder *EmbeddingProvider
	logger   *slog.Logger
	topK     int
}

// NewSQLiteVectorSearch creates a new SQLite-backed vector search.
func NewSQLiteVectorSearch(db *sql.DB, embedder *EmbeddingProvider, logger *slog.Logger) *SQLiteVectorSearch {
	return &SQLiteVectorSearch{
		db:       db,
		embedder: embedder,
		logger:   logger,
		topK:     5,
	}
}

// Search finds the most relevant document fragments for a query.
func (s *SQLiteVectorSearch) Search(ctx context.Context, query string) ([]Fragment, error) {
	queryVec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT source, content, embedding FROM documents WHERE embedding IS NOT NULL",
	)
	if err != nil {
		return nil, fmt.Errorf("querying documents: %w", err)
	}
	defer rows.Close()

	var fragments []Fragment
	for rows.Next() {
		var source, content string
		var embBlob []byte
		if err := rows.Scan(&source, &content, &embBlob); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		docVec := decodeEmbedding(embBlob)
		if len(docVec) == 0 {
			continue
		}

		score := cosineSimilarity(queryVec, docVec)
		fragments = append(fragments, Fragment{
			Source:  source,
			Content: content,
			Score:   score,
		})
	}

	// Sort by score descending and return top-K
	sort.Slice(fragments, func(i, j int) bool {
		return fragments[i].Score > fragments[j].Score
	})

	if len(fragments) > s.topK {
		fragments = fragments[:s.topK]
	}

	s.logger.Debug("sqlite vector search completed", "query_len", len(query), "results", len(fragments))
	return fragments, nil
}

// Ingest stores a document chunk with its embedding.
func (s *SQLiteVectorSearch) Ingest(ctx context.Context, chunk Chunk, embedding []float32) error {
	blob := encodeEmbedding(embedding)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (source, title, chunk_index, content, embedding)
		VALUES (?, ?, ?, ?, ?)
	`, chunk.Source, chunk.Title, chunk.Index, chunk.Content, blob)
	if err != nil {
		return fmt.Errorf("inserting document: %w", err)
	}
	return nil
}

// encodeEmbedding converts a float32 slice to a binary blob (little-endian).
func encodeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeEmbedding converts a binary blob back to a float32 slice.
func decodeEmbedding(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
