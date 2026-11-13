package tool

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/types"
)

// RegisterMultiChildQuestionTool registers `ask_parent_question` so concurrent
// subagents and background child tasks can submit questions to the parent
// operator without colliding or overwriting each other's drafts.
func RegisterMultiChildQuestionTool(reg *Registry, q *humanloop.MultiChildQuestionQueue) {
	if reg == nil || q == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "ask_parent_question" },
		DescriptionFn: func() string {
			return "Envia uma pergunta isolada da sub-tarefa/subagente atual para a fila concorrente do operador (/questions e /answer <id> <resposta>), aguardando a resposta sem colidir com outros subagentes."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "Pergunta clara e objetiva que precisa da decisão do operador",
					},
					"child_id": map[string]any{
						"type":        "string",
						"description": "Identificador curto da sub-tarefa ou subagente (ex.: 'worker-auth', 'arena-2')",
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Nome do papel/agente solicitante",
					},
					"options": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Opções sugeridas de resposta (opcional)",
					},
				},
				"required": []string{"question"},
			}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			question, _ := args["question"].(string)
			if strings.TrimSpace(question) == "" {
				return types.ValidationResult{Valid: false, Message: "question é obrigatório"}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			question, _ := args["question"].(string)
			childID, _ := args["child_id"].(string)
			if strings.TrimSpace(childID) == "" {
				childID = string(ctx.AgentID)
			}
			if strings.TrimSpace(childID) == "" {
				childID = "subagent"
			}
			agentName, _ := args["agent_name"].(string)
			if rawOpts, ok := args["options"].([]any); ok && len(rawOpts) > 0 {
				var opts []string
				for _, item := range rawOpts {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						opts = append(opts, strings.TrimSpace(s))
					}
				}
				if len(opts) > 0 {
					question = fmt.Sprintf("%s (opções sugeridas: %s)", strings.TrimSpace(question), strings.Join(opts, " | "))
				}
			}
			ans, err := q.Ask(ctx.Context, childID, agentName, question)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Erro aguardando resposta em ask_parent_question: %v", err),
					IsError: true,
				}, nil
			}
			return types.ToolResult{Content: ans}, nil
		},
	})
}
