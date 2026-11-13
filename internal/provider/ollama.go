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

	"github.com/google/uuid"
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

func (o *Ollama) Name() string                   { return "ollama" }
func (o *Ollama) SupportsVision() bool           { return true }
func (o *Ollama) SupportsTools() bool            { return true }
func (o *Ollama) SupportsStructuredOutput() bool { return true }

type ollamaChatReq struct {
	Model    string                 `json:"model"`
	Messages []ollamaMsg            `json:"messages"`
	Stream   bool                   `json:"stream"`
	Tools    []ollamaTool           `json:"tools,omitempty"`
	Format   json.RawMessage        `json:"format,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaMsg struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
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
	Message         ollamaMsg `json:"message"`
	Done            bool      `json:"done"`
	TotalDuration   int64     `json:"total_duration"`
	PromptEvalCount int       `json:"prompt_eval_count"`
	EvalCount       int       `json:"eval_count"`
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
		call, err := decodeOllamaToolCall(tc)
		if err != nil {
			return nil, err
		}
		cr.ToolCalls = append(cr.ToolCalls, call)
	}
	if len(cr.ToolCalls) == 0 {
		if call, ok := decodeOllamaTextToolCall(cr.Content, req.Tools); ok {
			cr.Content = ""
			cr.ToolCalls = append(cr.ToolCalls, call)
		}
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
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var content string
	var calls []ToolCall
	var totalPrompt, totalCompletion int

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk ollamaChatResp
		if err := json.Unmarshal(line, &chunk); err != nil {
			return nil, fmt.Errorf("ollama: bloco JSON inválido: %w", err)
		}
		if chunk.Message.Content != "" {
			content += chunk.Message.Content
			if callback != nil {
				callback(StreamDelta{Content: chunk.Message.Content})
			}
		}
		for _, native := range chunk.Message.ToolCalls {
			call, err := decodeOllamaToolCall(native)
			if err != nil {
				return nil, err
			}
			calls = append(calls, call)
			if callback != nil {
				callback(StreamDelta{ToolCalls: []ToolCall{call}})
			}
		}
		if chunk.Done {
			totalPrompt = chunk.PromptEvalCount
			totalCompletion = chunk.EvalCount
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ollama stream: %w", err)
	}
	if len(calls) == 0 {
		if call, ok := decodeOllamaTextToolCall(content, req.Tools); ok {
			content = ""
			calls = append(calls, call)
		}
	}

	if callback != nil {
		callback(StreamDelta{Done: true})
	}

	return &ChatResponse{
		Content:          content,
		ToolCalls:        calls,
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
		for _, call := range m.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(call.Arguments), &args); err == nil {
				var native ollamaToolCall
				native.Function.Name = call.Name
				native.Function.Arguments = args
				om.ToolCalls = append(om.ToolCalls, native)
			}
		}
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

func decodeOllamaToolCall(native ollamaToolCall) (ToolCall, error) {
	if strings.TrimSpace(native.Function.Name) == "" {
		return ToolCall{}, fmt.Errorf("ollama: tool sem nome")
	}
	args, err := json.Marshal(native.Function.Arguments)
	if err != nil {
		return ToolCall{}, fmt.Errorf("ollama: argumentos de tool: %w", err)
	}
	return ToolCall{ID: uuid.NewString(), Name: native.Function.Name, Arguments: string(args)}, nil
}

// Some local models emit a complete function call as JSON text instead of
// Ollama's structured tool_calls field. Accept only an exact object naming a
// tool that was actually advertised in this request.
func decodeOllamaTextToolCall(content string, offered []Tool) (ToolCall, bool) {
	var raw struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &raw) != nil || raw.Name == "" || len(raw.Arguments) == 0 {
		return ToolCall{}, false
	}
	for _, tool := range offered {
		if tool.Name == raw.Name {
			var args map[string]any
			if json.Unmarshal(raw.Arguments, &args) != nil || args == nil {
				return ToolCall{}, false
			}
			return ToolCall{ID: uuid.NewString(), Name: raw.Name, Arguments: string(raw.Arguments)}, true
		}
	}
	return ToolCall{}, false
}
