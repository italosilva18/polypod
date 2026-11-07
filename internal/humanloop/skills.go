package humanloop

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills exposes durable inbox and notification controls to the agent.
func RegisterSkills(reg *skill.Registry, store *Store, wake func()) {
	reg.Register(&skill.Skill{
		Name:        "heartbeat_status",
		Description: "Consultar o heartbeat e o estado duravel da caixa de entrada operacional",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(map[string]string) (string, error) {
			state, messages := store.Snapshot()
			counts := map[Status]int{}
			for _, msg := range messages {
				counts[msg.Status]++
			}
			return fmt.Sprintf("Ultimo heartbeat: %s\nPendentes: %d | processando: %d | concluidas: %d | falhas: %d | resolvidas: %d\nUltimo erro: %s",
				state.LastHeartbeat.Format("2006-01-02 15:04:05"), counts[StatusPending], counts[StatusProcessing],
				counts[StatusCompleted], counts[StatusFailed], counts[StatusResolved], state.LastError), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "inbox_list",
		Description: "Listar mensagens e resultados recentes da caixa de entrada operacional",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"status": {Type: jsonschema.String, Description: "Filtro opcional: pending, processing, completed, failed ou resolved"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			_, messages := store.Snapshot()
			filter := Status(strings.TrimSpace(args["status"]))
			selected := make([]Message, 0, len(messages))
			for _, msg := range messages {
				if filter == "" || msg.Status == filter {
					selected = append(selected, msg)
				}
			}
			raw, err := json.MarshalIndent(selected, "", "  ")
			if err != nil {
				return "", fmt.Errorf("serializando inbox: %w", err)
			}
			return string(raw), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "inbox_resolve",
		Description: "Revisar ou encerrar com motivo um item com falha terminal, sem reexecutar a acao nem afirmar sucesso",
		Parameters: jsonschema.Definition{Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id":     {Type: jsonschema.String, Description: "ID interno do item com falha"},
				"mode":   {Type: jsonschema.String, Description: "preview (padrao) ou apply"},
				"reason": {Type: jsonschema.String, Description: "Motivo objetivo obrigatorio para apply"},
			}, Required: []string{"id"}},
		Execute: func(args map[string]string) (string, error) {
			id := strings.TrimSpace(args["id"])
			preview, err := store.PreviewFailedResolution(id)
			if err != nil {
				return "", err
			}
			mode := strings.TrimSpace(args["mode"])
			if mode == "" || mode == "preview" {
				return fmt.Sprintf("Falha %s: direcao=%s canal=%s tentativas=%d desde=%s destinos_entregues=%s midia=%t replays=%d. Use mode=apply e reason para encerrar sem replay.",
					id, preview.Direction, preview.Channel, preview.Attempts, preview.FailedAt.Format(time.RFC3339),
					strings.Join(preview.Delivered, ","), preview.HasMedia, preview.PriorReplays), nil
			}
			if mode != "apply" {
				return "", fmt.Errorf("modo invalido: use preview ou apply")
			}
			if err := store.ResolveFailed(id, "agent", args["reason"], time.Now()); err != nil {
				return "", err
			}
			return fmt.Sprintf("Falha %s resolvida com registro de motivo; nenhuma acao foi repetida", id), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "notify_user",
		Description: "Enfileirar uma notificacao duravel local e remota para o usuario",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"message": {Type: jsonschema.String, Description: "Mensagem objetiva para o usuario"},
			},
			Required: []string{"message"},
		},
		Execute: func(args map[string]string) (string, error) {
			msg, err := store.EnqueueOutbound("notification", "owner", strings.TrimSpace(args["message"]), "")
			if err != nil {
				return "", err
			}
			if wake != nil {
				wake()
			}
			return fmt.Sprintf("Notificacao enfileirada com ID %s", msg.ID), nil
		},
	})
}
