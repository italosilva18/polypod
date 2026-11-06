package tool

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/planmode"
	"github.com/costa/polypod/internal/types"
)

// PlanModeState holds the shared state for plan mode tools.
// It tracks the current plan and the plan simulator so that
// enter_plan_mode and exit_plan_mode can coordinate.
type PlanModeState struct {
	ModeState     *modes.State
	PlanSimulator *modes.PlanSimulator
	CurrentPlan   *planmode.Plan
}

// RegisterPlanModeTools registers the enter_plan_mode and exit_plan_mode tools
// with real implementations that interact with modes.State and PlanSimulator.
func RegisterPlanModeTools(reg *Registry, pms *PlanModeState) {
	registerEnterPlanMode(reg, pms)
	registerExitPlanMode(reg, pms)
}

// registerEnterPlanMode registers a tool that switches to plan mode,
// creates a new plan, and starts collecting simulated tool calls.
func registerEnterPlanMode(reg *Registry, pms *PlanModeState) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "enter_plan_mode" },
		DescriptionFn: func() string { return "Entrar em modo de planejamento (somente leitura)" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Titulo do plano a ser criado",
					},
				},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			// Switch to plan mode
			desc, err := pms.ModeState.SetMode("plan")
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Erro ao entrar em modo plan: %v", err),
					IsError: true,
				}, nil
			}

			// Reset the plan simulator to start fresh
			if pms.PlanSimulator != nil {
				pms.PlanSimulator.Reset()
			}

			// Create a new plan
			title := "Plano sem titulo"
			if t, ok := args["title"].(string); ok && t != "" {
				title = t
			}
			pms.CurrentPlan = planmode.NewPlan(title)

			var sb strings.Builder
			sb.WriteString(desc)
			sb.WriteString("\n\n")
			sb.WriteString(fmt.Sprintf("Plano criado: **%s** (ID: %s)\n\n", title, pms.CurrentPlan.ID))
			sb.WriteString("Instrucoes do modo plan:\n")
			sb.WriteString("1. Voce pode ler arquivos, buscar no codebase e analisar codigo.\n")
			sb.WriteString("2. Operacoes destrutivas (escrita, execucao) serao SIMULADAS.\n")
			sb.WriteString("3. Todas as operacoes simuladas serao registradas no plano.\n")
			sb.WriteString("4. Use `exit_plan_mode` quando terminar para ver o resumo e solicitar aprovacao.\n")

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}

// registerExitPlanMode registers a tool that shows the plan summary,
// switches back to edit mode, and resets the simulator.
func registerExitPlanMode(reg *Registry, pms *PlanModeState) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "exit_plan_mode" },
		DescriptionFn: func() string { return "Sair do modo de planejamento e mostrar resumo do plano" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"approve": map[string]any{
						"type":        "boolean",
						"description": "Se true, muda para modo edit para executar o plano. Se false, apenas mostra o resumo.",
					},
				},
			}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			// Check we're actually in plan mode
			if pms.ModeState.Current != modes.ModePlan {
				return types.ToolResult{
					Content: "Voce nao esta em modo plan. Use enter_plan_mode primeiro.",
					IsError: true,
				}, nil
			}

			// Get the plan summary from the simulator
			var summary string
			if pms.PlanSimulator != nil {
				summary = pms.PlanSimulator.FormatPlanSummary()
			} else {
				summary = "Nenhuma operacao simulada."
			}

			// Include plan step info if we have a plan
			var planInfo string
			if pms.CurrentPlan != nil {
				planInfo = fmt.Sprintf("Plano: **%s** (Status: %s, Passos: %d)\n\n",
					pms.CurrentPlan.Title,
					pms.CurrentPlan.Status,
					len(pms.CurrentPlan.Steps),
				)
			}

			// Check if the caller wants to approve and switch to edit mode
			approve, _ := args["approve"].(bool)

			var sb strings.Builder
			sb.WriteString("=== Saindo do modo plan ===\n\n")
			sb.WriteString(planInfo)
			sb.WriteString(summary)
			sb.WriteString("\n")

			if approve {
				// Switch to edit mode
				desc, err := pms.ModeState.SetMode("edit")
				if err != nil {
					return types.ToolResult{
						Content: fmt.Sprintf("Erro ao voltar para modo edit: %v", err),
						IsError: true,
					}, nil
				}

				sb.WriteString("\n--- Plano APROVADO ---\n")
				sb.WriteString(desc)
				sb.WriteString("\nVoce esta agora em modo edit. Execute as operacoes do plano acima.\n")
			} else {
				// Stay in plan mode but show summary for review
				sb.WriteString("\nPlano NAO aprovado. Ainda em modo plan.\n")
				sb.WriteString("Passe approve=true para aprovar e mudar para modo edit.\n")
			}

			// Reset simulator after showing summary (whether approved or not)
			if approve {
				if pms.PlanSimulator != nil {
					pms.PlanSimulator.Reset()
				}
				pms.CurrentPlan = nil
			}

			return types.ToolResult{Content: sb.String()}, nil
		},
	})
}
