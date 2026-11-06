package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPEmbeddingConfig describes an explicitly configured OpenAI-compatible
// embeddings endpoint. It is independent of the agent's chat provider.
type HTTPEmbeddingConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

type HTTPEmbedder struct {
	endpoint string
	apiKey   string
	model    string
	name     string
	client   *http.Client
}

// NewHTTPEmbedder never discovers credentials or an endpoint implicitly.
// API keys are only sent over HTTPS or to a loopback HTTP service.
func NewHTTPEmbedder(cfg HTTPEmbeddingConfig) (*HTTPEmbedder, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("modelo de embeddings obrigatório")
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("URL de embeddings inválida")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("endpoint remoto de embeddings exige HTTPS")
	}
	if !strings.HasSuffix(strings.TrimSuffix(parsed.Path, "/"), "/embeddings") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/embeddings"
	}
	endpoint := parsed.String()
	sum := sha256.Sum256([]byte(endpoint + "\x00" + cfg.Model))
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	privateClient := *client
	privateClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPEmbedder{endpoint: endpoint, apiKey: cfg.APIKey,
		model: cfg.Model, name: "http-" + hex.EncodeToString(sum[:12]), client: &privateClient}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Name is a non-secret identity for the endpoint/model pair. Persisted vectors
// are compared against this identity before semantic search is attempted.
func (e *HTTPEmbedder) Name() string { return e.name }

// Embed batches inputs and returns vectors in input order, even if the server
// returns the data array out of order. Invalid or partial responses fail closed.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > 10000 {
		return nil, errors.New("muitos textos para embeddings")
	}
	out := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += 32 {
		end := min(start+32, len(texts))
		batch, err := e.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (e *HTTPEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	for _, text := range texts {
		if strings.TrimSpace(text) == "" || len(text) > 128_000 {
			return nil, errors.New("texto de embedding vazio ou muito longo")
		}
	}
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("codificar requisição de embeddings: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("criar requisição de embeddings: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consultar embeddings: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
	var decoded struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("ler resposta de embeddings: %w", err)
	}
	if len(decoded.Data) != len(texts) {
		return nil, errors.New("resposta de embeddings incompleta")
	}
	vectors := make([][]float64, len(texts))
	dimension := 0
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(texts) || vectors[item.Index] != nil ||
			len(item.Embedding) == 0 || len(item.Embedding) > 8192 {
			return nil, errors.New("índice ou vetor de embedding inválido")
		}
		if dimension == 0 {
			dimension = len(item.Embedding)
		} else if len(item.Embedding) != dimension {
			return nil, errors.New("dimensões de embeddings inconsistentes")
		}
		var norm float64
		for _, value := range item.Embedding {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errors.New("embedding contém valor não finito")
			}
			norm += value * value
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, errors.New("embedding sem magnitude válida")
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}
