package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic provides native access to Claude models.
type Anthropic struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewAnthropic creates an Anthropic provider.
func NewAnthropic(apiKey, baseURL string) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &Anthropic{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (a *Anthropic) Name() string                  { return "anthropic" }
func (a *Anthropic) SupportsVision() bool           { return true }
func (a *Anthropic) SupportsTools() bool            { return true }
func (a *Anthropic) SupportsStructuredOutput() bool { return true }

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMsg     `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMsg struct {
	Role    string        `json:"role"`
	Content any           `json:"content"`
}

type anthropicContentBlock struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Input  any    `json:"input,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResp struct {
	ID           string                  `json:"id"`
	Content      []anthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason"`
	Usage        anthropicUsage          `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (a *Anthropic) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := a.buildRequest(req, false)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	a.setHeaders(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, string(b))
	}

	var aResp anthropicResp
	if err := json.NewDecoder(resp.Body).Decode(&aResp); err != nil {
		return nil, fmt.Errorf("anthropic decode: %w", err)
	}

	return a.parseResponse(aResp), nil
}

func (a *Anthropic) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	body := a.buildRequest(req, true)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	a.setHeaders(httpReq)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic HTTP %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var content strings.Builder
	var toolCalls []ToolCall
	var usage anthropicUsage
	var currentToolID, currentToolName string
	var currentToolArgs strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		eventData := strings.TrimPrefix(line, "data: ")
		if eventData == "[DONE]" {
			break
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(eventData), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)
		switch eventType {
		case "content_block_start":
			if cb, ok := event["content_block"].(map[string]any); ok {
				if cb["type"] == "tool_use" {
					currentToolID, _ = cb["id"].(string)
					currentToolName, _ = cb["name"].(string)
					currentToolArgs.Reset()
				}
			}
		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]any); ok {
				dtype, _ := delta["type"].(string)
				switch dtype {
				case "text_delta":
					text, _ := delta["text"].(string)
					content.WriteString(text)
					if callback != nil {
						callback(StreamDelta{Content: text})
					}
				case "input_json_delta":
					partial, _ := delta["partial_json"].(string)
					currentToolArgs.WriteString(partial)
				}
			}
		case "content_block_stop":
			if currentToolName != "" {
				toolCalls = append(toolCalls, ToolCall{
					ID:        currentToolID,
					Name:      currentToolName,
					Arguments: currentToolArgs.String(),
				})
				currentToolName = ""
			}
		case "message_delta":
			if u, ok := event["usage"].(map[string]any); ok {
				if v, ok := u["output_tokens"].(float64); ok {
					usage.OutputTokens = int(v)
				}
			}
		}
	}

	if callback != nil {
		callback(StreamDelta{Done: true})
	}

	return &ChatResponse{
		Content:          content.String(),
		ToolCalls:        toolCalls,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.InputTokens + usage.OutputTokens,
	}, nil
}

func (a *Anthropic) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (a *Anthropic) buildRequest(req ChatRequest, stream bool) anthropicReq {
	aReq := anthropicReq{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}
	if aReq.MaxTokens == 0 {
		aReq.MaxTokens = 4096
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			aReq.System = m.Content
			continue
		}

		role := m.Role
		if role == "tool" {
			// Anthropic expects tool results as user messages with tool_result content blocks
			aReq.Messages = append(aReq.Messages, anthropicMsg{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
			continue
		}

		if len(m.Parts) > 0 {
			var blocks []anthropicContentBlock
			for _, p := range m.Parts {
				switch p.Type {
				case "text":
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: p.Text})
				case "image_base64":
					blocks = append(blocks, anthropicContentBlock{
						Type: "image",
						Source: &anthropicImageSource{
							Type:      "base64",
							MediaType: p.MimeType,
							Data:      p.ImageB64,
						},
					})
				}
			}
			aReq.Messages = append(aReq.Messages, anthropicMsg{Role: role, Content: blocks})
		} else if len(m.ToolCalls) > 0 {
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				json.Unmarshal([]byte(tc.Arguments), &input)
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			aReq.Messages = append(aReq.Messages, anthropicMsg{Role: role, Content: blocks})
		} else {
			aReq.Messages = append(aReq.Messages, anthropicMsg{Role: role, Content: m.Content})
		}
	}

	for _, t := range req.Tools {
		aReq.Tools = append(aReq.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	return aReq
}

func (a *Anthropic) parseResponse(resp anthropicResp) *ChatResponse {
	cr := &ChatResponse{
		FinishReason:     resp.StopReason,
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			cr.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			cr.ToolCalls = append(cr.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	return cr
}
