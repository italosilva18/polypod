package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AzureConfig configures the Azure OpenAI provider.
type AzureConfig struct {
	Endpoint   string // e.g. https://mycompany.openai.azure.com
	Deployment string // deployment name (not model ID)
	APIKey     string
	APIVersion string // e.g. 2024-08-01-preview
	HTTPClient *http.Client
}

// AzureProvider implements a client for Azure OpenAI Service.
type AzureProvider struct {
	config AzureConfig
	client *http.Client
}

// NewAzureProvider creates an Azure OpenAI provider.
func NewAzureProvider(cfg AzureConfig) *AzureProvider {
	if cfg.APIVersion == "" {
		cfg.APIVersion = "2024-08-01-preview"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &AzureProvider{
		config: cfg,
		client: client,
	}
}

// Name returns the provider name.
func (p *AzureProvider) Name() string {
	return "azure"
}

// Models returns the list of common Azure OpenAI model deployments.
func (p *AzureProvider) Models() []string {
	return []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-4-turbo",
		"gpt-35-turbo",
		"o1-preview",
		"o1-mini",
	}
}

// AzureChatRequest is an OpenAI-compatible chat request.
type AzureChatRequest struct {
	Messages    []AzureMessage `json:"messages"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature float64        `json:"temperature,omitempty"`
	Stream      bool           `json:"stream,omitempty"`
	TopP        float64        `json:"top_p,omitempty"`
}

type AzureMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// AzureChatResponse is an OpenAI-compatible response.
type AzureChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int          `json:"index"`
		Message      AzureMessage `json:"message"`
		FinishReason string       `json:"finish_reason"`
	} `json:"choices"`
	Usage AzureUsage `json:"usage"`
}

type AzureUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AzureRequest is a simplified cross-provider request.
type AzureRequest struct {
	Deployment  string
	Messages    []AzureMessage
	MaxTokens   int
	Temperature float64
	Stream      bool
}

// AzureResponse is a simplified cross-provider response.
type AzureResponse struct {
	Content      string
	PromptTokens int
	OutputTokens int
	Model        string
	FinishReason string
}

// Complete sends a non-streaming completion request.
func (p *AzureProvider) Complete(ctx context.Context, req AzureRequest) (*AzureResponse, error) {
	deployment := req.Deployment
	if deployment == "" {
		deployment = p.config.Deployment
	}
	if deployment == "" {
		return nil, fmt.Errorf("deployment is required")
	}
	if p.config.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	body := AzureChatRequest{
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      false,
	}

	if body.MaxTokens == 0 {
		body.MaxTokens = 4096
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		p.config.Endpoint, deployment, p.config.APIVersion)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure error %d: %s", resp.StatusCode, string(respBody))
	}

	var ar AzureChatResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if len(ar.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	return &AzureResponse{
		Content:      ar.Choices[0].Message.Content,
		PromptTokens: ar.Usage.PromptTokens,
		OutputTokens: ar.Usage.CompletionTokens,
		Model:        ar.Model,
		FinishReason: ar.Choices[0].FinishReason,
	}, nil
}

// IsAvailable checks if the provider is configured.
func (p *AzureProvider) IsAvailable() bool {
	return p.config.Endpoint != "" && p.config.APIKey != "" && p.config.Deployment != ""
}

// BuildURL returns the full URL for a deployment endpoint.
func (p *AzureProvider) BuildURL(path string) string {
	return fmt.Sprintf("%s/openai/deployments/%s%s?api-version=%s",
		p.config.Endpoint, p.config.Deployment, path, p.config.APIVersion)
}

// AzureModelInfo returns information about an Azure model.
func AzureModelInfo(model string) map[string]any {
	info := map[string]map[string]any{
		"gpt-4o": {
			"context_tokens": 128000,
			"max_output":     16384,
			"input_cost":     0.0025,
			"output_cost":    0.01,
		},
		"gpt-4o-mini": {
			"context_tokens": 128000,
			"max_output":     16384,
			"input_cost":     0.00015,
			"output_cost":    0.0006,
		},
		"o1-preview": {
			"context_tokens": 128000,
			"max_output":     32768,
			"input_cost":     0.015,
			"output_cost":    0.06,
		},
	}
	if m, ok := info[model]; ok {
		return m
	}
	return map[string]any{}
}
