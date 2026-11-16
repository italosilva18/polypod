// Package rag — RAG (Retrieval-Augmented Generation) extracted from AICHAT
// Sources: aichat/src/rag/

package rag

import (
	"fmt"
	"strings"
)

// RAGConfig holds RAG configuration
type RAGConfig struct {
	IndexDir    string   `json:"index_dir"`
	ChunkSize   int      `json:"chunk_size"`
	TopK        int      `json:"top_k"`
	EmbeddingModel string `json:"embedding_model"`
}

// RAGIndex holds the document index
type RAGIndex struct {
	Documents []Document
	Embeddings []Embedding
}

// Document represents a document chunk
type Document struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Source   string `json:"source"`
	Metadata map[string]string `json:"metadata"`
}

// Embedding represents document embedding
type Embedding struct {
	DocID    string    `json:"doc_id"`
	Vector   []float64 `json:"vector"`
}

// RAGSystem implements RAG functionality
type RAGSystem struct {
	config RAGConfig
	index  *RAGIndex
}

// NewRAGSystem creates new RAG system
func NewRAGSystem(config RAGConfig) *RAGSystem {
	return &RAGSystem{
		config: config,
		index:  &RAGIndex{},
	}
}

// AddDocument adds a document to the index
func (r *RAGSystem) AddDocument(doc Document) error {
	// Generate embedding (placeholder - needs actual embedding model)
	embedding := generateEmbedding(doc.Content)
	
	r.index.Documents = append(r.index.Documents, doc)
	r.index.Embeddings = append(r.index.Embeddings, Embedding{
		DocID:  doc.ID,
		Vector: embedding,
	})
	
	return nil
}

// Search searches for relevant documents
func (r *RAGSystem) Search(query string, topK int) ([]Document, error) {
	// Generate query embedding
	queryEmbedding := generateEmbedding(query)
	
	// Find most similar documents
	scores := make([]float64, len(r.index.Documents))
	for i, emb := range r.index.Embeddings {
		scores[i] = cosineSimilarity(queryEmbedding, emb.Vector)
	}
	
	// Sort by score and return top K
	results := make([]Document, 0, topK)
	for i, score := range scores {
		if score > 0.5 && len(results) < topK {
			results = append(results, r.index.Documents[i])
		}
	}
	
	return results, nil
}

// GenerateEmbedding generates a simple embedding (placeholder)
func generateEmbedding(text string) []float64 {
	// Simple word-based embedding (placeholder)
	words := strings.Fields(text)
	vector := make([]float64, 100)
	
	for i, word := range words {
		if i >= 100 {
			break
		}
		vector[i] = float64(len(word)) / 10.0
	}
	
	return vector
}

// CosineSimilarity calculates cosine similarity between vectors
func cosineSimilarity(a, b []float64) float64 {
	dotProduct := 0.0
	normA := 0.0
	normB := 0.0
	
	for i := 0; i < len(a) && i < len(b); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	
	if normA == 0 || normB == 0 {
		return 0
	}
	
	return dotProduct / (normA * normB)
}

// FormatSearchResults formats search results
func FormatSearchResults(results []Document) string {
	var sb strings.Builder
	
	sb.WriteString("🔍 **RAG Search Results**:\n\n")
	
	for i, doc := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, doc.Source))
		sb.WriteString(fmt.Sprintf("   %s\n", truncate(doc.Content, 200)))
	}
	
	return sb.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
