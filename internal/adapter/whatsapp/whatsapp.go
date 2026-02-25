package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	greenapi "github.com/green-api/whatsapp-api-client-golang/pkg/api"

	"github.com/costa/polypod/internal/adapter"
)

// Adapter implements the WhatsApp channel via GREEN-API.
type Adapter struct {
	client greenapi.GreenAPI
	logger *slog.Logger
}

// New creates a new WhatsApp adapter.
func New(idInstance, apiToken string, logger *slog.Logger) *Adapter {
	return &Adapter{
		client: greenapi.GreenAPI{
			IDInstance:       idInstance,
			APITokenInstance: apiToken,
		},
		logger: logger,
	}
}

func (a *Adapter) Name() string { return "whatsapp" }

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	a.logger.Info("whatsapp adapter starting (polling)")

	receiving := a.client.Methods().Receiving()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		notification, err := receiving.ReceiveNotification()
		if err != nil {
			a.logger.Warn("whatsapp receive error", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// No notification available
		if notification == nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Second):
			}
			continue
		}

		receiptID := 0
		if rid, ok := notification["receiptId"].(float64); ok {
			receiptID = int(rid)
		}

		// Process incoming message
		if body, ok := notification["body"].(map[string]interface{}); ok {
			a.processNotification(ctx, body, handler)
		}

		// Delete notification after processing
		if receiptID > 0 {
			if _, err := receiving.DeleteNotification(receiptID); err != nil {
				a.logger.Warn("failed to delete notification", "receipt", receiptID, "error", err)
			}
		}
	}
}

func (a *Adapter) processNotification(ctx context.Context, body map[string]interface{}, handler adapter.MessageHandler) {
	typeWebhook, _ := body["typeWebhook"].(string)
	if typeWebhook != "incomingMessageReceived" {
		return
	}

	senderData, _ := body["senderData"].(map[string]interface{})
	messageData, _ := body["messageData"].(map[string]interface{})
	if senderData == nil || messageData == nil {
		return
	}

	chatID, _ := senderData["chatId"].(string)
	senderName, _ := senderData["senderName"].(string)

	// Only handle text messages
	typeMessage, _ := messageData["typeMessage"].(string)
	if typeMessage != "textMessage" {
		return
	}

	textData, _ := messageData["textMessageData"].(map[string]interface{})
	if textData == nil {
		return
	}
	text, _ := textData["textMessage"].(string)
	if text == "" {
		return
	}

	idMessage, _ := body["idMessage"].(string)

	in := adapter.InMessage{
		ID:        idMessage,
		Channel:   "whatsapp",
		UserID:    chatID,
		UserName:  senderName,
		Text:      text,
		Timestamp: time.Now(),
	}

	out, err := handler(ctx, in)
	if err != nil {
		a.logger.Error("handler error", "error", err, "user", chatID)
		return
	}

	if out.Text != "" {
		if err := a.Send(ctx, out); err != nil {
			a.logger.Error("failed to send whatsapp message", "error", err, "chat", chatID)
		}
	}
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	response, err := a.client.Methods().Sending().SendMessage(map[string]interface{}{
		"chatId":  msg.UserID,
		"message": msg.Text,
	})
	if err != nil {
		return fmt.Errorf("sending whatsapp message: %w", err)
	}

	a.logger.Debug("whatsapp message sent", "response", response)
	return nil
}
