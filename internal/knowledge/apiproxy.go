package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// APIProxy proxies requests to the company's internal API.
type APIProxy struct {
	baseURL string
	apiKey  string
	client  *http.Client
	logger  *slog.Logger
}

// NewAPIProxy creates a new API proxy.
func NewAPIProxy(baseURL, apiKey string, logger *slog.Logger) *APIProxy {
	return &APIProxy{
		baseURL: baseURL,
		apiKey:  apiKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// Query fetches data from the internal API.
func (p *APIProxy) Query(ctx context.Context, endpoint string) (string, error) {
	url := fmt.Sprintf("%s/%s", p.baseURL, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	// Try to format JSON nicely
	var data interface{}
	if err := json.Unmarshal(body, &data); err == nil {
		formatted, _ := json.MarshalIndent(data, "", "  ")
		return string(formatted), nil
	}

	return string(body), nil
}
