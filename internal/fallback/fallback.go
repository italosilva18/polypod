package fallback

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/costa/polypod/internal/provider"
)

// Chain manages a prioritized list of providers with automatic fallback.
type Chain struct {
	providers []provider.Provider
	modelMap  map[string]map[string]string // provider -> source_model -> fallback_model
	logger    *slog.Logger
}

// NewChain creates a fallback chain from a list of providers.
func NewChain(providers []provider.Provider, logger *slog.Logger) *Chain {
	return &Chain{
		providers: providers,
		modelMap:  defaultModelMap(),
		logger:    logger,
	}
}

// Chat tries each provider in order until one succeeds.
func (c *Chain) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	var lastErr error
	originalModel := req.Model

	for i, p := range c.providers {
		// Map model to this provider's equivalent
		req.Model = c.mapModel(p.Name(), originalModel)

		resp, err := p.Chat(ctx, req)
		if err == nil {
			if i > 0 {
				c.logger.Info("fallback usado",
					"provider_original", c.providers[0].Name(),
					"provider_fallback", p.Name(),
					"model", req.Model,
				)
			}
			return resp, nil
		}

		lastErr = err
		c.logger.Warn("provider falhou, tentando proximo",
			"provider", p.Name(),
			"model", req.Model,
			"error", err,
			"next", i+1 < len(c.providers),
		)

		// Only fallback on retriable errors (rate limit, server error)
		if !isRetriableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("todos os providers falharam. Ultimo erro: %w", lastErr)
}

// ChatStream tries each provider with streaming.
func (c *Chain) ChatStream(ctx context.Context, req provider.ChatRequest, callback provider.StreamCallback) (*provider.ChatResponse, error) {
	var lastErr error
	originalModel := req.Model

	for i, p := range c.providers {
		req.Model = c.mapModel(p.Name(), originalModel)

		resp, err := p.ChatStream(ctx, req, callback)
		if err == nil {
			if i > 0 {
				c.logger.Info("fallback stream usado", "provider", p.Name(), "model", req.Model)
			}
			return resp, nil
		}

		lastErr = err
		c.logger.Warn("provider stream falhou", "provider", p.Name(), "error", err)

		if !isRetriableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("todos os providers falharam (stream). Ultimo erro: %w", lastErr)
}

func (c *Chain) mapModel(providerName, model string) string {
	if providerMap, ok := c.modelMap[providerName]; ok {
		if mapped, ok := providerMap[model]; ok {
			return mapped
		}
	}
	return model // use original if no mapping
}

func defaultModelMap() map[string]map[string]string {
	return map[string]map[string]string{
		"ollama": {
			"deepseek-chat":           "deepseek-coder-v2",
			"gpt-4o":                  "llama3.1:70b",
			"gpt-4o-mini":             "llama3.1:8b",
			"claude-3-5-sonnet-latest": "llama3.1:70b",
			"gemini-1.5-pro":          "llama3.1:70b",
		},
		"openai": {
			"deepseek-chat": "gpt-4o-mini",
			"llama3.1":      "gpt-4o-mini",
		},
		"anthropic": {
			"deepseek-chat": "claude-3-haiku-20240307",
			"gpt-4o":        "claude-3-5-sonnet-latest",
			"gpt-4o-mini":   "claude-3-haiku-20240307",
		},
		"google": {
			"deepseek-chat": "gemini-1.5-flash",
			"gpt-4o":        "gemini-1.5-pro",
		},
	}
}

func isRetriableError(err error) bool {
	msg := strings.ToLower(err.Error())
	retriable := []string{
		"429", "rate limit", "too many requests",
		"500", "502", "503", "504",
		"internal server error", "bad gateway",
		"service unavailable", "gateway timeout",
		"connection refused", "connection reset",
		"timeout", "deadline exceeded",
	}
	for _, r := range retriable {
		if strings.Contains(msg, r) {
			return true
		}
	}
	return false
}

// SetModelMap allows custom model mapping between providers.
func (c *Chain) SetModelMap(providerName, sourceModel, targetModel string) {
	if c.modelMap[providerName] == nil {
		c.modelMap[providerName] = make(map[string]string)
	}
	c.modelMap[providerName][sourceModel] = targetModel
}
