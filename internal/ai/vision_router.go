package ai

import (
	"context"
	"strings"

	"github.com/costa/polypod/internal/accountauth"
	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/models"
	providerapi "github.com/costa/polypod/internal/provider"
	openai "github.com/sashabaranov/go-openai"
)

type requestProviderKey struct{}

func withRequestProvider(ctx context.Context, p providerapi.Provider) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, requestProviderKey{}, p)
}

func (c *Client) activeResponseProviderFor(ctx context.Context) providerapi.Provider {
	if ctx != nil {
		if p, ok := ctx.Value(requestProviderKey{}).(providerapi.Provider); ok && p != nil {
			return p
		}
	}
	return c.activeResponseProvider()
}

// SetVisionModel configures an explicit multimodal fallback/override model for
// turns containing images or screenshots (equivalent to `/model --vision`).
// Passing model == "" or "auto" clears the manual override and restores auto-pick.
func (c *Client) SetVisionModel(providerName, model, apiKey, baseURL string) error {
	model = strings.TrimSpace(model)
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	if model == "" || strings.EqualFold(model, "auto") {
		c.ClearVisionModel()
		return nil
	}

	var visProv providerapi.Provider
	if providerName != "" && !strings.EqualFold(providerName, c.Provider()) {
		acctMgr := accountauth.NewManager(nil)
		if providerName == "chatgpt" {
			mgr := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
			visProv = providerapi.NewChatGPT(mgr.AccessToken)
		} else if apiKey == "" && accountauth.IsOAuthProvider(providerName) && acctMgr.HasActiveAccount(providerName) {
			if baseURL == "" {
				baseURL = acctMgr.ActiveBaseURL(providerName)
			}
			visProv = providerapi.ProviderFactoryWithTokenSource(providerName, acctMgr.TokenSource(providerName), baseURL)
		} else {
			visProv = providerapi.ProviderFactory(providerName, apiKey, baseURL)
		}
	}

	c.visionMu.Lock()
	c.visionProviderName = providerName
	c.visionModel = model
	c.visionProvider = visProv
	c.visionMu.Unlock()
	return nil
}

// ClearVisionModel resets multimodal routing back to automatic same-provider detection.
func (c *Client) ClearVisionModel() {
	c.visionMu.Lock()
	c.visionProviderName = ""
	c.visionModel = ""
	c.visionProvider = nil
	c.visionMu.Unlock()
}

// VisionModelConfig returns any explicitly configured vision override, plus the
// effective auto-picked fallback model when in automatic mode.
func (c *Client) VisionModelConfig() (explicitProvider, explicitModel, effectiveAutoModel string) {
	c.visionMu.RLock()
	explicitProvider = c.visionProviderName
	explicitModel = c.visionModel
	c.visionMu.RUnlock()

	_, effectiveAutoModel = c.resolveAutoVisionFallback(c.Model())
	return explicitProvider, explicitModel, effectiveAutoModel
}

func messagesContainImages(messages []ChatMessage) bool {
	for _, m := range messages {
		for _, p := range m.Parts {
			if p.Type == "image_url" || strings.TrimSpace(p.ImageURL) != "" {
				return true
			}
		}
	}
	return false
}

func openAIMessagesContainImages(messages []openai.ChatCompletionMessage) bool {
	for _, m := range messages {
		for _, p := range m.MultiContent {
			if p.Type == openai.ChatMessagePartTypeImageURL && p.ImageURL != nil && p.ImageURL.URL != "" {
				return true
			}
		}
	}
	return false
}

func observationsContainImages(results []ToolCallResult) bool {
	for _, r := range results {
		if len(r.Images) > 0 {
			return true
		}
	}
	return false
}

func (c *Client) modelSupportsVision(ctx context.Context, model string) bool {
	if capEntry := models.GetCapabilities(model); capEntry != nil {
		if !capEntry.SupportsVision {
			return false
		}
		if p := c.activeResponseProviderFor(ctx); p != nil {
			return p.SupportsVision()
		}
		return !c.isTextOnly()
	}
	if p := c.activeResponseProviderFor(ctx); p != nil {
		return p.SupportsVision()
	}
	return !c.isTextOnly()
}

func (c *Client) resolveAutoVisionFallback(currentModel string) (providerapi.Provider, string) {
	// 1. Explicit `/model --vision` override takes top precedence.
	c.visionMu.RLock()
	expProv := c.visionProvider
	expModel := c.visionModel
	c.visionMu.RUnlock()
	if expModel != "" {
		return expProv, expModel
	}

	// 2. Same-provider vision model (e.g., qwen3-coder-plus -> qwen3-vl-plus, o3-mini -> gpt-4.1).
	provName := c.Provider()
	if sameProvModel := models.BestVisionModelForProvider(provName); sameProvModel != "" && !strings.EqualFold(sameProvModel, currentModel) {
		if !c.isTextOnly() {
			return nil, sameProvModel
		}
	}

	// 3. Cross-provider fallback via any active OAuth session if current provider is text-only (e.g. deepseek).
	acctMgr := accountauth.NewManager(nil)
	for _, candidateProv := range []string{"google", "chatgpt", "openrouter", "anthropic", "qwen", "github"} {
		if !acctMgr.HasActiveAccount(candidateProv) {
			continue
		}
		visMod := models.BestVisionModelForProvider(candidateProv)
		if visMod == "" {
			continue
		}
		if candidateProv == "chatgpt" {
			cg := chatgptauth.NewManager(chatgptauth.DefaultCredentialPath())
			return providerapi.NewChatGPT(cg.AccessToken), visMod
		}
		baseURL := acctMgr.ActiveBaseURL(candidateProv)
		return providerapi.ProviderFactoryWithTokenSource(candidateProv, acctMgr.TokenSource(candidateProv), baseURL), visMod
	}

	return nil, ""
}

// withAutoVisionContext inspects messages for visual parts and transparently
// switches the turn's model/provider when an explicit `/model --vision` is set
// or when the active coding model does not support image inputs.
func (c *Client) withAutoVisionContext(ctx context.Context, messages []ChatMessage) context.Context {
	if !messagesContainImages(messages) {
		return ctx
	}
	currentModel := c.modelFor(ctx)

	c.visionMu.RLock()
	hasExplicit := c.visionModel != ""
	c.visionMu.RUnlock()

	if !hasExplicit && c.modelSupportsVision(ctx, currentModel) {
		return ctx
	}

	visProv, visModel := c.resolveAutoVisionFallback(currentModel)
	if visModel == "" {
		return ctx
	}
	ctx = withRequestModel(ctx, visModel)
	if visProv != nil {
		ctx = withRequestProvider(ctx, visProv)
	}
	return ctx
}
