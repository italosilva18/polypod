package router

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/ai"
	"github.com/costa/polypod/internal/auth"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/ratelimit"
)

// Router is the central dispatcher that connects adapters to the AI pipeline.
type Router struct {
	convMgr  *conversation.Manager
	aiSvc    *ai.Service
	authz    *auth.Authorizer
	limiter  *ratelimit.Limiter
	pool     *pgxpool.Pool
	sqliteDB *sql.DB
	logger   *slog.Logger
}

// New creates a new router.
func New(
	convMgr *conversation.Manager,
	aiSvc *ai.Service,
	authz *auth.Authorizer,
	limiter *ratelimit.Limiter,
	pool *pgxpool.Pool,
	sqliteDB *sql.DB,
	logger *slog.Logger,
) *Router {
	return &Router{
		convMgr:  convMgr,
		aiSvc:    aiSvc,
		authz:    authz,
		limiter:  limiter,
		pool:     pool,
		sqliteDB: sqliteDB,
		logger:   logger,
	}
}

// Handler returns a MessageHandler that processes messages through the pipeline.
func (r *Router) Handler() adapter.MessageHandler {
	return func(ctx context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		out := adapter.OutMessage{
			Channel: msg.Channel,
			UserID:  msg.UserID,
			ReplyTo: msg.ID,
		}

		// Auth check
		if !r.authz.IsAllowed(msg.Channel, msg.UserID) {
			r.logger.Warn("unauthorized access", "channel", msg.Channel, "user", msg.UserID)
			out.Text = "Acesso nao autorizado."
			return out, nil
		}

		// Rate limit check
		rateLimitKey := fmt.Sprintf("%s:%s", msg.Channel, msg.UserID)
		if !r.limiter.Allow(rateLimitKey) {
			r.logger.Warn("rate limited", "channel", msg.Channel, "user", msg.UserID)
			out.Text = "Muitas mensagens. Aguarde um momento antes de enviar outra."
			return out, nil
		}

		// Get or create session
		sess, err := r.convMgr.GetSession(ctx, msg.Channel, msg.UserID)
		if err != nil {
			r.logger.Error("session error", "error", err)
			out.Text = "Erro interno. Tente novamente."
			return out, nil
		}

		// Add user message to history
		r.convMgr.AddUserMessage(ctx, sess, msg.Text)

		// Get AI answer
		resp, err := r.aiSvc.Answer(ctx, ai.AnswerRequest{
			Channel: msg.Channel,
			UserID:  msg.UserID,
			Query:   msg.Text,
			History: r.convMgr.GetHistory(sess),
		})
		if err != nil {
			r.logger.Error("ai error", "error", err)
			out.Text = "Desculpe, ocorreu um erro ao processar sua mensagem."
			return out, nil
		}

		// Save assistant response
		if err := r.convMgr.AddAssistantMessage(ctx, sess, resp.Content); err != nil {
			r.logger.Warn("failed to save response", "error", err)
		}

		// Log usage to database
		r.logUsage(ctx, msg.Channel, msg.UserID, resp)

		r.logger.Info("message processed",
			"channel", msg.Channel,
			"user", msg.UserID,
			"tokens", resp.TotalTokens,
		)

		out.Text = resp.Content
		return out, nil
	}
}

func (r *Router) logUsage(ctx context.Context, channel, userID string, resp *ai.AnswerResponse) {
	if r.pool != nil {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO usage_log (channel, user_id, model, prompt_tokens, completion_tokens, total_tokens)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, channel, userID, "deepseek-chat", resp.PromptTokens, resp.CompletionTokens, resp.TotalTokens)
		if err != nil {
			r.logger.Warn("failed to log usage", "error", err)
		}
		return
	}

	if r.sqliteDB != nil {
		_, err := r.sqliteDB.ExecContext(ctx, `
			INSERT INTO usage_log (channel, user_id, model, prompt_tokens, completion_tokens, total_tokens)
			VALUES (?, ?, ?, ?, ?, ?)
		`, channel, userID, "deepseek-chat", resp.PromptTokens, resp.CompletionTokens, resp.TotalTokens)
		if err != nil {
			r.logger.Warn("failed to log usage", "error", err)
		}
	}
}
