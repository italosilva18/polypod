package tts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/humanloop"
	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

type VoiceSynthesizer interface {
	Synthesize(context.Context, string) ([]byte, error)
}

type MediaQueue interface {
	QueueMedia(chatID, caption, filename, mimeType string, data []byte) (humanloop.Message, error)
}

// RegisterWhatsAppVoiceSkill gives the agent a voice-note tool for the owner's
// allowlisted WhatsApp chat. The queue persists audio before the gateway sends it.
func RegisterWhatsAppVoiceSkill(reg *skill.Registry, queue MediaQueue, ownerChat string, synth VoiceSynthesizer) error {
	if reg == nil || queue == nil || synth == nil || strings.TrimSpace(ownerChat) == "" {
		return errors.New("voz WhatsApp requer registro, fila, sintetizador e chat do dono")
	}
	reg.Register(&skill.Skill{
		Name:        "whatsapp_send_voice",
		Description: "Sintetizar fala em português e enfileirar uma mensagem de voz para o WhatsApp do usuário",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"text": {Type: jsonschema.String, Description: "Texto a falar, até 1200 caracteres"},
			},
			Required: []string{"text"},
		},
		ExecuteResult: func(ctx context.Context, args map[string]any) (types.ToolResult, error) {
			text, _ := args["text"].(string)
			audio, err := synth.Synthesize(ctx, text)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("Falha ao sintetizar voz: %v", err)}, nil
			}
			queued, err := queue.QueueMedia(ownerChat, "", "voz.ogg", "audio/ogg; codecs=opus", audio)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("Falha ao enfileirar voz: %v", err)}, nil
			}
			return types.ToolResult{Content: fmt.Sprintf("Mensagem de voz enfileirada com ID %s", queued.ID)}, nil
		},
	})
	return nil
}
