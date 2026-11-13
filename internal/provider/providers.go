package provider

const (
	NVIDIAHostedBaseURL     = "https://integrate.api.nvidia.com/v1"
	NVIDIADefaultModel      = "nvidia/nemotron-3.5-super-vl-120b-a12b"
	OpenRouterBaseURL       = "https://openrouter.ai/api/v1"
	QwenDashScopeBaseURL    = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	GitHubDefaultBaseURL    = "https://api.githubcopilot.com"
)

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

// NewNVIDIA creates a provider for NVIDIA-hosted NIM or a self-hosted NIM.
func NewNVIDIA(apiKey, baseURL string) *OpenAICompat {
	if baseURL == "" {
		baseURL = NVIDIAHostedBaseURL
	}
	return NewOpenAICompat("nvidia", apiKey, baseURL)
}

// NewCerebras creates a Cerebras provider (fastest inference).
func NewCerebras(apiKey string) *OpenAICompat {
	return NewOpenAICompat("cerebras", apiKey, "https://api.cerebras.ai/v1")
}

// NewSambaNova creates a SambaNova provider.
func NewSambaNova(apiKey string) *OpenAICompat {
	return NewOpenAICompat("sambanova", apiKey, "https://api.sambanova.ai/v1")
}

// NewQwen creates an Alibaba Cloud DashScope / Qwen OpenAI-compatible provider.
func NewQwen(apiKey, baseURL string) *OpenAICompat {
	if baseURL == "" {
		baseURL = QwenDashScopeBaseURL
	}
	return NewOpenAICompat("qwen", apiKey, baseURL)
}

// NewGitHub creates a GitHub Copilot / GitHub OAuth OpenAI-compatible provider.
func NewGitHub(apiKey, baseURL string) *OpenAICompat {
	if baseURL == "" {
		baseURL = GitHubDefaultBaseURL
	}
	return NewOpenAICompat("github", apiKey, baseURL)
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
		if baseURL != "" {
			return NewOpenAICompat("openrouter", apiKey, baseURL)
		}
		return NewOpenRouter(apiKey)
	case "qwen":
		return NewQwen(apiKey, baseURL)
	case "github":
		return NewGitHub(apiKey, baseURL)
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
	case "nvidia":
		return NewNVIDIA(apiKey, baseURL)
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

// ProviderFactoryWithTokenSource creates a provider backed by a rotating OAuth TokenSource.
func ProviderFactoryWithTokenSource(name string, tokenSource TokenSource, baseURL string) Provider {
	switch name {
	case "chatgpt":
		return NewChatGPT(tokenSource)
	case "google":
		return NewGoogleWithTokenSource(tokenSource, baseURL)
	case "anthropic":
		return NewAnthropicWithTokenSource(tokenSource, baseURL)
	case "openrouter":
		if baseURL == "" {
			baseURL = OpenRouterBaseURL
		}
		return NewOpenAICompatWithTokenSource("openrouter", tokenSource, baseURL)
	case "qwen":
		if baseURL == "" {
			baseURL = QwenDashScopeBaseURL
		}
		return NewOpenAICompatWithTokenSource("qwen", tokenSource, baseURL)
	case "github":
		if baseURL == "" {
			baseURL = GitHubDefaultBaseURL
		}
		return NewOpenAICompatWithTokenSource("github", tokenSource, baseURL)
	default:
		return NewOpenAICompatWithTokenSource(name, tokenSource, baseURL)
	}
}

// SupportedProviders returns the list of all supported provider names.
func SupportedProviders() []string {
	return []string{
		"deepseek", "openai", "ollama", "anthropic", "google",
		"groq", "mistral", "cohere", "openrouter", "qwen", "github",
		"together", "perplexity", "fireworks", "deepinfra", "xai",
		"cerebras", "sambanova", "azure", "nvidia",
	}
}
