package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/architect"
	"github.com/costa/polypod/internal/arena"
	"github.com/costa/polypod/internal/types"
)

// CognitiveToolDeps wires AI planning and isolated worktree candidate execution
// into `architect_plan` and `parallel_best_of_n` without creating import cycles.
type CognitiveToolDeps struct {
	ArchitectExec         func(ctx context.Context, workDir, prompt string) (string, error)
	EditorExec            func(ctx context.Context, workDir, prompt string) (string, error)
	WorktreeCandidateExec func(ctx context.Context, worktreeDir, task, strategy string) (string, error)
}

// RegisterCognitiveTools registers `architect_plan` (Pillar 4) and
// `parallel_best_of_n` (Pillar 5) in reg.
func RegisterCognitiveTools(reg *Registry, root string, deps CognitiveToolDeps) {
	if reg == nil {
		return
	}

	if deps.ArchitectExec != nil {
		reg.RegisterDef(types.ToolDef{
			NameFn: func() string { return "architect_plan" },
			DescriptionFn: func() string {
				return "Executa o pipeline de dois estágios Architect → Editor: primeiro gera um plano cirúrgico arquivo-por-arquivo com raciocínio profundo (Arquiteto) e, opcionalmente, aplica e verifica as edições (Editor)."
			},
			InputSchemaFn: func() map[string]any {
				return map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task": map[string]any{
							"type":        "string",
							"description": "Descrição completa da tarefa de arquitetura, refatoração ou correção complexa",
						},
						"plan_only": map[string]any{
							"type":        "boolean",
							"description": "Se true (padrão), retorna apenas o plano detalhado do Arquiteto para revisão/execução; se false, também executa o estágio Editor automaticamente",
						},
						"verify_cmd": map[string]any{
							"type":        "string",
							"description": "Comando opcional de lint/teste para validar após o estágio Editor",
						},
					},
					"required": []string{"task"},
				}
			},
			IsReadOnlyFn: func(args map[string]any) bool {
				planOnly := true
				if v, ok := args["plan_only"].(bool); ok {
					planOnly = v
				}
				return planOnly
			},
			IsDestructiveFn: func(args map[string]any) bool {
				if v, ok := args["plan_only"].(bool); ok && !v {
					return true
				}
				return false
			},
			ValidateFn: func(args map[string]any) types.ValidationResult {
				task, _ := args["task"].(string)
				if strings.TrimSpace(task) == "" {
					return types.ValidationResult{Valid: false, Message: "task é obrigatório"}
				}
				return types.ValidationResult{Valid: true}
			},
			ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
				task, _ := args["task"].(string)
				planOnly := true
				if v, ok := args["plan_only"].(bool); ok {
					planOnly = v
				}
				verifyCmd, _ := args["verify_cmd"].(string)
				wd := effectiveWorkingDir(ctx, root)

				var editorFn architect.StageExecutor
				if !planOnly && deps.EditorExec != nil {
					editorFn = func(c context.Context, p string) (string, error) {
						return deps.EditorExec(c, wd, p)
					}
				}
				res, err := architect.RunDualPipeline(
					ctx.Context,
					task,
					func(c context.Context, p string) (string, error) {
						return deps.ArchitectExec(c, wd, p)
					},
					editorFn,
					verifyCmd,
				)
				if err != nil {
					return types.ToolResult{Content: fmt.Sprintf("Erro no pipeline Architect/Editor: %v", err), IsError: true}, nil
				}
				return types.ToolResult{
					Content: architect.FormatPipelineSummary(res),
					IsError: !res.LintPassed,
				}, nil
			},
		})
	}

	if deps.WorktreeCandidateExec != nil {
		reg.RegisterDef(types.ToolDef{
			NameFn: func() string { return "parallel_best_of_n" },
			DescriptionFn: func() string {
				return "Executa Best-of-N real em git worktrees isoladas em paralelo: testa múltiplas estratégias para uma tarefa difícil, roda os testes/verificadores do projeto em cada worktree e aplica na árvore principal apenas o patch vencedor verificado."
			},
			InputSchemaFn: func() map[string]any {
				return map[string]any{
					"type": "object",
					"properties": map[string]any{
						"task": map[string]any{
							"type":        "string",
							"description": "Objetivo exato de código/correção a resolver e verificar nas worktrees paralelas",
						},
						"candidates": map[string]any{
							"type":        "integer",
							"description": "Número de candidatos paralelos (2 ou 3; padrão: 2)",
						},
						"verifier_cmd": map[string]any{
							"type":        "string",
							"description": "Comando determinístico de verificação (ex.: 'go test ./internal/foo/...'). Vazio auto-detecta.",
						},
						"apply_winner": map[string]any{
							"type":        "boolean",
							"description": "Aplicar automaticamente o patch vencedor verificado na árvore atual (padrão: true)",
						},
					},
					"required": []string{"task"},
				}
			},
			IsDestructiveFn: func(map[string]any) bool { return true },
			ValidateFn: func(args map[string]any) types.ValidationResult {
				task, _ := args["task"].(string)
				if strings.TrimSpace(task) == "" {
					return types.ValidationResult{Valid: false, Message: "task é obrigatório"}
				}
				return types.ValidationResult{Valid: true}
			},
			ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
				task, _ := args["task"].(string)
				verifierCmd, _ := args["verifier_cmd"].(string)
				applyWinner := true
				if v, ok := args["apply_winner"].(bool); ok {
					applyWinner = v
				}
				n := 2
				if rawN, ok := args["candidates"].(float64); ok && int(rawN) >= 2 && int(rawN) <= 3 {
					n = int(rawN)
				}
				allStrategies := []string{
					"Estratégia 1 (Cirúrgica Mínima): menor diff possível focado na causa-raiz sem alterar contratos externos.",
					"Estratégia 2 (Orientada a Contratos e Testes): trate explicitamente casos de borda/erros e garanta cobertura de teste.",
					"Estratégia 3 (Refatoração Estrutural Limpa): simplifique o fluxo de controle eliminando estado acoplado antes de validar.",
				}
				wd := effectiveWorkingDir(ctx, root)
				outcome, err := arena.RunWorktreeArena(ctx.Context, arena.WorktreeArenaConfig{
					RepoDir:     wd,
					Task:        task,
					Strategies:  allStrategies[:n],
					VerifierCmd: verifierCmd,
					ApplyWinner: applyWinner,
					Runner: func(c context.Context, _ int, wtDir string, strategy string) (string, error) {
						return deps.WorktreeCandidateExec(c, wtDir, task, strategy)
					},
				})
				if err != nil {
					return types.ToolResult{Content: fmt.Sprintf("Erro na Worktree Arena: %v", err), IsError: true}, nil
				}
				return types.ToolResult{
					Content: arena.FormatWorktreeOutcome(outcome),
					IsError: outcome.WinnerIndex < 0,
				}, nil
			},
		})
	}
}
