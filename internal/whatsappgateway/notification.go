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
	reg.Register(&skill.Skill{
		Name:        "whatsapp_send_media",
		Description: "Enfileirar imagem, documento ou audio OGG/Opus de arquivo local para o chat pessoal configurado do usuario",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path":    {Type: jsonschema.String, Description: "Caminho do arquivo local"},
				"caption": {Type: jsonschema.String, Description: "Legenda opcional; deixe vazia para audio"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			path := strings.TrimSpace(args["path"])
			if path == "" {
				return "", errors.New("whatsapp: caminho de midia vazio")
			}
			queued, err := gateway.QueueLocalFile(ownerChat, path, strings.TrimSpace(args["caption"]))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Midia WhatsApp enfileirada com ID %s", queued.ID), nil
		},
	})
	return nil
}
