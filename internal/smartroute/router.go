package smartroute

import (
	"strings"
)

// Complexity classifies prompt complexity.
type Complexity int

const (
	ComplexityLow    Complexity = iota // simple factual, extraction, formatting
	ComplexityMedium                   // moderate reasoning, code explanation
	ComplexityHigh                     // complex reasoning, architecture, debugging
)

// ModelTier maps complexity to model categories.
type ModelTier struct {
	Low    string // cheapest: deepseek-chat, gpt-4o-mini, gemini-flash, llama3.1:8b
	Medium string // balanced: deepseek-chat, gpt-4o, claude-sonnet, gemini-pro
	High   string // frontier: gpt-4o, claude-opus, gemini-pro, deepseek-reasoner
}

// DefaultTiers for common providers (updated for 2026 model lineups).
var DefaultTiers = map[string]ModelTier{
	"deepseek":   {Low: "deepseek-chat", Medium: "deepseek-chat", High: "deepseek-reasoner"},
	"openai":     {Low: "gpt-4o-mini", Medium: "gpt-4.1", High: "o3"},
	"chatgpt":    {Low: "gpt-5-mini", Medium: "gpt-5", High: "gpt-5"},
	"anthropic":  {Low: "claude-3-5-haiku-latest", Medium: "claude-sonnet-4-0", High: "claude-opus-4-0"},
	"google":     {Low: "gemini-2.5-flash", Medium: "gemini-2.5-pro", High: "gemini-2.5-pro"},
	"openrouter": {Low: "google/gemini-2.5-flash", Medium: "anthropic/claude-sonnet-4", High: "openai/o3"},
	"qwen":       {Low: "qwen-turbo", Medium: "qwen-coder-plus", High: "qwq-plus"},
	"github":     {Low: "gpt-4o-mini", Medium: "claude-3.7-sonnet", High: "o3-mini"},
	"nvidia":     {Low: "nvidia/llama-3.1-nemotron-nano-8b-v1", Medium: "nvidia/nemotron-3.5-super-vl-120b-a12b", High: "deepseek-ai/deepseek-r1"},
	"groq":       {Low: "llama-3.1-8b-instant", Medium: "llama-3.3-70b-versatile", High: "deepseek-r1-distill-llama-70b"},
	"xai":        {Low: "grok-3-mini", Medium: "grok-3", High: "grok-3"},
	"ollama":     {Low: "qwen2.5-coder:7b", Medium: "qwen2.5-coder:32b", High: "deepseek-r1:32b"},
}

// ClassifyComplexity analyzes a prompt and returns its complexity level.
func ClassifyComplexity(prompt string) Complexity {
	lower := strings.ToLower(prompt)
	words := strings.Fields(lower)
	wordCount := len(words)

	// High complexity indicators
	highIndicators := []string{
		"arquitetura", "architecture", "refatorar", "refactor", "redesign",
		"debug", "depurar", "por que", "why does", "explain the bug",
		"implementar", "implement", "criar sistema", "build a system",
		"migrar", "migrate", "otimizar", "optimize", "performance",
		"seguranca", "security", "vulnerabilidade", "vulnerability",
		"concurrent", "concorrencia", "race condition", "deadlock",
		"design pattern", "trade-off", "compare and contrast",
	}
	for _, ind := range highIndicators {
		if strings.Contains(lower, ind) {
			return ComplexityHigh
		}
	}

	// Low complexity indicators
	lowIndicators := []string{
		"liste", "list", "mostre", "show", "qual o", "what is",
		"traduza", "translate", "formate", "format", "converta", "convert",
		"renomeie", "rename", "copie", "copy", "mova", "move",
		"resuma em uma linha", "one line summary",
	}
	for _, ind := range lowIndicators {
		if strings.Contains(lower, ind) {
			return ComplexityLow
		}
	}

	// Heuristics
	if wordCount < 10 {
		return ComplexityLow
	}
	if wordCount > 100 {
		return ComplexityHigh
	}

	// Code content suggests medium+
	codeIndicators := []string{"func ", "function ", "class ", "def ", "import ", "```"}
	for _, ind := range codeIndicators {
		if strings.Contains(prompt, ind) {
			return ComplexityMedium
		}
	}

	return ComplexityMedium
}

// SelectModel picks the best model for the given complexity and provider.
func SelectModel(provider string, complexity Complexity) string {
	tiers, ok := DefaultTiers[provider]
	if !ok {
		return "" // use default
	}
	switch complexity {
	case ComplexityLow:
		return tiers.Low
	case ComplexityHigh:
		return tiers.High
	default:
		return tiers.Medium
	}
}

// Route analyzes a prompt and returns the recommended model for a provider.
func Route(provider, prompt string) (model string, complexity Complexity) {
	complexity = ClassifyComplexity(prompt)
	model = SelectModel(provider, complexity)
	return
}

// ComplexityName returns a human-readable name.
func ComplexityName(c Complexity) string {
	switch c {
	case ComplexityLow:
		return "baixa"
	case ComplexityMedium:
		return "media"
	case ComplexityHigh:
		return "alta"
	default:
		return "desconhecida"
	}
}

// RecommendedEffort returns the reasoning effort ("low", "medium", "high") and
// native thinking token budget appropriate for complexity c.
func RecommendedEffort(c Complexity) (effort string, thinkingBudget int) {
	switch c {
	case ComplexityLow:
		return "low", 0
	case ComplexityHigh:
		return "high", 8192
	default:
		return "medium", 2048
	}
}

// BudgetForEffort maps an explicit CLI /effort level ("low", "medium", "high", "max")
// to provider ReasoningEffort and native ThinkingBudget tokens.
func BudgetForEffort(level string) (reasoningEffort string, thinkingBudget int) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low":
		return "low", 0
	case "high":
		return "high", 8192
	case "max":
		return "high", 16384
	case "medium":
		return "medium", 2048
	default:
		return "", 0
	}
}

// SelectAvailableProviderRoute picks the highest-capability configured provider
// and model from availableProviders for prompt.
func SelectAvailableProviderRoute(prompt string, availableProviders []string) (providerName, model string, complexity Complexity) {
	complexity = ClassifyComplexity(prompt)
	if len(availableProviders) == 0 {
		return "", "", complexity
	}
	avail := make(map[string]bool, len(availableProviders))
	for _, p := range availableProviders {
		avail[strings.ToLower(strings.TrimSpace(p))] = true
	}
	priority := []string{"anthropic", "chatgpt", "openai", "google", "openrouter", "deepseek", "qwen", "github", "nvidia", "groq", "xai", "ollama"}
	if complexity == ComplexityLow {
		priority = []string{"groq", "google", "deepseek", "openai", "chatgpt", "qwen", "openrouter", "anthropic", "github", "nvidia", "xai", "ollama"}
	}
	for _, cand := range priority {
		if avail[cand] {
			if m := SelectModel(cand, complexity); m != "" {
				return cand, m, complexity
			}
		}
	}
	first := strings.ToLower(strings.TrimSpace(availableProviders[0]))
	return first, SelectModel(first, complexity), complexity
}
