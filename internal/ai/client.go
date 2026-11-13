package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/config"
	providerapi "github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/tracking"
	openai "github.com/sashabaranov/go-openai"
)

// Client wraps the OpenAI-compatible API client for DeepSeek.
type Client struct {
	client           *openai.Client
	responseProvider providerapi.Provider
	providerMu       sync.RWMutex // protects responseProvider during interactive switches
	providerName     string
	textOnly         bool
	modelMu          sync.RWMutex // protects the manually selected default model
	model            string
	maxTok           int
	maxToolIter      int
	temp             float32
	toolsOn          bool
	skills           SkillRegistry
	skillNamesMu     sync.RWMutex
	skillNames       []string
	toolSelectionMu  sync.Mutex
	toolSelections   map[toolSessionID]*toolSelection
	costEngine       *tracking.CostEngine
	toolEventMu      sync.RWMutex
	toolEventCB      ToolEventCallback
	streamingToolMgr *StreamingToolManager
	effortMu           sync.RWMutex
	effort             string
	repoPlaybook       *RepoPlaybookStore
	visionMu           sync.RWMutex
	visionProviderName string
	visionModel        string
	visionProvider     providerapi.Provider
	waterfallMu        sync.Mutex
	waterfall          *WaterfallRecorder
}

// Waterfall returns the trajectory waterfall recorder, initializing lazily if needed.
func (c *Client) Waterfall() *WaterfallRecorder {
	c.waterfallMu.Lock()
	defer c.waterfallMu.Unlock()
	if c.waterfall == nil {
		c.waterfall = NewWaterfallRecorder(12)
	}
	return c.waterfall
}

// SetEffort configures the default reasoning effort ("low", "medium", "high", "max").
func (c *Client) SetEffort(effort string) {
	c.effortMu.Lock()
	c.effort = effort
	c.effortMu.Unlock()
}

func (c *Client) defaultEffort() string {
	c.effortMu.RLock()
	defer c.effortMu.RUnlock()
	return c.effort
}

// SetRepoPlaybook attaches the verified engineering episode store.
func (c *Client) SetRepoPlaybook(store *RepoPlaybookStore) {
	c.repoPlaybook = store
}

// SetCostEngine attaches a CostEngine for automatic cost tracking on every
// API call made through this client.
func (c *Client) SetCostEngine(ce *tracking.CostEngine) {
	c.costEngine = ce
}

func (c *Client) recordProviderUsage(resp *providerapi.ChatResponse, requestedModel string) {
	if c.costEngine == nil || resp == nil {
		return
	}
	model := resp.Model
	if model == "" {
		model = requestedModel
	}
	c.costEngine.RecordCompletion(model, resp.PromptTokens, resp.CompletionTokens, resp.CacheReadTokens, resp.CacheWriteTokens)
}

// SetToolEventCallback sets a callback that is invoked for each tool
// lifecycle event (start, complete, error) during streaming tool execution.
func (c *Client) SetToolEventCallback(cb ToolEventCallback) {
	c.toolEventMu.Lock()
	c.toolEventCB = cb
	c.toolEventMu.Unlock()
}

func (c *Client) emitToolEvent(ctx context.Context, event ToolEvent) {
	c.Waterfall().ObserveToolEvent(event)
	if ctx != nil {
		if callback, ok := ctx.Value(toolEventCallbackKey{}).(ToolEventCallback); ok {
			if callback != nil {
				callback(event)
			}
			return
		}
	}
	c.toolEventMu.RLock()
	callback := c.toolEventCB
	c.toolEventMu.RUnlock()
	if callback != nil {
		callback(event)
	}
}

// SetStreamingToolManager attaches a StreamingToolManager that enables
// intelligent parallel execution of tool calls during streaming. When set,
// read-only tools are executed concurrently while write tools run serially.
func (c *Client) SetStreamingToolManager(stm *StreamingToolManager) {
	c.streamingToolMgr = stm
}

// NewClient creates a DeepSeek client using OpenAI-compatible API.
func NewClient(cfg config.AIConfig, skills SkillRegistry) *Client {
	ocfg := openai.DefaultConfig(cfg.APIKey)
	ocfg.BaseURL = cfg.BaseURL
	c := &Client{
		client:       openai.NewClientWithConfig(ocfg),
		providerName: cfg.Provider,
		model:        cfg.Model,
		maxTok:       cfg.MaxToks,
		maxToolIter:  cfg.ToolIterationLimit(),
		temp:         cfg.Temp,
		toolsOn:      cfg.Tools,
		skills:       skills,
		textOnly:     cfg.Provider == "deepseek",
		waterfall:    NewWaterfallRecorder(12),
	}
	acctMgr := accountauth.NewManager(nil)
	if cfg.Provider == "chatgpt" {
		manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
		c.responseProvider = providerapi.NewChatGPT(manager.AccessToken)
	} else if cfg.APIKey == "" && accountauth.IsOAuthProvider(cfg.Provider) && acctMgr.HasActiveAccount(cfg.Provider) {
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = acctMgr.ActiveBaseURL(cfg.Provider)
		}
		c.responseProvider = providerapi.ProviderFactoryWithTokenSource(cfg.Provider, acctMgr.TokenSource(cfg.Provider), baseURL)
	} else if cfg.Provider == "anthropic" || cfg.Provider == "google" || cfg.Provider == "ollama" {
		c.responseProvider = providerapi.ProviderFactory(cfg.Provider, cfg.APIKey, cfg.BaseURL)
	}
	if cfg.FallbackProvider != "" && cfg.FallbackProvider != cfg.Provider && cfg.FallbackModel != "" {
		primary := c.responseProvider
		if primary == nil {
			primary = providerapi.ProviderFactory(cfg.Provider, cfg.APIKey, cfg.BaseURL)
		}
		var secondary providerapi.Provider
		if cfg.FallbackProvider == "chatgpt" {
			manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
			secondary = providerapi.NewChatGPT(manager.AccessToken)
		} else if cfg.FallbackAPIKey == "" && accountauth.IsOAuthProvider(cfg.FallbackProvider) && acctMgr.HasActiveAccount(cfg.FallbackProvider) {
			fbURL := cfg.FallbackBaseURL
			if fbURL == "" {
				fbURL = acctMgr.ActiveBaseURL(cfg.FallbackProvider)
			}
			secondary = providerapi.ProviderFactoryWithTokenSource(cfg.FallbackProvider, acctMgr.TokenSource(cfg.FallbackProvider), fbURL)
		} else {
			secondary = providerapi.ProviderFactory(cfg.FallbackProvider, cfg.FallbackAPIKey, cfg.FallbackBaseURL)
		}
		c.responseProvider = providerapi.NewFailover(primary, secondary, cfg.FallbackModel, nil)
	}
	return c
}

func (c *Client) toolIterationLimit() int {
	if c.maxToolIter == -1 {
		return -1
	}
	if c.maxToolIter <= 0 {
		return config.DefaultMaxToolIterations
	}
	return c.maxToolIter
}

// SetSkillNames sets the active skill names for tool definitions.
func (c *Client) SetSkillNames(names []string) {
	c.skillNamesMu.Lock()
	c.skillNames = append([]string(nil), names...)
	c.skillNamesMu.Unlock()
}

type requestSkillNamesKey struct{}

func withRequestSkillNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, requestSkillNamesKey{}, append([]string(nil), names...))
}

func (c *Client) skillNamesFor(ctx context.Context) []string {
	if names, ok := ctx.Value(requestSkillNamesKey{}).([]string); ok {
		return names
	}
	c.skillNamesMu.RLock()
	defer c.skillNamesMu.RUnlock()
	return append([]string(nil), c.skillNames...)
}

// Model returns the configured model name.
func (c *Client) Model() string {
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	return c.model
}

// SetModel changes the model used for subsequent API calls.
func (c *Client) SetModel(model string) {
	if model == "" {
		return
	}
	c.modelMu.Lock()
	c.model = model
	c.modelMu.Unlock()
}

// Provider returns the active backend name.
func (c *Client) Provider() string {
	c.providerMu.RLock()
	defer c.providerMu.RUnlock()
	return c.providerName
}

// SwitchProvider replaces the backend for subsequent turns and selects its model.
// The caller validates credentials before invoking this method.
func (c *Client) SwitchProvider(name, model, apiKey, baseURL string) error {
	if name == "" || model == "" {
		return fmt.Errorf("provedor e modelo são obrigatórios")
	}
	var next providerapi.Provider
	acctMgr := accountauth.NewManager(nil)
	if name == "chatgpt" {
		manager := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
		next = providerapi.NewChatGPT(manager.AccessToken)
	} else if apiKey == "" && accountauth.IsOAuthProvider(name) && acctMgr.HasActiveAccount(name) {
		if baseURL == "" {
			baseURL = acctMgr.ActiveBaseURL(name)
		}
		next = providerapi.ProviderFactoryWithTokenSource(name, acctMgr.TokenSource(name), baseURL)
	} else {
		next = providerapi.ProviderFactory(name, apiKey, baseURL)
	}
	c.providerMu.Lock()
	c.responseProvider = next
	c.providerName = name
	c.textOnly = name == "deepseek"
	c.providerMu.Unlock()
	c.SetModel(model)
	return nil
}

func (c *Client) activeResponseProvider() providerapi.Provider {
	c.providerMu.RLock()
	defer c.providerMu.RUnlock()
	return c.responseProvider
}

func (c *Client) isTextOnly() bool {
	c.providerMu.RLock()
	defer c.providerMu.RUnlock()
	return c.textOnly
}

type requestModelKey struct{}

// withRequestModel pins a routed model to a single turn. Tool iterations keep
// that same model even if another session changes its manual default.
func withRequestModel(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, requestModelKey{}, model)
}

func (c *Client) modelFor(ctx context.Context) string {
	if model, ok := ctx.Value(requestModelKey{}).(string); ok && model != "" {
		return model
	}
	return c.Model()
}

// MaxTokens returns the configured max tokens limit.
func (c *Client) MaxTokens() int {
	return c.maxTok
}

// Temperature returns the configured temperature.
func (c *Client) Temperature() float32 {
	return c.temp
}

// CreateChatCompletion sends a raw chat completion request to the underlying API
// with retry logic. This is used by sub-agent executors that manage their own
// tool-calling loop.
func (c *Client) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	if prov := c.activeResponseProviderFor(ctx); prov != nil {
		genericReq := providerapi.ChatRequest{Model: req.Model, MaxTokens: req.MaxTokens, Temperature: req.Temperature}
		genericReq.Stop = req.Stop
		if req.ResponseFormat != nil {
			genericReq.ResponseFormat = &providerapi.ResponseFormat{Type: string(req.ResponseFormat.Type)}
			if req.ResponseFormat.JSONSchema != nil {
				genericReq.ResponseFormat.Schema = responseToolParameters(req.ResponseFormat.JSONSchema.Schema)
			}
		}
		for _, msg := range req.Messages {
			m := providerapi.Message{Role: msg.Role, Content: msg.Content, ToolCallID: msg.ToolCallID, Name: msg.Name}
			for _, part := range msg.MultiContent {
				switch part.Type {
				case openai.ChatMessagePartTypeText:
					m.Parts = append(m.Parts, providerapi.ContentPart{Type: "text", Text: part.Text})
				case openai.ChatMessagePartTypeImageURL:
					if part.ImageURL != nil {
						m.Parts = append(m.Parts, providerapi.ContentPart{Type: "image_url", ImageURL: part.ImageURL.URL})
					}
				}
			}
			for _, tc := range msg.ToolCalls {
				m.ToolCalls = append(m.ToolCalls, providerapi.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			}
			genericReq.Messages = append(genericReq.Messages, m)
		}
		for _, tool := range req.Tools {
			if tool.Function != nil {
				genericReq.Tools = append(genericReq.Tools, providerapi.Tool{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: responseToolParameters(tool.Function.Parameters)})
			}
		}
		resp, err := prov.Chat(ctx, genericReq)
		if err != nil {
			return openai.ChatCompletionResponse{}, err
		}
		c.recordProviderUsage(resp, req.Model)
		message := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: resp.Content}
		for _, tc := range resp.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, openai.ToolCall{ID: tc.ID, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: tc.Name, Arguments: tc.Arguments}})
		}
		finish := openai.FinishReasonStop
		if len(message.ToolCalls) > 0 {
			finish = openai.FinishReasonToolCalls
		}
		return openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: message, FinishReason: finish}},
			Usage:   openai.Usage{PromptTokens: resp.PromptTokens, CompletionTokens: resp.CompletionTokens, TotalTokens: resp.TotalTokens},
		}, nil
	}
	return retry(ctx, func() (openai.ChatCompletionResponse, error) {
		return c.client.CreateChatCompletion(ctx, req)
	})
}

// ChatMessage mirrors openai.ChatCompletionMessage.
type ChatMessage struct {
	Parts   []providerapi.ContentPart
	Role    string
	Content string
}

// ChatResponse is the result of a chat completion.
type ChatResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// StreamCallback is called for each token delta during streaming.
type StreamCallback func(delta string)

// Complete sends a chat completion request with retry.
func (c *Client) Complete(ctx context.Context, messages []ChatMessage) (*ChatResponse, error) {
	ctx = c.withAutoVisionContext(ctx, messages)
	ctx, finishWaterfall := c.beginWaterfallTurn(ctx, messages)
	defer finishWaterfall()

	model := c.modelFor(ctx)
	if prov := c.activeResponseProviderFor(ctx); prov != nil {
		req := providerapi.ChatRequest{Model: model, MaxTokens: c.maxTok, Temperature: c.temp}
		c.applyReasoningToProviderReq(ctx, &req, false)
		for _, msg := range messages {
			req.Messages = append(req.Messages, providerapi.Message{Role: msg.Role, Content: msg.Content, Parts: msg.Parts})
		}
		started := time.Now()
		resp, err := prov.Chat(ctx, req)
		if err != nil {
			c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
			return nil, err
		}
		c.Waterfall().RecordModelSpan(model, started, time.Since(started), resp.PromptTokens, resp.CompletionTokens, false)
		c.recordProviderUsage(resp, model)
		return &ChatResponse{Content: resp.Content, PromptTokens: resp.PromptTokens, CompletionTokens: resp.CompletionTokens, TotalTokens: resp.TotalTokens}, nil
	}
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
	}

	started := time.Now()
	resp, err := retry(ctx, func() (openai.ChatCompletionResponse, error) {
		oReq := openai.ChatCompletionRequest{
			Model:       model,
			Messages:    msgs,
			MaxTokens:   c.maxTok,
			Temperature: c.temp,
		}
		c.applyReasoningToOpenAIReq(ctx, &oReq, false)
		return c.client.CreateChatCompletion(ctx, oReq)
	})
	if err != nil {
		c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
		return nil, fmt.Errorf("chat completion: %w", err)
	}

	if len(resp.Choices) == 0 {
		c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
		return nil, fmt.Errorf("no choices in response")
	}
	c.Waterfall().RecordModelSpan(model, started, time.Since(started), resp.Usage.PromptTokens, resp.Usage.CompletionTokens, false)

	if c.costEngine != nil {
		c.costEngine.RecordCompletion(model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, 0, 0)
	}

	return &ChatResponse{
		Content:          resp.Choices[0].Message.Content,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}, nil
}

// CompleteWithTools sends a chat completion with tool calling loop and retry.
func (c *Client) CompleteWithTools(ctx context.Context, messages []ChatMessage, logger *slog.Logger) (*ChatResponse, error) {
	ctx = c.withAutoVisionContext(ctx, messages)
	ctx, finishWaterfall := c.beginWaterfallTurn(ctx, messages)
	defer finishWaterfall()

	if _, checkpointed := checkpointScopeFor(ctx); checkpointed {
		return c.CompleteWithToolsStream(ctx, messages, nil, logger)
	}
	if c.activeResponseProviderFor(ctx) != nil {
		resp, err := c.completeWithResponseTools(ctx, messages, nil, logger)
		return resp, err
	}
	catalog := c.catalogFor(ctx)
	tools := catalog.definitions()
	if !c.toolsOn || len(tools) == 0 {
		return c.Complete(ctx, messages)
	}
	model := c.modelFor(ctx)

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
	}

	var totalPrompt, totalCompletion int

	maxIterations := c.toolIterationLimit()
	guard := newCognitiveGuard()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
		started := time.Now()
		resp, err := retry(ctx, func() (openai.ChatCompletionResponse, error) {
			oReq := openai.ChatCompletionRequest{
				Model:       model,
				Messages:    msgs,
				MaxTokens:   c.maxTok,
				Temperature: c.temp,
				Tools:       tools,
			}
			c.applyReasoningToOpenAIReq(ctx, &oReq, guard.boostedReasoning)
			return c.client.CreateChatCompletion(ctx, oReq)
		})
		if err != nil {
			c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
			return nil, fmt.Errorf("chat completion (iter %d): %w", iter, err)
		}
		if len(resp.Choices) == 0 {
			c.Waterfall().RecordModelSpan(model, started, time.Since(started), 0, 0, true)
			return nil, fmt.Errorf("no choices in response (iter %d)", iter)
		}
		c.Waterfall().RecordModelSpan(model, started, time.Since(started), resp.Usage.PromptTokens, resp.Usage.CompletionTokens, false)

		totalPrompt += resp.Usage.PromptTokens
		totalCompletion += resp.Usage.CompletionTokens

		if c.costEngine != nil {
			c.costEngine.RecordCompletion(model, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, 0, 0)
		}

		choice := resp.Choices[0]

		if choice.FinishReason != openai.FinishReasonToolCalls || len(choice.Message.ToolCalls) == 0 {
			if maxIterations < 0 || iter+1 < maxIterations {
				if gate := guard.checkCompletionGate(ctx); gate != "" {
					msgs = append(msgs,
						openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: choice.Message.Content},
						openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: gate},
					)
					continue
				}
			}
			c.recordTurnPlaybook(ctx, guard, choice.Message.Content)
			return &ChatResponse{
				Content:          choice.Message.Content,
				PromptTokens:     totalPrompt,
				CompletionTokens: totalCompletion,
				TotalTokens:      totalPrompt + totalCompletion,
			}, nil
		}

		msgs = append(msgs, choice.Message)

		var observations []ToolCallResult
		var gCalls []guardedCall
		for _, tc := range choice.Message.ToolCalls {
			gCalls = append(gCalls, guardedCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments})
			logger.Debug("executing tool",
				"tool", tc.Function.Name,
				"args", tc.Function.Arguments,
				"iter", iter,
			)
			var parsedArgs map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsedArgs); err != nil {
				parsedArgs = map[string]any{"raw": tc.Function.Arguments}
			}
			c.emitToolEvent(ctx, ToolEvent{
				Type: ToolEventStart, ToolName: tc.Function.Name,
				ToolID: tc.ID, Args: parsedArgs,
			})
			toolStart := time.Now()

			if c.costEngine != nil {
				c.costEngine.RecordToolUse(tc.Function.Name, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
			}

			richResult, execErr := executeSkillResult(ctx, c.skills, tc.Function.Name, tc.Function.Arguments)
			result := richResult.Content
			observations = append(observations, ToolCallResult{ID: tc.ID, Name: tc.Function.Name, Content: result, Images: richResult.Images, IsError: richResult.IsError || execErr != nil})
			if execErr != nil {
				result = fmt.Sprintf("Erro executando tool: %v", execErr)
			}
			resultBytes, resultHash := toolResultMetadata(result)
			if richResult.IsError || execErr != nil {
				c.emitToolEvent(ctx, ToolEvent{
					Type: ToolEventError, ToolName: tc.Function.Name,
					ToolID: tc.ID, Error: result, ResultBytes: resultBytes,
					ResultSHA256: resultHash, Elapsed: time.Since(toolStart),
				})
			} else {
				c.emitToolEvent(ctx, ToolEvent{
					Type: ToolEventComplete, ToolName: tc.Function.Name,
					ToolID: tc.ID, Result: result, ResultBytes: resultBytes,
					ResultSHA256: resultHash, Elapsed: time.Since(toolStart),
				})
			}

			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result,
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
			})
		}
		if !c.isTextOnly() {
			if observationsContainImages(observations) && !c.modelSupportsVision(ctx, model) {
				if visProv, visModel := c.resolveAutoVisionFallback(model); visModel != "" && visProv == nil {
					model = visModel
					ctx = withRequestModel(ctx, visModel)
				}
			}
			msgs = append(msgs, openAIImageObservations(observations)...)
		}
		catalog.observe(observations)
		tools = catalog.definitions()
		if replan, _ := guard.observe(ctx, gCalls, observations); replan != "" && (maxIterations < 0 || iter+1 < maxIterations) {
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: replan})
		}
	}

	return nil, fmt.Errorf("tool calling reached ai.max_tool_iterations (%d)", maxIterations)
}

// CompleteWithToolsStream sends a streaming chat completion with tool calling loop.
// It calls callback for each content delta token, and handles tool calls between stream cycles.
func (c *Client) CompleteWithToolsStream(ctx context.Context, messages []ChatMessage, callback StreamCallback, logger *slog.Logger) (*ChatResponse, error) {
	ctx = c.withAutoVisionContext(ctx, messages)
	ctx, finishWaterfall := c.beginWaterfallTurn(ctx, messages)
	defer finishWaterfall()

	if _, checkpointed := checkpointScopeFor(ctx); checkpointed {
		if c.activeResponseProviderFor(ctx) != nil {
			return c.completeWithCheckpointedProvider(ctx, messages, callback, logger)
		}
		return c.completeWithCheckpointedOpenAI(ctx, messages, callback, logger)
	}
	if c.activeResponseProviderFor(ctx) != nil {
		resp, err := c.completeWithResponseTools(ctx, messages, callback, logger)
		return resp, err
	}
	var catalog *toolCatalog
	var tools []openai.Tool
	if c.toolsOn {
		catalog = c.catalogFor(ctx)
		tools = catalog.definitions()
	}

	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openAIChatMessage(m)
	}

	var totalPrompt, totalCompletion int
	model := c.modelFor(ctx)

	maxIterations := c.toolIterationLimit()
	guard := newCognitiveGuard()
	for iter := 0; maxIterations < 0 || iter < maxIterations; iter++ {
		for _, guidance := range takeSteering(ctx) {
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: guidance})
		}
		req := openai.ChatCompletionRequest{
			Model:       model,
			Messages:    msgs,
			MaxTokens:   c.maxTok,
			Temperature: c.temp,
			Stream:      true,
		}
		c.applyReasoningToOpenAIReq(ctx, &req, guard.boostedReasoning)
		if len(tools) > 0 {
			req.Tools = tools
		}

		streamStart := time.Now()
		stream, err := retry(ctx, func() (*openai.ChatCompletionStream, error) {
			return c.client.CreateChatCompletionStream(ctx, req)
		})
		if err != nil {
			c.Waterfall().RecordModelSpan(model, streamStart, time.Since(streamStart), 0, 0, true)
			return nil, fmt.Errorf("stream creation (iter %d): %w", iter, err)
		}

		var contentBuf string
		// Prepare complete JSON arguments as fragments arrive. Chat
		// Completions provides no reliable per-call completion boundary, so
		// execution waits for EOF and final validation.
		preparedToolCalls := newStreamToolPreparation()

		for {
			chunk, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				stream.Close()
				c.Waterfall().RecordModelSpan(model, streamStart, time.Since(streamStart), 0, 0, true)
				return nil, fmt.Errorf("stream recv (iter %d): %w", iter, recvErr)
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			// Accumulate content and call callback
			if delta.Content != "" {
				contentBuf += delta.Content
				if callback != nil {
					callback(delta.Content)
				}
			}

			// Accumulate tool calls by index
			for _, tc := range delta.ToolCalls {
				preparedToolCalls.add(tc)
			}
		}
		stream.Close()
		c.Waterfall().RecordModelSpan(model, streamStart, time.Since(streamStart), 0, len(contentBuf)/4, false)

		// If no tool calls, we're done
		if len(preparedToolCalls.byIndex) == 0 {
			var guidance []string
			if maxIterations < 0 || iter+1 < maxIterations {
				guidance = takeSteering(ctx)
				if len(guidance) == 0 {
					if gate := guard.checkCompletionGate(ctx); gate != "" {
						guidance = append(guidance, gate)
					}
				}
			}
			if len(guidance) > 0 {
				msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: contentBuf})
				for _, message := range guidance {
					msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: message})
				}
				if callback != nil {
					callback("\n")
				}
				continue
			}
			c.recordTurnPlaybook(ctx, guard, contentBuf)
			return &ChatResponse{
				Content:          contentBuf,
				PromptTokens:     totalPrompt,
				CompletionTokens: totalCompletion,
				TotalTokens:      totalPrompt + totalCompletion,
			}, nil
		}

		toolCalls, preparedCalls, err := preparedToolCalls.finalize()
		if err != nil {
			return nil, fmt.Errorf("validating streamed tool calls (iter %d): %w", iter, err)
		}

		assistantMsg := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   contentBuf,
			ToolCalls: toolCalls,
		}
		msgs = append(msgs, assistantMsg)

		// Execute tool calls — use StreamingToolManager for intelligent
		// parallelism when available, fall back to sequential execution.
		var observations []ToolCallResult
		if c.streamingToolMgr != nil {
			for i, tc := range toolCalls {
				// Fire ToolEventStart for each tool.
				parsedArgs := clonePreparedArgs(preparedCalls[i].preparedArgs)
				c.emitToolEvent(ctx, ToolEvent{
					Type: ToolEventStart, ToolName: tc.Function.Name,
					ToolID: tc.ID, Args: parsedArgs,
				})

				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Function.Name, 0, 0)
				}
			}

			// Execute with parallel partitioning
			batchStart := time.Now()
			results := c.streamingToolMgr.ExecuteToolCalls(ctx, preparedCalls)
			observations = append(observations, results...)
			batchElapsed := time.Since(batchStart)

			logger.Debug("streaming tool batch complete",
				"total", len(results),
				"elapsed", batchElapsed,
				"iter", iter,
			)

			// Convert results back to messages
			for _, r := range results {
				content := r.Content
				if r.IsError {
					content = fmt.Sprintf("Erro executando tool: %s", r.Content)
				}
				resultBytes, resultHash := toolResultMetadata(r.Content)

				// Fire ToolEventComplete/Error.
				if r.IsError {
					c.emitToolEvent(ctx, ToolEvent{
						Type: ToolEventError, ToolName: r.Name, ToolID: r.ID,
						Error: r.Content, ResultBytes: resultBytes,
						ResultSHA256: resultHash, Elapsed: batchElapsed,
					})
				} else {
					displayResult := r.Content
					if len(displayResult) > 200 {
						displayResult = displayResult[:197] + "..."
					}
					c.emitToolEvent(ctx, ToolEvent{
						Type: ToolEventComplete, ToolName: r.Name, ToolID: r.ID,
						Result: displayResult, ResultBytes: resultBytes,
						ResultSHA256: resultHash, Elapsed: batchElapsed,
					})
				}

				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    content,
					Name:       r.Name,
					ToolCallID: r.ID,
				})
			}
		} else {
			// Fallback: sequential execution
			for i, tc := range toolCalls {
				logger.Debug("executing tool (stream)",
					"tool", tc.Function.Name,
					"args", tc.Function.Arguments,
					"iter", iter,
				)

				parsedArgs := clonePreparedArgs(preparedCalls[i].preparedArgs)

				c.emitToolEvent(ctx, ToolEvent{
					Type: ToolEventStart, ToolName: tc.Function.Name,
					ToolID: tc.ID, Args: parsedArgs,
				})

				toolStart := time.Now()

				if c.costEngine != nil {
					c.costEngine.RecordToolUse(tc.Function.Name, 0, 0)
				}

				richResult, execErr := executeSkillResult(ctx, c.skills, tc.Function.Name, tc.Function.Arguments)
				result := richResult.Content
				observations = append(observations, ToolCallResult{ID: tc.ID, Name: tc.Function.Name, Content: result, Images: richResult.Images, IsError: richResult.IsError || execErr != nil})

				toolElapsed := time.Since(toolStart)

				if execErr != nil {
					result = fmt.Sprintf("Erro executando tool: %v", execErr)
				}
				resultBytes, resultHash := toolResultMetadata(result)
				if execErr != nil || richResult.IsError {
					c.emitToolEvent(ctx, ToolEvent{
						Type: ToolEventError, ToolName: tc.Function.Name,
						ToolID: tc.ID, Args: parsedArgs,
						Error: result, ResultBytes: resultBytes,
						ResultSHA256: resultHash, Elapsed: toolElapsed,
					})
				} else {
					displayResult := result
					if len(displayResult) > 200 {
						displayResult = displayResult[:197] + "..."
					}
					c.emitToolEvent(ctx, ToolEvent{
						Type: ToolEventComplete, ToolName: tc.Function.Name,
						ToolID: tc.ID, Args: parsedArgs,
						Result: displayResult, ResultBytes: resultBytes,
						ResultSHA256: resultHash, Elapsed: toolElapsed,
					})
				}

				msgs = append(msgs, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    result,
					Name:       tc.Function.Name,
					ToolCallID: tc.ID,
				})
			}
		}

		if !c.isTextOnly() {
			if observationsContainImages(observations) && !c.modelSupportsVision(ctx, model) {
				if visProv, visModel := c.resolveAutoVisionFallback(model); visModel != "" && visProv == nil {
					model = visModel
					ctx = withRequestModel(ctx, visModel)
				}
			}
			msgs = append(msgs, openAIImageObservations(observations)...)
		}
		if catalog != nil {
			catalog.observe(observations)
			tools = catalog.definitions()
		}
		gCalls := make([]guardedCall, 0, len(toolCalls))
		for _, tc := range toolCalls {
			gCalls = append(gCalls, guardedCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments})
		}
		if replan, _ := guard.observe(ctx, gCalls, observations); replan != "" && (maxIterations < 0 || iter+1 < maxIterations) {
			msgs = append(msgs, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: replan})
		}

		// Notify callback that tool execution happened (optional separator)
		if callback != nil {
			callback("\n")
		}
	}

	return nil, fmt.Errorf("tool calling reached ai.max_tool_iterations (%d)", maxIterations)
}
