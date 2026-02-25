package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/costa/polypod/internal/adapter"
)

// Adapter implements the Telegram channel using long polling.
type Adapter struct {
	token  string
	logger *slog.Logger
}

// New creates a new Telegram adapter.
func New(token string, logger *slog.Logger) *Adapter {
	return &Adapter{
		token:  token,
		logger: logger,
	}
}

func (a *Adapter) Name() string { return "telegram" }

func (a *Adapter) Start(ctx context.Context, handler adapter.MessageHandler) error {
	b, err := bot.New(a.token,
		bot.WithDefaultHandler(a.makeHandler(handler)),
	)
	if err != nil {
		return fmt.Errorf("creating telegram bot: %w", err)
	}

	a.logger.Info("telegram bot starting (long polling)")
	b.Start(ctx)
	return nil
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutMessage) error {
	// Sending is handled inline in the handler via the bot reference.
	// This method is for out-of-band messages if needed.
	return nil
}

func (a *Adapter) makeHandler(handler adapter.MessageHandler) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}

		msg := update.Message
		userID := strconv.FormatInt(msg.From.ID, 10)
		userName := msg.From.FirstName
		if msg.From.Username != "" {
			userName = msg.From.Username
		}

		in := adapter.InMessage{
			ID:        strconv.Itoa(msg.ID),
			Channel:   "telegram",
			UserID:    userID,
			UserName:  userName,
			Text:      msg.Text,
			Timestamp: time.Unix(int64(msg.Date), 0),
		}

		out, err := handler(ctx, in)
		if err != nil {
			a.logger.Error("handler error", "error", err, "user", userID)
			return
		}

		if out.Text == "" {
			return
		}

		_, err = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: msg.Chat.ID,
			Text:   out.Text,
		})
		if err != nil {
			a.logger.Error("failed to send telegram message", "error", err, "chat", msg.Chat.ID)
		}
	}
}
