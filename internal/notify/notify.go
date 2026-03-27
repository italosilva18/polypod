package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Channel is the interface for notification delivery.
type Channel interface {
	Send(ctx context.Context, userID, message string) error
	Name() string
}

// TelegramNotifier sends via Telegram Bot API.
type TelegramNotifier struct {
	token  string
	logger *slog.Logger
}

func NewTelegramNotifier(token string, logger *slog.Logger) *TelegramNotifier {
	return &TelegramNotifier{token: token, logger: logger}
}

func (t *TelegramNotifier) Name() string { return "telegram" }

func (t *TelegramNotifier) Send(ctx context.Context, chatID, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)
	body, _ := json.Marshal(map[string]string{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": "Markdown",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram HTTP %d", resp.StatusCode)
	}
	return nil
}

// WhatsAppNotifier sends via Green API.
type WhatsAppNotifier struct {
	idInstance string
	apiToken   string
	logger     *slog.Logger
}

func NewWhatsAppNotifier(idInstance, apiToken string, logger *slog.Logger) *WhatsAppNotifier {
	return &WhatsAppNotifier{idInstance: idInstance, apiToken: apiToken, logger: logger}
}

func (w *WhatsAppNotifier) Name() string { return "whatsapp" }

func (w *WhatsAppNotifier) Send(ctx context.Context, chatID, message string) error {
	url := fmt.Sprintf("https://api.green-api.com/waInstance%s/sendMessage/%s", w.idInstance, w.apiToken)
	body, _ := json.Marshal(map[string]string{
		"chatId":  chatID,
		"message": message,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whatsapp HTTP %d", resp.StatusCode)
	}
	return nil
}

// WebhookNotifier sends via HTTP POST.
type WebhookNotifier struct {
	url    string
	logger *slog.Logger
}

func NewWebhookNotifier(url string, logger *slog.Logger) *WebhookNotifier {
	return &WebhookNotifier{url: url, logger: logger}
}

func (wh *WebhookNotifier) Name() string { return "webhook" }

func (wh *WebhookNotifier) Send(ctx context.Context, _ string, message string) error {
	body, _ := json.Marshal(map[string]string{
		"text":      message,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, "POST", wh.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("webhook send: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Dispatcher routes notifications to the right channel.
type Dispatcher struct {
	channels map[string]Channel
	logger   *slog.Logger
}

func NewDispatcher(logger *slog.Logger) *Dispatcher {
	return &Dispatcher{channels: make(map[string]Channel), logger: logger}
}

func (d *Dispatcher) Register(ch Channel) { d.channels[ch.Name()] = ch }

func (d *Dispatcher) Send(ctx context.Context, channel, userID, message string) error {
	ch, ok := d.channels[channel]
	if !ok {
		return fmt.Errorf("canal '%s' nao configurado", channel)
	}
	return ch.Send(ctx, userID, message)
}

func (d *Dispatcher) Broadcast(ctx context.Context, userID, message string) error {
	var errs []string
	for _, ch := range d.channels {
		if err := ch.Send(ctx, userID, message); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ch.Name(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("erros: %s", strings.Join(errs, "; "))
	}
	return nil
}

// RegisterSkills registers notification skills.
func RegisterSkills(reg *skill.Registry, disp *Dispatcher) {
	reg.Register(&skill.Skill{
		Name:        "notify_send",
		Description: "Enviar notificacao por um canal especifico (telegram, whatsapp, webhook)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"channel": {Type: jsonschema.String, Description: "Canal: telegram, whatsapp, webhook"},
				"user_id": {Type: jsonschema.String, Description: "ID do destinatario (chat_id, numero, etc)"},
				"message": {Type: jsonschema.String, Description: "Mensagem a enviar"},
			},
			Required: []string{"channel", "user_id", "message"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := disp.Send(context.Background(), args["channel"], args["user_id"], args["message"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Notificacao enviada via %s para %s.", args["channel"], args["user_id"]), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "notify_broadcast",
		Description: "Enviar notificacao para todos os canais configurados",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"user_id": {Type: jsonschema.String, Description: "ID do destinatario"},
				"message": {Type: jsonschema.String, Description: "Mensagem"},
			},
			Required: []string{"message"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := disp.Broadcast(context.Background(), args["user_id"], args["message"]); err != nil {
				return "", err
			}
			return "Notificacao enviada para todos os canais.", nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "notify_channels",
		Description: "Listar canais de notificacao configurados",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			if len(disp.channels) == 0 {
				return "Nenhum canal de notificacao configurado.", nil
			}
			var names []string
			for n := range disp.channels {
				names = append(names, n)
			}
			return "Canais disponiveis: " + strings.Join(names, ", "), nil
		},
	})
}
