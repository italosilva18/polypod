// Package opencode_llm — LLM Abstraction from OPENCODE
package opencode_llm

type LLMProvider string

const (
	ProviderOpenAI     LLMProvider = "openai"
	ProviderAnthropic  LLMProvider = "anthropic"
	ProviderGemini     LLMProvider = "gemini"
	ProviderOllama     LLMProvider = "ollama"
)

type LLMConfig struct {
	Provider LLMProvider
	Model    string
	APIKey   string
	BaseURL  string
}

type LLMClient struct {
	config LLMConfig
}

func NewLLMClient(config LLMConfig) *LLMClient {
	return &LLMClient{config: config}
}

func (c *LLMClient) Generate(prompt string) (string, error) {
	return "Generated response", nil
}
