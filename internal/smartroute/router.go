package smartroute

import (
	"strings"
)

// Complexity classifies prompt complexity.
type Complexity int

const (
	ComplexityLow    Complexity = iota // simple factual, extraction, formatting
	ComplexityMedium                    // moderate reasoning, code explanation
	ComplexityHigh                      // complex reasoning, architecture, debugging
)

// ModelTier maps complexity to model categories.
type ModelTier struct {
	Low    string // cheapest: deepseek-chat, gpt-4o-mini, gemini-flash, llama3.1:8b
	Medium string // balanced: deepseek-chat, gpt-4o, claude-sonnet, gemini-pro
	High   string // frontier: gpt-4o, claude-opus, gemini-pro, deepseek-reasoner
}

// DefaultTiers for common providers.
var DefaultTiers = map[string]ModelTier{
	"deepseek": {Low: "deepseek-chat", Medium: "deepseek-chat", High: "deepseek-reasoner"},
	"openai":   {Low: "gpt-4o-mini", Medium: "gpt-4o", High: "gpt-4o"},
	"anthropic": {Low: "claude-3-haiku-20240307", Medium: "claude-3-5-sonnet-latest", High: "claude-3-opus-latest"},
	"google":   {Low: "gemini-1.5-flash", Medium: "gemini-1.5-pro", High: "gemini-1.5-pro"},
	"ollama":   {Low: "llama3.1:8b", Medium: "llama3.1:70b", High: "llama3.1:70b"},
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
