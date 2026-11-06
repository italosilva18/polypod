// Package models provides a unified model capabilities database with pricing,
// feature flags, and recommendation logic for all supported LLM providers.
package models

import (
	"fmt"
	"sort"
	"strings"
)

// ModelCapabilities describes the full feature set, limits, and pricing for a model.
type ModelCapabilities struct {
	ID                string   // Canonical model identifier (e.g. "claude-sonnet-4-20250514")
	Provider          string   // "anthropic", "openai", "google", "deepseek", "groq", "mistral"
	DisplayName       string   // Human-readable name (e.g. "Claude Sonnet 4")
	MaxContextTokens  int      // Total context window size
	MaxOutputTokens   int      // Maximum tokens in a single response
	SupportsVision    bool     // Can process images
	SupportsTools     bool     // Supports tool/function calling
	SupportsStreaming bool     // Supports streaming responses
	SupportsThinking  bool     // Extended thinking / chain-of-thought (o3, deepseek-reasoner, etc.)
	SupportsCaching   bool     // Prompt caching support
	SupportsJSON      bool     // Structured JSON output mode
	InputPricePer1M   float64  // USD per 1M input tokens
	OutputPricePer1M  float64  // USD per 1M output tokens
	CachePricePer1M   float64  // USD per 1M cached input tokens (read)
	Speed             string   // "fast", "medium", "slow"
	Quality           string   // "low", "medium", "high", "frontier"
	IsDeprecated      bool     // Model is deprecated and should be avoided
	RecommendedFor    []string // Use-case tags: "coding", "analysis", "creative", "fast-tasks"
}

// allModels is the authoritative capabilities database.
var allModels = []*ModelCapabilities{
	// ── Anthropic ──────────────────────────────────────────────────────
	{
		ID: "claude-sonnet-4-20250514", Provider: "anthropic",
		DisplayName:      "Claude Sonnet 4",
		MaxContextTokens: 200_000, MaxOutputTokens: 16_384,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 3.00, OutputPricePer1M: 15.00, CachePricePer1M: 0.30,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "claude-3-7-sonnet-20250219", Provider: "anthropic",
		DisplayName:      "Claude 3.7 Sonnet",
		MaxContextTokens: 200_000, MaxOutputTokens: 16_384,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 3.00, OutputPricePer1M: 15.00, CachePricePer1M: 0.30,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "claude-3-5-sonnet-20241022", Provider: "anthropic",
		DisplayName:      "Claude 3.5 Sonnet",
		MaxContextTokens: 200_000, MaxOutputTokens: 8_192,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 3.00, OutputPricePer1M: 15.00, CachePricePer1M: 0.30,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "claude-3-5-haiku-20241022", Provider: "anthropic",
		DisplayName:      "Claude 3.5 Haiku",
		MaxContextTokens: 200_000, MaxOutputTokens: 8_192,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 0.80, OutputPricePer1M: 4.00, CachePricePer1M: 0.08,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks", "coding"},
	},
	{
		ID: "claude-3-opus-20240229", Provider: "anthropic",
		DisplayName:      "Claude 3 Opus",
		MaxContextTokens: 200_000, MaxOutputTokens: 4_096,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 15.00, OutputPricePer1M: 75.00, CachePricePer1M: 1.50,
		Speed: "slow", Quality: "frontier",
		RecommendedFor: []string{"analysis", "creative"},
	},

	// ── OpenAI ─────────────────────────────────────────────────────────
	{
		ID: "gpt-4o", Provider: "openai",
		DisplayName:      "GPT-4o",
		MaxContextTokens: 128_000, MaxOutputTokens: 16_384,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 2.50, OutputPricePer1M: 10.00, CachePricePer1M: 0,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis", "creative"},
	},
	{
		ID: "gpt-4o-mini", Provider: "openai",
		DisplayName:      "GPT-4o Mini",
		MaxContextTokens: 128_000, MaxOutputTokens: 16_384,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 0.15, OutputPricePer1M: 0.60, CachePricePer1M: 0,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks"},
	},
	{
		ID: "gpt-4.1", Provider: "openai",
		DisplayName:      "GPT-4.1",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 32_768,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 2.00, OutputPricePer1M: 8.00, CachePricePer1M: 0,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "gpt-4.1-mini", Provider: "openai",
		DisplayName:      "GPT-4.1 Mini",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 32_768,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 0.40, OutputPricePer1M: 1.60, CachePricePer1M: 0,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks", "coding"},
	},
	{
		ID: "gpt-4.1-nano", Provider: "openai",
		DisplayName:      "GPT-4.1 Nano",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 32_768,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 0.10, OutputPricePer1M: 0.40, CachePricePer1M: 0,
		Speed: "fast", Quality: "low",
		RecommendedFor: []string{"fast-tasks"},
	},
	{
		ID: "o3", Provider: "openai",
		DisplayName:      "o3",
		MaxContextTokens: 200_000, MaxOutputTokens: 100_000,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 10.00, OutputPricePer1M: 40.00, CachePricePer1M: 0,
		Speed: "slow", Quality: "frontier",
		RecommendedFor: []string{"analysis", "coding"},
	},
	{
		ID: "o3-mini", Provider: "openai",
		DisplayName:      "o3 Mini",
		MaxContextTokens: 200_000, MaxOutputTokens: 100_000,
		SupportsVision: false, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 1.10, OutputPricePer1M: 4.40, CachePricePer1M: 0,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "o4-mini", Provider: "openai",
		DisplayName:      "o4 Mini",
		MaxContextTokens: 200_000, MaxOutputTokens: 100_000,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 1.10, OutputPricePer1M: 4.40, CachePricePer1M: 0,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},

	// ── Google ──────────────────────────────────────────────────────────
	{
		ID: "gemini-2.5-pro", Provider: "google",
		DisplayName:      "Gemini 2.5 Pro",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 65_536,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 1.25, OutputPricePer1M: 10.00, CachePricePer1M: 0.315,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
	{
		ID: "gemini-2.5-flash", Provider: "google",
		DisplayName:      "Gemini 2.5 Flash",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 65_536,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 0.15, OutputPricePer1M: 0.60, CachePricePer1M: 0.0375,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks", "coding"},
	},
	{
		ID: "gemini-2.0-flash", Provider: "google",
		DisplayName:      "Gemini 2.0 Flash",
		MaxContextTokens: 1_048_576, MaxOutputTokens: 8_192,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 0.10, OutputPricePer1M: 0.40, CachePricePer1M: 0.025,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks"},
	},

	// ── DeepSeek ────────────────────────────────────────────────────────
	{
		ID: "deepseek-chat", Provider: "deepseek",
		DisplayName:      "DeepSeek Chat (V3)",
		MaxContextTokens: 128_000, MaxOutputTokens: 8_192,
		SupportsVision: false, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: true, SupportsJSON: true,
		InputPricePer1M: 0.14, OutputPricePer1M: 0.28, CachePricePer1M: 0.014,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"coding", "fast-tasks"},
	},
	{
		ID: "deepseek-reasoner", Provider: "deepseek",
		DisplayName:      "DeepSeek Reasoner (R1)",
		MaxContextTokens: 128_000, MaxOutputTokens: 8_192,
		SupportsVision: false, SupportsTools: false, SupportsStreaming: true,
		SupportsThinking: true, SupportsCaching: true, SupportsJSON: false,
		InputPricePer1M: 0.55, OutputPricePer1M: 2.19, CachePricePer1M: 0.14,
		Speed: "slow", Quality: "high",
		RecommendedFor: []string{"analysis", "coding"},
	},

	// ── Groq ────────────────────────────────────────────────────────────
	{
		ID: "llama-3.3-70b-versatile", Provider: "groq",
		DisplayName:      "Llama 3.3 70B (Groq)",
		MaxContextTokens: 131_072, MaxOutputTokens: 32_768,
		SupportsVision: false, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 0.59, OutputPricePer1M: 0.79, CachePricePer1M: 0,
		Speed: "fast", Quality: "medium",
		RecommendedFor: []string{"fast-tasks", "coding"},
	},

	// ── Mistral ─────────────────────────────────────────────────────────
	{
		ID: "mistral-large-latest", Provider: "mistral",
		DisplayName:      "Mistral Large",
		MaxContextTokens: 128_000, MaxOutputTokens: 8_192,
		SupportsVision: true, SupportsTools: true, SupportsStreaming: true,
		SupportsThinking: false, SupportsCaching: false, SupportsJSON: true,
		InputPricePer1M: 2.00, OutputPricePer1M: 6.00, CachePricePer1M: 0,
		Speed: "medium", Quality: "high",
		RecommendedFor: []string{"coding", "analysis"},
	},
}

// modelIndex is built once for O(1) lookups by ID.
var modelIndex map[string]*ModelCapabilities

func init() {
	modelIndex = make(map[string]*ModelCapabilities, len(allModels))
	for _, m := range allModels {
		modelIndex[strings.ToLower(m.ID)] = m
	}
}

// GetCapabilities returns the capabilities for a model by ID.
// Returns nil if the model is not found.
func GetCapabilities(modelID string) *ModelCapabilities {
	return modelIndex[strings.ToLower(modelID)]
}

// ListModels returns all models for a given provider, sorted alphabetically by ID.
// If provider is empty, returns all models.
func ListModels(provider string) []*ModelCapabilities {
	if provider == "" {
		return ListAllModels()
	}
	p := strings.ToLower(provider)
	var result []*ModelCapabilities
	for _, m := range allModels {
		if strings.ToLower(m.Provider) == p {
			result = append(result, m)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// ListAllModels returns a copy of all models sorted by provider then ID.
func ListAllModels() []*ModelCapabilities {
	result := make([]*ModelCapabilities, len(allModels))
	copy(result, allModels)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// taskRecommendations maps task tags to ordered lists of preferred model IDs.
var taskRecommendations = map[string][]string{
	"coding":     {"claude-sonnet-4-20250514", "gpt-4.1", "gemini-2.5-pro", "deepseek-chat"},
	"analysis":   {"o3", "claude-3-opus-20240229", "gemini-2.5-pro", "deepseek-reasoner"},
	"creative":   {"claude-3-opus-20240229", "gpt-4o", "mistral-large-latest"},
	"fast-tasks": {"claude-3-5-haiku-20241022", "gpt-4o-mini", "gemini-2.5-flash", "deepseek-chat"},
}

// RecommendModel returns the best-fit non-deprecated model for a given task tag
// (e.g. "coding", "analysis", "creative", "fast-tasks").
// Falls back to claude-sonnet-4-20250514 if the task is unknown.
func RecommendModel(task string) *ModelCapabilities {
	ids, ok := taskRecommendations[strings.ToLower(task)]
	if !ok {
		// Default to Sonnet 4.
		return GetCapabilities("claude-sonnet-4-20250514")
	}
	for _, id := range ids {
		cap := GetCapabilities(id)
		if cap != nil && !cap.IsDeprecated {
			return cap
		}
	}
	return GetCapabilities("claude-sonnet-4-20250514")
}

// FormatModelInfo returns a multi-line human-readable summary of model capabilities.
func FormatModelInfo(cap *ModelCapabilities) string {
	if cap == nil {
		return "<unknown model>"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s)\n", cap.DisplayName, cap.ID)
	fmt.Fprintf(&sb, "  Provider:  %s\n", cap.Provider)
	fmt.Fprintf(&sb, "  Context:   %s tokens\n", formatTokenCount(cap.MaxContextTokens))
	fmt.Fprintf(&sb, "  Output:    %s tokens\n", formatTokenCount(cap.MaxOutputTokens))
	fmt.Fprintf(&sb, "  Pricing:   $%.2f/$%.2f per Mtok", cap.InputPricePer1M, cap.OutputPricePer1M)
	if cap.CachePricePer1M > 0 {
		fmt.Fprintf(&sb, " (cache: $%.3f)", cap.CachePricePer1M)
	}
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "  Speed:     %s | Quality: %s\n", cap.Speed, cap.Quality)

	var features []string
	if cap.SupportsVision {
		features = append(features, "vision")
	}
	if cap.SupportsTools {
		features = append(features, "tools")
	}
	if cap.SupportsStreaming {
		features = append(features, "streaming")
	}
	if cap.SupportsThinking {
		features = append(features, "thinking")
	}
	if cap.SupportsCaching {
		features = append(features, "caching")
	}
	if cap.SupportsJSON {
		features = append(features, "json")
	}
	if len(features) > 0 {
		fmt.Fprintf(&sb, "  Features:  %s\n", strings.Join(features, ", "))
	}

	if len(cap.RecommendedFor) > 0 {
		fmt.Fprintf(&sb, "  Best for:  %s\n", strings.Join(cap.RecommendedFor, ", "))
	}
	if cap.IsDeprecated {
		sb.WriteString("  WARNING:   deprecated\n")
	}

	return sb.String()
}

// FormatModelTable returns a formatted markdown table of all models.
func FormatModelTable() string {
	models := ListAllModels()

	var sb strings.Builder
	sb.WriteString("| Provider  | Model                          | Context  | Output | $/Mtok In | $/Mtok Out | Speed  | Quality  |\n")
	sb.WriteString("|-----------|--------------------------------|----------|--------|-----------|------------|--------|----------|\n")

	for _, m := range models {
		fmt.Fprintf(&sb, "| %-9s | %-30s | %8s | %6s | %9.2f | %10.2f | %-6s | %-8s |\n",
			m.Provider,
			truncate(m.ID, 30),
			formatTokenCount(m.MaxContextTokens),
			formatTokenCount(m.MaxOutputTokens),
			m.InputPricePer1M,
			m.OutputPricePer1M,
			m.Speed,
			m.Quality,
		)
	}

	return sb.String()
}

// --- helpers ----------------------------------------------------------------

func formatTokenCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.0fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%dK", n/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}
