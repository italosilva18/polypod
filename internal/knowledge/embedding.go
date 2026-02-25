package knowledge

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// EmbeddingProvider generates vector embeddings for text.
type EmbeddingProvider struct {
	client *openai.Client
	model  openai.EmbeddingModel
}

// NewEmbeddingProvider creates an embedding provider.
// For DeepSeek, it uses the same client. For OpenAI embeddings, pass a separate config.
func NewEmbeddingProvider(apiKey, baseURL string) *EmbeddingProvider {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &EmbeddingProvider{
		client: openai.NewClientWithConfig(cfg),
		model:  openai.AdaEmbeddingV2,
	}
}

// Embed generates an embedding vector for the given text.
func (p *EmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := p.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: p.model,
	})
	if err != nil {
		return nil, fmt.Errorf("creating embedding: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return resp.Data[0].Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts.
func (p *EmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := p.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: texts,
		Model: p.model,
	})
	if err != nil {
		return nil, fmt.Errorf("creating batch embeddings: %w", err)
	}
	result := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		result[i] = d.Embedding
	}
	return result, nil
}
