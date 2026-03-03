package knowledge

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// vectorSearcher is the interface for semantic search backends.
type vectorSearcher interface {
	Search(ctx context.Context, query string) ([]Fragment, error)
	Ingest(ctx context.Context, chunk Chunk, embedding []float32) error
}

// VectorSearch performs semantic search using pgvector.
type VectorSearch struct {
	pool      *pgxpool.Pool
	embedder  *EmbeddingProvider
	logger    *slog.Logger
	topK      int
}

// Fragment is a search result from the knowledge base.
type Fragment struct {
	Source   string
	Content string
	Score   float64
}

// NewVectorSearch creates a new vector search service.
func NewVectorSearch(pool *pgxpool.Pool, embedder *EmbeddingProvider, logger *slog.Logger) *VectorSearch {
	return &VectorSearch{
		pool:     pool,
		embedder: embedder,
		logger:   logger,
		topK:     5,
	}
}

// Search finds the most relevant document fragments for a query.
func (vs *VectorSearch) Search(ctx context.Context, query string) ([]Fragment, error) {
	embedding, err := vs.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}

	vec := pgvector.NewVector(embedding)
	rows, err := vs.pool.Query(ctx, `
		SELECT source, content, 1 - (embedding <=> $1::vector) AS score
		FROM documents
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`, vec, vs.topK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var fragments []Fragment
	for rows.Next() {
		var f Fragment
		if err := rows.Scan(&f.Source, &f.Content, &f.Score); err != nil {
			return nil, fmt.Errorf("scanning result: %w", err)
		}
		fragments = append(fragments, f)
	}

	vs.logger.Debug("vector search completed", "query_len", len(query), "results", len(fragments))
	return fragments, nil
}

// Ingest stores a document chunk with its embedding.
func (vs *VectorSearch) Ingest(ctx context.Context, chunk Chunk, embedding []float32) error {
	vec := pgvector.NewVector(embedding)
	_, err := vs.pool.Exec(ctx, `
		INSERT INTO documents (source, title, chunk_index, content, embedding)
		VALUES ($1, $2, $3, $4, $5)
	`, chunk.Source, chunk.Title, chunk.Index, chunk.Content, vec)
	if err != nil {
		return fmt.Errorf("inserting document: %w", err)
	}
	return nil
}
