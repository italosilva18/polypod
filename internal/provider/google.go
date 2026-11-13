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

// Google provides access to Gemini models via API key or OAuth Bearer token.
type Google struct {
	apiKey      string
	tokenSource TokenSource
	baseURL     string
	httpClient  *http.Client
}

// NewGoogle creates a Google Gemini provider.
func NewGoogle(apiKey, baseURL string) *Google {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &Google{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

// NewGoogleWithTokenSource creates a Google Gemini provider authenticated via rotating OAuth tokens.
func NewGoogleWithTokenSource(tokenSource TokenSource, baseURL string) *Google {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	return &Google{
		tokenSource: tokenSource,
		baseURL:     baseURL,
		httpClient:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (g *Google) endpointAndBearer(ctx context.Context, model, suffix string) (string, string, error) {
	if g.tokenSource != nil {
		token, err := g.tokenSource(ctx)
		if err != nil {
			return "", "", fmt.Errorf("google oauth: %w", err)
		}
		return fmt.Sprintf("%s/models/%s:%s", g.baseURL, model, suffix), token, nil
	}
	if strings.HasPrefix(g.apiKey, "ya29.") {
		return fmt.Sprintf("%s/models/%s:%s", g.baseURL, model, suffix), g.apiKey, nil
	}
	sep := "?"
	if strings.Contains(suffix, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s/models/%s:%s%skey=%s", g.baseURL, model, suffix, sep, g.apiKey), "", nil
}

func (g *Google) Name() string                   { return "google" }
func (g *Google) SupportsVision() bool           { return true }
func (g *Google) SupportsTools() bool            { return true }
func (g *Google) SupportsStructuredOutput() bool { return true }

type geminiReq struct {
	Contents         []geminiContent  `json:"contents"`
	SystemInstruct   *geminiContent   `json:"systemInstruction,omitempty"`
	Tools            []geminiToolDecl `json:"tools,omitempty"`
	GenerationConfig *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	InlineData   *geminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResp *geminiFunctionResp `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

type geminiGenConfig struct {
	Temperature      float32               `json:"temperature,omitempty"`
	MaxOutputTokens  int                   `json:"maxOutputTokens,omitempty"`
	ResponseMimeType string                `json:"responseMimeType,omitempty"`
	ThinkingConfig   *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiResp struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (g *Google) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := g.buildRequest(req)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		url, bearer, epErr := g.endpointAndBearer(ctx, req.Model, "generateContent")
		if epErr != nil {
			return nil, epErr
		}
		httpReq, reqErr := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
		if reqErr != nil {
			return nil, reqErr
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			httpReq.Header.Set("Authorization", "Bearer "+bearer)
		}

		resp, doErr := g.httpClient.Do(httpReq)
		if doErr != nil {
			return nil, fmt.Errorf("google: %w", doErr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 && g.tokenSource != nil {
			resp.Body.Close()
			if _, rotated := tryRotateAccount("google"); rotated {
				continue
			}
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if failoverStatus(resp.StatusCode) {
				return nil, fmt.Errorf("google HTTP %d: %w", resp.StatusCode, ErrProviderUnavailable)
			}
			return nil, fmt.Errorf("google HTTP %d: %s", resp.StatusCode, string(b))
		}

		var gResp geminiResp
		decErr := json.NewDecoder(resp.Body).Decode(&gResp)
		resp.Body.Close()
		if decErr != nil {
			return nil, fmt.Errorf("google decode: %w", decErr)
		}
		return g.parseResponse(gResp), nil
	}
	return nil, fmt.Errorf("google: %w", ErrProviderUnavailable)
}

func (g *Google) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	body := g.buildRequest(req)
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		url, bearer, epErr := g.endpointAndBearer(ctx, req.Model, "streamGenerateContent?alt=sse")
		if epErr != nil {
			return nil, epErr
		}
		httpReq, reqErr := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
		if reqErr != nil {
			return nil, reqErr
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			httpReq.Header.Set("Authorization", "Bearer "+bearer)
		}
		var doErr error
		resp, doErr = g.httpClient.Do(httpReq)
		if doErr != nil {
			return nil, fmt.Errorf("google stream: %w", doErr)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 && g.tokenSource != nil {
			resp.Body.Close()
			if _, rotated := tryRotateAccount("google"); rotated {
				continue
			}
			return nil, fmt.Errorf("google HTTP 429: %w", ErrProviderUnavailable)
		}
		break
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		if failoverStatus(resp.StatusCode) {
			return nil, fmt.Errorf("google HTTP %d: %w", resp.StatusCode, ErrProviderUnavailable)
		}
		return nil, fmt.Errorf("google HTTP %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var content strings.Builder
	var toolCalls []ToolCall
	var usage geminiUsage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		eventData := strings.TrimPrefix(line, "data: ")

		var gResp geminiResp
		if err := json.Unmarshal([]byte(eventData), &gResp); err != nil {
			continue
		}

		if gResp.UsageMetadata != nil {
			usage = *gResp.UsageMetadata
		}

		for _, cand := range gResp.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					content.WriteString(part.Text)
					if callback != nil {
						callback(StreamDelta{Content: part.Text})
					}
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, ToolCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					})
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
		PromptTokens:     usage.PromptTokenCount,
		CompletionTokens: usage.CandidatesTokenCount,
		TotalTokens:      usage.TotalTokenCount,
	}, nil
}

func (g *Google) buildRequest(req ChatRequest) geminiReq {
	gReq := geminiReq{
		GenerationConfig: &geminiGenConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		},
	}
	if req.ThinkingBudget > 0 {
		mLower := strings.ToLower(req.Model)
		if strings.Contains(mLower, "gemini-2.5") || strings.Contains(mLower, "gemini-3") {
			gReq.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
				ThinkingBudget: req.ThinkingBudget,
			}
		}
	}

	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		gReq.GenerationConfig.ResponseMimeType = "application/json"
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			gReq.SystemInstruct = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}

		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}

		if m.Role == "tool" {
			var respData map[string]any
			json.Unmarshal([]byte(m.Content), &respData)
			if respData == nil {
				respData = map[string]any{"result": m.Content}
			}
			gReq.Contents = append(gReq.Contents, geminiContent{
				Role: "function",
				Parts: []geminiPart{{
					FunctionResp: &geminiFunctionResp{
						Name:     m.Name,
						Response: respData,
					},
				}},
			})
			continue
		}

		var parts []geminiPart
		if m.Content != "" {
			parts = append(parts, geminiPart{Text: m.Content})
		}
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				parts = append(parts, geminiPart{Text: p.Text})
			case "image_base64":
				parts = append(parts, geminiPart{
					InlineData: &geminiInlineData{MimeType: p.MimeType, Data: p.ImageB64},
				})
			}
		}
		for _, tc := range m.ToolCalls {
			var args map[string]any
			json.Unmarshal([]byte(tc.Arguments), &args)
			parts = append(parts, geminiPart{
				FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: args},
			})
		}

		gReq.Contents = append(gReq.Contents, geminiContent{Role: role, Parts: parts})
	}

	if len(req.Tools) > 0 {
		var decls []geminiFuncDecl
		for _, t := range req.Tools {
			decls = append(decls, geminiFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		gReq.Tools = []geminiToolDecl{{FunctionDeclarations: decls}}
	}

	return gReq
}

func (g *Google) parseResponse(resp geminiResp) *ChatResponse {
	cr := &ChatResponse{}
	if resp.UsageMetadata != nil {
		cr.PromptTokens = resp.UsageMetadata.PromptTokenCount
		cr.CompletionTokens = resp.UsageMetadata.CandidatesTokenCount
		cr.TotalTokens = resp.UsageMetadata.TotalTokenCount
	}
	if len(resp.Candidates) == 0 {
		return cr
	}
	cand := resp.Candidates[0]
	cr.FinishReason = cand.FinishReason
	for _, part := range cand.Content.Parts {
		if part.Text != "" {
			cr.Content += part.Text
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			cr.ToolCalls = append(cr.ToolCalls, ToolCall{
				Name:      part.FunctionCall.Name,
				Arguments: string(argsJSON),
			})
		}
	}
	return cr
}
