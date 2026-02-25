package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/database"
	"github.com/costa/polypod/internal/knowledge"
	"github.com/costa/polypod/internal/observability"
)

func main() {
	var (
		configPath string
		source     string
		title      string
		chunkSize  int
		overlap    int
	)

	flag.StringVar(&configPath, "config", "config.yaml", "path to config file")
	flag.StringVar(&source, "source", "", "path to document file to ingest")
	flag.StringVar(&title, "title", "", "document title (defaults to filename)")
	flag.IntVar(&chunkSize, "chunk-size", knowledge.DefaultChunkSize, "chunk size in characters")
	flag.IntVar(&overlap, "overlap", knowledge.DefaultChunkOverlap, "overlap between chunks")
	flag.Parse()

	if source == "" {
		fmt.Fprintf(os.Stderr, "usage: ingest --source <file> [--config config.yaml] [--title <title>]\n")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	logger := observability.NewLogger("info", "text")
	ctx := context.Background()

	db, err := database.New(ctx, cfg.Database.DSN(), cfg.Database.MaxConns, logger)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Read file
	data, err := os.ReadFile(source)
	if err != nil {
		logger.Error("failed to read file", "path", source, "error", err)
		os.Exit(1)
	}

	if title == "" {
		title = strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	}

	// Chunk
	chunks := knowledge.ChunkText(source, title, string(data), chunkSize, overlap)
	logger.Info("document chunked", "source", source, "chunks", len(chunks))

	// Embedding provider
	embedder := knowledge.NewEmbeddingProvider(cfg.AI.APIKey, cfg.AI.BaseURL)
	vs := knowledge.NewVectorSearch(db.Pool, embedder, logger)

	// Ingest each chunk
	for i, chunk := range chunks {
		embedding, err := embedder.Embed(ctx, chunk.Content)
		if err != nil {
			logger.Error("embedding failed", "chunk", i, "error", err)
			continue
		}

		if err := vs.Ingest(ctx, chunk, embedding); err != nil {
			logger.Error("ingest failed", "chunk", i, "error", err)
			continue
		}

		logger.Info("chunk ingested", "index", i, "len", len(chunk.Content))
	}

	logger.Info("ingestion complete",
		"source", source,
		"total_chunks", len(chunks),
	)
}
