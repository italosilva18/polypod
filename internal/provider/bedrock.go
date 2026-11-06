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

// BedrockConfig configures the Bedrock provider.
type BedrockConfig struct {
	Region       string
	Profile      string
	AccessKeyID  string
	SecretKey    string
	SessionToken string
	Model        string
	HTTPClient   *http.Client
}

// BedrockProvider implements a minimal AWS Bedrock client with SigV4 request signing.
type BedrockProvider struct {
	config BedrockConfig
	client *http.Client
}

// NewBedrockProvider creates a Bedrock provider.
func NewBedrockProvider(cfg BedrockConfig) *BedrockProvider {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Model == "" {
		cfg.Model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &BedrockProvider{
		config: cfg,
		client: client,
	}
}

// Name returns the provider name.
func (p *BedrockProvider) Name() string {
	return "bedrock"
}

// Models returns the list of supported Bedrock models.
func (p *BedrockProvider) Models() []string {
	return []string{
		"anthropic.claude-3-5-sonnet-20241022-v2:0",
		"anthropic.claude-3-5-haiku-20241022-v1:0",
		"anthropic.claude-3-opus-20240229-v1:0",
		"anthropic.claude-3-sonnet-20240229-v1:0",
		"anthropic.claude-3-haiku-20240307-v1:0",
		"meta.llama3-1-405b-instruct-v1:0",
		"meta.llama3-1-70b-instruct-v1:0",
		"mistral.mistral-large-2407-v1:0",
	}
}

// BedrockMessage matches Anthropic Messages API format.
type BedrockMessage struct {
	Role    string                `json:"role"`
	Content []BedrockContentBlock `json:"content"`
}

type BedrockContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// BedrockInvokeRequest is the request body for invoke-model.
type BedrockInvokeRequest struct {
	AnthropicVersion string           `json:"anthropic_version"`
	MaxTokens        int              `json:"max_tokens"`
	Messages         []BedrockMessage `json:"messages"`
	Temperature      float64          `json:"temperature,omitempty"`
	System           string           `json:"system,omitempty"`
}

// BedrockInvokeResponse is the response from invoke-model.
type BedrockInvokeResponse struct {
	ID      string                `json:"id"`
	Type    string                `json:"type"`
	Role    string                `json:"role"`
	Content []BedrockContentBlock `json:"content"`
	Model   string                `json:"model"`
	Usage   BedrockUsage          `json:"usage"`
}

type BedrockUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SimpleRequest is a simplified cross-provider request.
type SimpleRequest struct {
	Model       string
	System      string
	Messages    []SimpleMessage
	MaxTokens   int
	Temperature float64
}

type SimpleMessage struct {
	Role    string
	Content string
}

// SimpleResponse is a simplified cross-provider response.
type SimpleResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	StopReason   string
}

// Invoke sends a non-streaming request to Bedrock with SigV4-signed authentication.
func (p *BedrockProvider) Invoke(ctx context.Context, req SimpleRequest) (*SimpleResponse, error) {
	model := req.Model
	if model == "" {
		model = p.config.Model
	}

	messages := make([]BedrockMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role == "system" {
			continue // system goes in a separate field
		}
		messages = append(messages, BedrockMessage{
			Role: role,
			Content: []BedrockContentBlock{
				{Type: "text", Text: m.Content},
			},
		})
	}

	// Extract system message if present
	system := req.System
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		}
	}

	body := BedrockInvokeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        req.MaxTokens,
		Messages:         messages,
		Temperature:      req.Temperature,
		System:           system,
	}

	if body.MaxTokens == 0 {
		body.MaxTokens = 4096
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/invoke",
		p.config.Region, model)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// SigV4 sign the request using the configured AWS credentials.
	bodyReader := bytes.NewReader(data)
	httpReq.Body = io.NopCloser(bodyReader)
	payloadHash, err := hashPayload(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("hash payload: %w", err)
	}
	httpReq.Header.Set("X-Amz-Content-Sha256", payloadHash)

	if err := signRequest(httpReq, sigV4Credentials{
		AccessKeyID:     p.config.AccessKeyID,
		SecretAccessKey: p.config.SecretKey,
		SessionToken:    p.config.SessionToken,
		Region:          p.config.Region,
		Service:         "bedrock",
	}, payloadHash); err != nil {
		return nil, fmt.Errorf("sigv4 sign: %w", err)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bedrock error %d: %s", resp.StatusCode, string(respBody))
	}

	var br BedrockInvokeResponse
	if err := json.Unmarshal(respBody, &br); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	content := ""
	for _, c := range br.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	return &SimpleResponse{
		Content:      content,
		InputTokens:  br.Usage.InputTokens,
		OutputTokens: br.Usage.OutputTokens,
		Model:        br.Model,
		StopReason:   "end_turn",
	}, nil
}

// IsAvailable checks if the provider is configured.
func (p *BedrockProvider) IsAvailable() bool {
	return p.config.AccessKeyID != "" && p.config.SecretKey != ""
}

// BedrockModelInfo returns information about a model.
func BedrockModelInfo(modelID string) map[string]any {
	info := map[string]map[string]any{
		"anthropic.claude-3-5-sonnet-20241022-v2:0": {
			"context_tokens":  200000,
			"max_output":      8192,
			"supports_tools":  true,
			"supports_vision": true,
			"input_cost":      0.003,
			"output_cost":     0.015,
		},
		"anthropic.claude-3-5-haiku-20241022-v1:0": {
			"context_tokens":  200000,
			"max_output":      8192,
			"supports_tools":  true,
			"supports_vision": false,
			"input_cost":      0.001,
			"output_cost":     0.005,
		},
		"anthropic.claude-3-opus-20240229-v1:0": {
			"context_tokens":  200000,
			"max_output":      4096,
			"supports_tools":  true,
			"supports_vision": true,
			"input_cost":      0.015,
			"output_cost":     0.075,
		},
	}
	if m, ok := info[modelID]; ok {
		return m
	}
	return map[string]any{}
}
