package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Ollama provides local model inference via the Ollama API.
type Ollama struct {
	baseURL    string
	httpClient *http.Client
}

// NewOllama creates an Ollama provider. baseURL defaults to http://localhost:11434.
func NewOllama(baseURL string) *Ollama {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Ollama{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *Ollama) Name() string                  { return "ollama" }
func (o *Ollama) SupportsVision() bool           { return true }
func (o *Ollama) SupportsTools() bool            { return true }
func (o *Ollama) SupportsStructuredOutput() bool { return true }

type ollamaChatReq struct {
	Model    string            `json:"model"`
	Messages []ollamaMsg       `json:"messages"`
	Stream   bool              `json:"stream"`
	Tools    []ollamaTool      `json:"tools,omitempty"`
	Format   json.RawMessage   `json:"format,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaMsg struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Images    []string          `json:"images,omitempty"`
	ToolCalls []ollamaToolCall  `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string          `json:"type"`
	Function ollamaFunction  `json:"function"`
}

type ollamaFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

type ollamaChatResp struct {
	Message          ollamaMsg `json:"message"`
	Done             bool      `json:"done"`
	TotalDuration    int64     `json:"total_duration"`
	PromptEvalCount  int       `json:"prompt_eval_count"`
	EvalCount        int       `json:"eval_count"`
}

func (o *Ollama) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := o.buildRequest(req, false)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(b))
	}

	var oResp ollamaChatResp
	if err := json.NewDecoder(resp.Body).Decode(&oResp); err != nil {
		return nil, fmt.Errorf("ollama decode: %w", err)
	}

	cr := &ChatResponse{
		Content:          oResp.Message.Content,
		PromptTokens:     oResp.PromptEvalCount,
		CompletionTokens: oResp.EvalCount,
		TotalTokens:      oResp.PromptEvalCount + oResp.EvalCount,
	}
	for _, tc := range oResp.Message.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Function.Arguments)
		cr.ToolCalls = append(cr.ToolCalls, ToolCall{
			Name:      tc.Function.Name,
			Arguments: string(argsJSON),
		})
	}
	return cr, nil
}

func (o *Ollama) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	body := o.buildRequest(req, true)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var content string
	var totalPrompt, totalCompletion int

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatResp
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}
		if chunk.Message.Content != "" {
			content += chunk.Message.Content
			if callback != nil {
				callback(StreamDelta{Content: chunk.Message.Content})
			}
		}
		if chunk.Done {
			totalPrompt = chunk.PromptEvalCount
			totalCompletion = chunk.EvalCount
		}
	}

	if callback != nil {
		callback(StreamDelta{Done: true})
	}

	return &ChatResponse{
		Content:          content,
		PromptTokens:     totalPrompt,
		CompletionTokens: totalCompletion,
		TotalTokens:      totalPrompt + totalCompletion,
	}, nil
}

func (o *Ollama) buildRequest(req ChatRequest, stream bool) ollamaChatReq {
	oReq := ollamaChatReq{
		Model:  req.Model,
		Stream: stream,
		Options: map[string]interface{}{
			"temperature": req.Temperature,
			"num_predict": req.MaxTokens,
		},
	}

	for _, m := range req.Messages {
		om := ollamaMsg{Role: m.Role, Content: m.Content}
		for _, p := range m.Parts {
			switch p.Type {
			case "image_base64":
				om.Images = append(om.Images, p.ImageB64)
			case "text":
				if om.Content == "" {
					om.Content = p.Text
				}
			}
		}
		oReq.Messages = append(oReq.Messages, om)
	}

	for _, t := range req.Tools {
		oReq.Tools = append(oReq.Tools, ollamaTool{
			Type: "function",
			Function: ollamaFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		oReq.Format = json.RawMessage(`"json"`)
	}

	return oReq
}
