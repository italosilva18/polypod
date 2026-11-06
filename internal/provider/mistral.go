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

// MistralConfig configures the Mistral provider.
type MistralConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// MistralProvider implements a native Mistral API client.
type MistralProvider struct {
	config MistralConfig
	client *http.Client
}

// NewMistralProvider creates a Mistral provider.
func NewMistralProvider(cfg MistralConfig) *MistralProvider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.mistral.ai/v1"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &MistralProvider{
		config: cfg,
		client: client,
	}
}

// Name returns the provider name.
func (p *MistralProvider) Name() string {
	return "mistral"
}

// Models returns the list of available Mistral models.
func (p *MistralProvider) Models() []string {
	return []string{
		"mistral-large-latest",
		"mistral-large-2411",
		"mistral-small-latest",
		"mistral-nemo",
		"pixtral-large-latest",
		"pixtral-12b-2409",
		"ministral-8b-latest",
		"ministral-3b-latest",
		"codestral-latest",
		"codestral-2405",
		"mixtral-8x22b",
		"mixtral-8x7b",
		"open-mistral-nemo",
	}
}

// MistralMessage is a single chat message.
type MistralMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Name      string            `json:"name,omitempty"`
	ToolCalls []MistralToolCall `json:"tool_calls,omitempty"`
}

// MistralToolCall describes a tool invocation.
type MistralToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function MistralFunctionCall `json:"function"`
}

type MistralFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// MistralTool describes a tool available to the model.
type MistralTool struct {
	Type     string             `json:"type"`
	Function MistralFunctionDef `json:"function"`
}

type MistralFunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// MistralChatRequest is a chat completion request.
type MistralChatRequest struct {
	Model          string                 `json:"model"`
	Messages       []MistralMessage       `json:"messages"`
	Temperature    float64                `json:"temperature,omitempty"`
	TopP           float64                `json:"top_p,omitempty"`
	MaxTokens      int                    `json:"max_tokens,omitempty"`
	Stream         bool                   `json:"stream,omitempty"`
	ResponseFormat *MistralResponseFormat `json:"response_format,omitempty"`
	Tools          []MistralTool          `json:"tools,omitempty"`
	ToolChoice     string                 `json:"tool_choice,omitempty"`
}

// MistralResponseFormat enables JSON mode.
type MistralResponseFormat struct {
	Type string `json:"type"`
}

// MistralChatResponse is a chat completion response.
type MistralChatResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []MistralChoice `json:"choices"`
	Usage   MistralUsage    `json:"usage"`
}

// MistralChoice is a response choice.
type MistralChoice struct {
	Index        int            `json:"index"`
	Message      MistralMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// MistralUsage tracks token usage.
type MistralUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Complete sends a chat completion request.
func (p *MistralProvider) Complete(ctx context.Context, req MistralChatRequest) (*MistralChatResponse, error) {
	if req.Model == "" {
		req.Model = "mistral-large-latest"
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := p.config.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mistral error %d: %s", resp.StatusCode, string(respBody))
	}

	var mr MistralChatResponse
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return &mr, nil
}

// SimpleComplete is a simplified interface for basic completion.
func (p *MistralProvider) SimpleComplete(ctx context.Context, model string, messages []MistralMessage, maxTokens int) (string, error) {
	req := MistralChatRequest{
		Model:     model,
		Messages:  messages,
		MaxTokens: maxTokens,
	}
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

// IsAvailable checks if the provider is configured.
func (p *MistralProvider) IsAvailable() bool {
	return p.config.APIKey != ""
}

// MistralModelInfo returns information about a Mistral model.
func MistralModelInfo(model string) map[string]any {
	info := map[string]map[string]any{
		"mistral-large-latest": {
			"context_tokens": 128000,
			"max_output":     4096,
			"supports_tools": true,
			"input_cost":     0.002,
			"output_cost":    0.006,
		},
		"mistral-small-latest": {
			"context_tokens": 32000,
			"max_output":     4096,
			"supports_tools": true,
			"input_cost":     0.001,
			"output_cost":    0.003,
		},
		"codestral-latest": {
			"context_tokens": 32000,
			"max_output":     4096,
			"supports_tools": false,
			"input_cost":     0.0003,
			"output_cost":    0.0009,
		},
		"ministral-8b-latest": {
			"context_tokens": 128000,
			"max_output":     4096,
			"supports_tools": true,
			"input_cost":     0.0001,
			"output_cost":    0.0001,
		},
	}
	if m, ok := info[model]; ok {
		return m
	}
	return map[string]any{}
}
