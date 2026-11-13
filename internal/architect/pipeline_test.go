package architect

import (
	"context"
	"strings"
	"testing"
)

func TestRunDualPipeline_ArchitectEditorAndVerifier(t *testing.T) {
	ctx := context.Background()
	var stages []string

	archExec := func(_ context.Context, prompt string) (string, error) {
		stages = append(stages, "architect")
		if !strings.Contains(prompt, "Refatorar serviço de autenticação") {
			t.Fatalf("expected task in architect prompt, got %q", prompt)
		}
		return "1. Editar internal/auth/service.go para extrair TokenValidator.\n2. Atualizar internal/auth/service_test.go.", nil
	}

	editExec := func(_ context.Context, prompt string) (string, error) {
		stages = append(stages, "editor")
		if !strings.Contains(prompt, "TokenValidator") {
			t.Fatalf("expected architect plan injected into editor prompt, got %q", prompt)
		}
		return "Arquivos internal/auth/service.go e internal/auth/service_test.go atualizados com sucesso.", nil
	}

	res, err := RunDualPipeline(ctx, "Refatorar serviço de autenticação", archExec, editExec, "true")
	if err != nil {
		t.Fatalf("RunDualPipeline failed: %v", err)
	}
	if len(stages) != 2 || stages[0] != "architect" || stages[1] != "editor" {
		t.Fatalf("expected [architect, editor] stage order, got %v", stages)
	}
	if !res.LintPassed {
		t.Fatalf("expected LintPassed=true")
	}

	summary := FormatPipelineSummary(res)
	if !strings.Contains(summary, "Plano do Arquiteto") || !strings.Contains(summary, "Execução do Editor") {
		t.Fatalf("unexpected pipeline summary:\n%s", summary)
	}
}
