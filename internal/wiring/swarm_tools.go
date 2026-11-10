package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/subagent"
	"github.com/costa/polypod/internal/swarm"
	"github.com/costa/polypod/internal/tool"
	"github.com/costa/polypod/internal/types"
)

// registerSwarmTools connects team operations to the live swarm manager and
// the same full agent executor used by agent_run. Worker turns persist a
// model/tool checkpoint when the team journal is enabled. team_resume only
// diagnoses crash boundaries; a journal prompt is not a resume cursor.
func registerSwarmTools(reg *tool.Registry, manager *swarm.SwarmManager, agents *agent.Registry, executor *subagent.SubAgentExecutor) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_create" },
		DescriptionFn: func() string {
			return "Criar e iniciar uma equipe local de agentes; com data_dir configurado, histórico e resultados ficam no diário persistente."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"name":        map[string]any{"type": "string", "description": "Nome único da equipe"},
				"description": map[string]any{"type": "string", "description": "Objetivo da equipe"},
			}, "name")
		},
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name := swarmArg(args, "name")
			if name == "" {
				return swarmError("name é obrigatório"), nil
			}
			teamConfig := swarm.DefaultTeamConfig()
			teamConfig.MaxMembers = 0
			team, err := manager.CreateTeam(name, swarmArg(args, "description"), teamConfig)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			if err := team.Start(); err != nil {
				_ = manager.DeleteTeam(name, 5*time.Second)
				return swarmError(err.Error()), nil
			}
			return swarmJSON(team.Summary()), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_spawn" },
		DescriptionFn: func() string {
			return "Iniciar um agente real em uma equipe e, opcionalmente, enviar a primeira tarefa."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team":   map[string]any{"type": "string", "description": "Nome da equipe"},
				"name":   map[string]any{"type": "string", "description": "Nome do integrante"},
				"agent":  map[string]any{"type": "string", "description": "Definição de agente; padrão: default"},
				"prompt": map[string]any{"type": "string", "description": "Primeira tarefa, opcional"},
			}, "team", "name")
		},
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			team, err := swarmTeam(manager, swarmArg(args, "team"))
			if err != nil {
				return swarmError(err.Error()), nil
			}
			name := swarmArg(args, "name")
			if name == "" || name == swarm.TeamLeaderName {
				return swarmError("name precisa identificar um integrante diferente do líder"), nil
			}
			agentName := swarmArg(args, "agent")
			if agentName == "" {
				agentName = agents.Default
			}
			if !agents.Has(agentName) {
				return swarmError("agente desconhecido: " + agentName), nil
			}
			ag := agents.Get(agentName)
			if ag == nil || ag.Name == "" || !agents.Has(agentName) {
				return swarmError("agente desconhecido: " + agentName), nil
			}
			member, err := team.AddTeammate(swarm.TeammateConfig{Name: name, AgentType: agentName})
			if err != nil {
				return swarmError(err.Error()), nil
			}
			worker := checkpointTeammateExecutor(executor, ag.Name)
			if err := team.StartTeammate(member.Name, worker); err != nil {
				_ = team.RemoveTeammate(member.Name)
				return swarmError(err.Error()), nil
			}
			if prompt := swarmArg(args, "prompt"); prompt != "" {
				msg := swarm.NewMessage(swarm.MessageTaskAssignment, swarm.TeamLeaderName, member.Name, prompt)
				if err := team.Router().SendMessage(msg); err != nil {
					_ = team.RemoveTeammate(member.Name)
					return swarmError(err.Error()), nil
				}
			}
			return swarmJSON(member), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_send" },
		DescriptionFn: func() string {
			return "Enviar uma tarefa ou mensagem a um integrante real da equipe; use * para difundir."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team":    map[string]any{"type": "string", "description": "Equipe; opcional dentro de um integrante"},
				"to":      map[string]any{"type": "string", "description": "Nome do destinatário ou *"},
				"message": map[string]any{"type": "string", "description": "Mensagem ou tarefa"},
				"summary": map[string]any{"type": "string", "description": "Resumo opcional"},
			}, "to", "message")
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			identity, isWorker := swarm.TeammateIdentityFromContext(tc.Context)
			teamName := swarmArg(args, "team")
			if teamName == "" && isWorker {
				teamName = identity.TeamName
			}
			if isWorker && teamName != identity.TeamName {
				return swarmError("integrante não pertence à equipe solicitada"), nil
			}
			team, err := swarmTeam(manager, teamName)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			to, content := swarmArg(args, "to"), swarmArg(args, "message")
			if to == "" || content == "" {
				return swarmError("to e message são obrigatórios"), nil
			}
			sender := swarm.TeamLeaderName
			if isWorker {
				sender = identity.AgentName
			}
			msg := swarm.NewMessage(swarm.MessageChat, sender, to, content)
			msg.Summary = swarmArg(args, "summary")
			if to == "*" {
				recipients := team.Router().BroadcastMessage(msg)
				return swarmJSON(map[string]any{"recipients": recipients, "message_id": msg.ID}), nil
			}
			if err := team.Router().SendMessage(msg); err != nil {
				return swarmError(err.Error()), nil
			}
			return swarmJSON(map[string]any{"to": to, "message_id": msg.ID}), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_inbox" },
		DescriptionFn: func() string {
			return "Ler mensagens pendentes da caixa do líder sem removê-las; confirme depois com team_ack."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team":  map[string]any{"type": "string", "description": "Nome da equipe"},
				"limit": map[string]any{"type": "integer", "description": "Máximo de mensagens, padrão 50 e máximo 200"},
			}, "team")
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if _, isWorker := swarm.TeammateIdentityFromContext(tc.Context); isWorker {
				return swarmError("a caixa do líder é consultada pela sessão principal"), nil
			}
			name := swarmArg(args, "team")
			limit := 50
			if raw, specified := args["limit"]; specified {
				number, ok := raw.(float64)
				if !ok || number < 1 || number > 200 || number != float64(int(number)) {
					return swarmError("limit precisa estar entre 1 e 200"), nil
				}
				limit = int(number)
			}
			if team, ok := manager.GetTeam(name); ok {
				mailbox := team.Router().GetMailbox(swarm.TeamLeaderName)
				if mailbox == nil {
					return swarmError("caixa do líder indisponível"), nil
				}
				return swarmJSON(mailbox.Snapshot(limit)), nil
			}
			archive, err := recordedTeam(manager, name)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			if len(archive.Pending) > limit {
				archive.Pending = archive.Pending[:limit]
			}
			return swarmJSON(archive.Pending), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_ack" },
		DescriptionFn: func() string {
			return "Confirmar IDs de mensagens do líder já processadas, removendo-as da caixa da equipe."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team": map[string]any{"type": "string", "description": "Nome da equipe"},
				"ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "IDs lidos com team_inbox"},
			}, "team", "ids")
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if _, isWorker := swarm.TeammateIdentityFromContext(tc.Context); isWorker {
				return swarmError("a caixa do líder é confirmada pela sessão principal"), nil
			}
			name := swarmArg(args, "team")
			raw, ok := args["ids"].([]any)
			if !ok || len(raw) == 0 || len(raw) > 1024 {
				return swarmError("ids precisa ser uma lista de 1 a 1024 mensagens"), nil
			}
			ids := make([]string, 0, len(raw))
			for _, value := range raw {
				id, valid := value.(string)
				if !valid || strings.TrimSpace(id) == "" {
					return swarmError("ids contém um identificador inválido"), nil
				}
				ids = append(ids, id)
			}
			if team, ok := manager.GetTeam(name); ok {
				mailbox := team.Router().GetMailbox(swarm.TeamLeaderName)
				if mailbox == nil {
					return swarmError("caixa do líder indisponível"), nil
				}
				matched := mailbox.AcknowledgeMatched(ids)
				if journal := manager.Journal(); journal != nil && len(matched) > 0 {
					if err := journal.AcknowledgeLive(team, matched); err != nil {
						return swarmError("mensagens confirmadas localmente, mas falhou o registro persistente: " + err.Error()), nil
					}
				}
				return swarmJSON(map[string]any{"acknowledged": len(matched), "pending": mailbox.Pending()}), nil
			}
			journal := manager.Journal()
			if journal == nil {
				return swarmError("equipe " + name + " não encontrada"), nil
			}
			acknowledged, pending, err := journal.Acknowledge(name, ids)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			return swarmJSON(map[string]any{"acknowledged": acknowledged, "pending": pending}), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "team_status" },
		DescriptionFn: func() string { return "Consultar equipes locais, integrantes e mensagens pendentes." },
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{"team": map[string]any{"type": "string", "description": "Equipe opcional"}})
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name := swarmArg(args, "team")
			if name == "" {
				items, err := manager.ListRecordedTeams()
				if err != nil {
					return swarmError(err.Error()), nil
				}
				return swarmJSON(items), nil
			}
			if team, ok := manager.GetTeam(name); ok {
				pending := 0
				if mailbox := team.Router().GetMailbox(swarm.TeamLeaderName); mailbox != nil {
					pending = mailbox.Pending()
				}
				return swarmJSON(map[string]any{"team": team.Summary(), "members": team.ListMembers(), "leader_pending": pending}), nil
			}
			archive, err := recordedTeam(manager, name)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			return swarmJSON(map[string]any{"team": archive.Summary, "members": archive.Members,
				"leader_pending": len(archive.Pending), "recorded": true,
				"owner_live": archive.OwnerLive}), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_results" },
		DescriptionFn: func() string {
			return "Consultar resultados recentes de um integrante sem consumir a caixa de entrada."
		},
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team": map[string]any{"type": "string", "description": "Nome da equipe"},
				"name": map[string]any{"type": "string", "description": "Nome do integrante"},
			}, "team", "name")
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(_ types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name, agentName := swarmArg(args, "team"), swarmArg(args, "name")
			if team, ok := manager.GetTeam(name); ok {
				if outputs, running := team.TeammateOutputs(agentName); running {
					type result struct {
						Turn     int       `json:"turn"`
						Response string    `json:"response,omitempty"`
						Error    string    `json:"error,omitempty"`
						Started  time.Time `json:"started_at"`
						Finished time.Time `json:"finished_at"`
					}
					items := make([]result, 0, len(outputs))
					for _, output := range outputs {
						item := result{Turn: output.TurnIndex, Response: output.Response, Started: output.StartTime, Finished: output.EndTime}
						if output.Error != nil {
							item.Error = output.Error.Error()
						}
						items = append(items, item)
					}
					return swarmJSON(items), nil
				}
				archive, err := recordedTeam(manager, name)
				if err != nil || archive.RunID != team.RunID() {
					return swarmError("integrante sem executor ativo"), nil
				}
				return swarmJSON(archive.Results[agentName]), nil
			}
			archive, err := recordedTeam(manager, name)
			if err != nil {
				return swarmError(err.Error()), nil
			}
			return swarmJSON(archive.Results[agentName]), nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return "team_stop" },
		DescriptionFn: func() string { return "Encerrar trabalhadores e remover uma equipe local." },
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{"team": map[string]any{"type": "string", "description": "Nome da equipe"}}, "team")
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			name := swarmArg(args, "team")
			if name == "" {
				return swarmError("team é obrigatório"), nil
			}
			if identity, isWorker := swarm.TeammateIdentityFromContext(tc.Context); isWorker && identity.TeamName == name {
				go func() {
					if err := manager.DeleteTeam(name, 15*time.Second); err != nil {
						slog.Warn("team stop requested by worker failed", "team", name, "error", err)
					}
				}()
				return types.ToolResult{Content: "Encerramento da equipe iniciado: " + name}, nil
			}
			if err := manager.DeleteTeam(name, 15*time.Second); err != nil {
				return swarmError(err.Error()), nil
			}
			return types.ToolResult{Content: "Equipe encerrada: " + name}, nil
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "team_resume" },
		DescriptionFn: func() string {
			return "Diagnosticar checkpoints de uma equipe interrompida. Somente leitura: não restaura a equipe, não inicia integrantes e não executa ferramentas. Mostra turnos, chamadas pendentes e bloqueios da retomada."
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		InputSchemaFn: func() map[string]any {
			return swarmSchema(map[string]any{
				"team": map[string]any{"type": "string", "description": "Nome da equipe"},
				"name": map[string]any{"type": "string", "description": "Integrante opcional; vazio avalia todos"},
			}, "team")
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if _, isWorker := swarm.TeammateIdentityFromContext(tc.Context); isWorker {
				return swarmError("a retomada da equipe é feita pela sessão principal"), nil
			}
			name := swarmArg(args, "team")
			if name == "" {
				return swarmError("team é obrigatório"), nil
			}
			report, err := manager.ResumeInterrupted(name, swarmArg(args, "name"))
			if err != nil && len(report.Members) == 0 {
				return swarmError(err.Error()), nil
			}
			if err != nil {
				report.Continued = false
				report.Note = strings.TrimSpace(err.Error() + " " + report.Note)
			}
			return swarmJSON(report), nil
		},
	})
}

func checkpointTeammateExecutor(executor *subagent.SubAgentExecutor, agentName string) swarm.TeammateExecutor {
	return func(ctx context.Context, prompt string, identity swarm.TeammateIdentity) (string, error) {
		ctx = swarm.WithTeammateIdentity(ctx, identity)
		binding, bound := swarm.CheckpointBindingFromContext(ctx)
		if !bound {
			return executor.ExecuteWithCheckpoint(ctx, agentName, prompt, nil, nil, nil)
		}
		return executor.ExecuteWithCheckpoint(ctx, agentName, prompt, binding.Initial, nil, binding.Save)
	}
}

func swarmSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func swarmArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func swarmTeam(manager *swarm.SwarmManager, name string) (*swarm.Team, error) {
	if name == "" {
		return nil, fmt.Errorf("team é obrigatório")
	}
	team, ok := manager.GetTeam(name)
	if !ok {
		return nil, fmt.Errorf("equipe %s não encontrada", name)
	}
	return team, nil
}

func recordedTeam(manager *swarm.SwarmManager, name string) (swarm.ArchivedTeam, error) {
	if name == "" {
		return swarm.ArchivedTeam{}, fmt.Errorf("team é obrigatório")
	}
	journal := manager.Journal()
	if journal == nil {
		return swarm.ArchivedTeam{}, fmt.Errorf("equipe %s não encontrada", name)
	}
	archive, ok, err := journal.Team(name)
	if err != nil {
		return swarm.ArchivedTeam{}, err
	}
	if !ok {
		return swarm.ArchivedTeam{}, fmt.Errorf("equipe %s não encontrada", name)
	}
	return archive, nil
}

func swarmError(message string) types.ToolResult {
	return types.ToolResult{Content: message, IsError: true}
}

func swarmJSON(value any) types.ToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return swarmError("falha ao serializar equipe: " + err.Error())
	}
	return types.ToolResult{Content: string(data)}
}
