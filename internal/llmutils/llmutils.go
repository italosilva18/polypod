// Package llmutils — LLM Utilities (token estimation + model switching)
package llmutils

type TokenEstimator struct {
	modelRates map[string]float64
}

type ModelSwitcher struct {
	currentModel string
	quickModels  []string
}

func NewTokenEstimator() *TokenEstimator {
	return &TokenEstimator{modelRates: map[string]float64{"gpt-4o": 0.015, "claude-sonnet": 0.003}}
}

func NewModelSwitcher() *ModelSwitcher {
	return &ModelSwitcher{currentModel: "gpt-4o", quickModels: []string{"gpt-4o", "claude-sonnet", "gemini-pro"}}
}

func (e *TokenEstimator) Estimate(text string, model string) float64 {
	tokens := len(text) / 4
	return float64(tokens) * e.modelRates[model] / 1000
}

func (s *ModelSwitcher) Switch(model string) { s.currentModel = model }
func (s *ModelSwitcher) GetCurrent() string { return s.currentModel }
