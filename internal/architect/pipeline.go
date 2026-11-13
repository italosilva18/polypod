package architect

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PipelineResult holds the structured output of an executable Architect -> Editor run.
type PipelineResult struct {
	Task            string
	Plan            string
	ExecutionReport string
	LintPassed      bool
	LintOutput      string
	Duration        time.Duration
}

// StageExecutor executes one stage prompt (Architect planning or Editor implementation).
type StageExecutor func(ctx context.Context, stagePrompt string) (string, error)

// RunDualPipeline executes the two-stage Architect -> Editor engineering pattern:
//  1. Architect analyzes the codebase and produces an explicit file-by-file change plan.
//  2. Editor executes the surgical edits strictly following the Architect's plan.
//  3. Optional post-pipeline lint/test verification checks the resulting tree.
func RunDualPipeline(
	ctx context.Context,
	task string,
	architectExec StageExecutor,
	editorExec StageExecutor,
	verifyCmd string,
) (*PipelineResult, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return nil, fmt.Errorf("a tarefa do pipeline Architect/Editor não pode ser vazia")
	}
	if architectExec == nil {
		return nil, fmt.Errorf("executor do estágio Arquiteto indisponível")
	}
	start := time.Now()

	archPrompt := FormatArchitectPrompt(task)
	plan, err := architectExec(ctx, archPrompt)
	if err != nil {
		return nil, fmt.Errorf("falha no estágio Arquiteto: %w", err)
	}
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return nil, fmt.Errorf("o Arquiteto retornou um plano vazio")
	}

	res := &PipelineResult{
		Task:       task,
		Plan:       plan,
		LintPassed: true,
	}

	if editorExec == nil {
		res.Duration = time.Since(start)
		return res, nil
	}

	editPrompt := FormatEditorPrompt(plan)
	execReport, err := editorExec(ctx, editPrompt)
	if err != nil {
		res.Duration = time.Since(start)
		return res, fmt.Errorf("falha no estágio Editor: %w", err)
	}
	res.ExecutionReport = strings.TrimSpace(execReport)

	if strings.TrimSpace(verifyCmd) != "" {
		lintRes, lintErr := RunLint(ctx, verifyCmd)
		if lintErr == nil && lintRes != nil {
			res.LintPassed = lintRes.Passed
			res.LintOutput = lintRes.Output
		}
	}

	res.Duration = time.Since(start)
	return res, nil
}

// FormatPipelineSummary formats a human-readable summary of a completed DualPipeline run.
func FormatPipelineSummary(r *PipelineResult) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Pipeline Architect → Editor (%s)\n\n", r.Duration.Round(100*time.Millisecond))
	b.WriteString("### 1. Plano do Arquiteto\n")
	b.WriteString(r.Plan)
	b.WriteString("\n")
	if r.ExecutionReport != "" {
		b.WriteString("\n### 2. Execução do Editor\n")
		b.WriteString(r.ExecutionReport)
		b.WriteString("\n")
	}
	if r.LintOutput != "" || !r.LintPassed {
		status := "PASSOU"
		if !r.LintPassed {
			status = "FALHOU"
		}
		fmt.Fprintf(&b, "\n### 3. Verificação Pós-Edição: %s\n", status)
		if r.LintOutput != "" {
			b.WriteString("```\n" + r.LintOutput + "\n```\n")
		}
	}
	return strings.TrimSpace(b.String())
}
