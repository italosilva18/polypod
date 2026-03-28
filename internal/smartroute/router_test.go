package smartroute

import "testing"

func TestClassifyComplexity(t *testing.T) {
	tests := []struct {
		prompt string
		want   Complexity
	}{
		{"liste os arquivos", ComplexityLow},
		{"mostre o status", ComplexityLow},
		{"traduza para ingles", ComplexityLow},
		{"refatorar esta funcao para usar context e melhorar a performance", ComplexityHigh},
		{"debug este null pointer exception no handler", ComplexityHigh},
		{"implementar um sistema de autenticacao JWT completo", ComplexityHigh},
		{"arquitetura do microsservico com event sourcing", ComplexityHigh},
	}

	for _, tt := range tests {
		got := ClassifyComplexity(tt.prompt)
		if got != tt.want {
			t.Errorf("ClassifyComplexity(%q) = %s, want %s", tt.prompt, ComplexityName(got), ComplexityName(tt.want))
		}
	}
}

func TestSelectModel(t *testing.T) {
	tests := []struct {
		provider   string
		complexity Complexity
		wantEmpty  bool
	}{
		{"deepseek", ComplexityLow, false},
		{"openai", ComplexityHigh, false},
		{"anthropic", ComplexityMedium, false},
		{"unknown_provider", ComplexityLow, true},
	}

	for _, tt := range tests {
		model := SelectModel(tt.provider, tt.complexity)
		if tt.wantEmpty && model != "" {
			t.Errorf("SelectModel(%q, %d) = %q, want empty", tt.provider, tt.complexity, model)
		}
		if !tt.wantEmpty && model == "" {
			t.Errorf("SelectModel(%q, %d) = empty, want non-empty", tt.provider, tt.complexity)
		}
	}
}

func TestRoute(t *testing.T) {
	model, complexity := Route("deepseek", "liste os arquivos Go")
	if complexity != ComplexityLow {
		t.Errorf("expected low, got %s", ComplexityName(complexity))
	}
	if model == "" {
		t.Error("expected model name")
	}
}
