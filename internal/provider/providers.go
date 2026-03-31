package provider

// Pre-configured providers using OpenAI-compatible API.
// Each is a thin wrapper over OpenAICompat with the correct base URL.

// NewGroq creates a Groq provider (ultra-fast inference).
func NewGroq(apiKey string) *OpenAICompat {
	return NewOpenAICompat("groq", apiKey, "https://api.groq.com/openai/v1")
}

// NewAzureOpenAI creates an Azure OpenAI provider.
func NewAzureOpenAI(apiKey, endpoint string) *OpenAICompat {
	return NewOpenAICompat("azure", apiKey, endpoint)
}

// NewMistral creates a Mistral AI provider.
func NewMistral(apiKey string) *OpenAICompat {
	return NewOpenAICompat("mistral", apiKey, "https://api.mistral.ai/v1")
}

// NewCohere creates a Cohere provider.
func NewCohere(apiKey string) *OpenAICompat {
	return NewOpenAICompat("cohere", apiKey, "https://api.cohere.com/compatibility/v1")
}

// NewOpenRouter creates an OpenRouter provider (100+ models).
func NewOpenRouter(apiKey string) *OpenAICompat {
	return NewOpenAICompat("openrouter", apiKey, "https://openrouter.ai/api/v1")
}

// NewTogether creates a Together AI provider.
func NewTogether(apiKey string) *OpenAICompat {
	return NewOpenAICompat("together", apiKey, "https://api.together.xyz/v1")
}

// NewPerplexity creates a Perplexity provider (search-enhanced).
func NewPerplexity(apiKey string) *OpenAICompat {
	return NewOpenAICompat("perplexity", apiKey, "https://api.perplexity.ai")
}

// NewFireworks creates a Fireworks AI provider.
func NewFireworks(apiKey string) *OpenAICompat {
	return NewOpenAICompat("fireworks", apiKey, "https://api.fireworks.ai/inference/v1")
}

// NewDeepInfra creates a DeepInfra provider.
func NewDeepInfra(apiKey string) *OpenAICompat {
	return NewOpenAICompat("deepinfra", apiKey, "https://api.deepinfra.com/v1/openai")
}

// NewXAI creates an xAI (Grok) provider.
func NewXAI(apiKey string) *OpenAICompat {
	return NewOpenAICompat("xai", apiKey, "https://api.x.ai/v1")
}

// NewCerebras creates a Cerebras provider (fastest inference).
func NewCerebras(apiKey string) *OpenAICompat {
	return NewOpenAICompat("cerebras", apiKey, "https://api.cerebras.ai/v1")
}

// NewSambaNova creates a SambaNova provider.
func NewSambaNova(apiKey string) *OpenAICompat {
	return NewOpenAICompat("sambanova", apiKey, "https://api.sambanova.ai/v1")
}

// ProviderFactory creates a provider from name + API key.
func ProviderFactory(name, apiKey, baseURL string) Provider {
	switch name {
	case "ollama":
		return NewOllama(baseURL)
	case "anthropic":
		return NewAnthropic(apiKey, baseURL)
	case "google":
		return NewGoogle(apiKey, baseURL)
	case "groq":
		return NewGroq(apiKey)
	case "mistral":
		return NewMistral(apiKey)
	case "cohere":
		return NewCohere(apiKey)
	case "openrouter":
		return NewOpenRouter(apiKey)
	case "together":
		return NewTogether(apiKey)
	case "perplexity":
		return NewPerplexity(apiKey)
	case "fireworks":
		return NewFireworks(apiKey)
	case "deepinfra":
		return NewDeepInfra(apiKey)
	case "xai":
		return NewXAI(apiKey)
	case "cerebras":
		return NewCerebras(apiKey)
	case "sambanova":
		return NewSambaNova(apiKey)
	case "azure":
		return NewAzureOpenAI(apiKey, baseURL)
	default:
		// Default: OpenAI-compatible with custom base URL
		return NewOpenAICompat(name, apiKey, baseURL)
	}
}

// SupportedProviders returns the list of all supported provider names.
func SupportedProviders() []string {
	return []string{
		"deepseek", "openai", "ollama", "anthropic", "google",
		"groq", "mistral", "cohere", "openrouter", "together",
		"perplexity", "fireworks", "deepinfra", "xai", "cerebras",
		"sambanova", "azure",
	}
}
