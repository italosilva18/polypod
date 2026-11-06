package humanloop

import (
	"encoding/json"
	"fmt"
	"strings"

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
			return fmt.Sprintf("Ultimo heartbeat: %s\nPendentes: %d | processando: %d | concluidas: %d | falhas: %d\nUltimo erro: %s",
				state.LastHeartbeat.Format("2006-01-02 15:04:05"), counts[StatusPending], counts[StatusProcessing], counts[StatusCompleted], counts[StatusFailed], state.LastError), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "inbox_list",
		Description: "Listar mensagens e resultados recentes da caixa de entrada operacional",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"status": {Type: jsonschema.String, Description: "Filtro opcional: pending, processing, completed ou failed"},
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
