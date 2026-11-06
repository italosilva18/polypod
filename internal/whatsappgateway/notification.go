package whatsappgateway

import (
	"errors"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterNotificationSkill lets the agent queue a message to one configured,
// allowlisted direct chat. Registration never sends a WhatsApp message.
func RegisterNotificationSkill(reg *skill.Registry, gateway *Gateway, ownerChat string) error {
	if reg == nil || gateway == nil || gateway.store == nil {
		return errors.New("whatsapp: registro de notificacao exige registro e gateway")
	}
	if ownerChat == "" || !gateway.chats[ownerChat] {
		return errors.New("whatsapp: owner_chat deve ser um chat permitido")
	}
	reg.Register(&skill.Skill{
		Name:        "whatsapp_notify",
		Description: "Enfileirar uma mensagem WhatsApp para o chat pessoal configurado do usuario",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"message": {Type: jsonschema.String, Description: "Mensagem para o usuario"},
			},
			Required: []string{"message"},
		},
		Execute: func(args map[string]string) (string, error) {
			message := strings.TrimSpace(args["message"])
			if message == "" {
				return "", errors.New("whatsapp: mensagem vazia")
			}
			queued, err := gateway.store.EnqueueOutbound(Channel, ownerChat, message, "")
			if err != nil {
				return "", fmt.Errorf("whatsapp: enfileirando notificacao: %w", err)
			}
			if gateway.wake != nil {
				gateway.wake()
			}
			return fmt.Sprintf("Notificacao WhatsApp enfileirada com ID %s", queued.ID), nil
		},
	})
	return nil
}
