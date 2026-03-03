package ai

import (
	"context"
	"log/slog"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/conversation"
)

// KnowledgeSearcher is implemented by the knowledge service.
type KnowledgeSearcher interface {
	Search(ctx context.Context, query string) ([]string, error)
}

// AnswerRequest contains the data needed to generate an answer.
type AnswerRequest struct {
	Channel string
	UserID  string
	Query   string
	History []conversation.Message
}

// AnswerResponse contains the AI response and usage stats.
type AnswerResponse struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Service orchestrates prompt building, knowledge retrieval, and AI completion.
type Service struct {
	client    *Client
	knowledge KnowledgeSearcher
	agentReg  *agent.Registry
	agentName string
	logger    *slog.Logger
}

// NewService creates a new AI service.
func NewService(client *Client, knowledge KnowledgeSearcher, agentReg *agent.Registry, agentName string, logger *slog.Logger) *Service {
	if agentReg == nil {
		agentReg = agent.NewRegistry()
	}
	return &Service{
		client:    client,
		knowledge: knowledge,
		agentReg:  agentReg,
		agentName: agentName,
		logger:    logger,
	}
}

// Answer processes a query through the full pipeline.
func (s *Service) Answer(ctx context.Context, req AnswerRequest) (*AnswerResponse, error) {
	// Fetch agent fresh so runtime changes take effect
	ag := s.agentReg.Get(s.agentName)

	// Update client skill names dynamically
	s.client.SetSkillNames(ag.Skills)

	// Search knowledge base
	var knowledgeCtx string
	if s.knowledge != nil {
		fragments, err := s.knowledge.Search(ctx, req.Query)
		if err != nil {
			s.logger.Warn("knowledge search failed, continuing without context", "error", err)
		} else {
			knowledgeCtx = FormatKnowledgeContext(fragments)
		}
	}

	// Build messages using agent persona
	systemPrompt := BuildSystemPrompt(ag.Persona, knowledgeCtx)
	messages := []ChatMessage{{Role: "system", Content: systemPrompt}}

	// Add conversation history
	for _, m := range req.History {
		messages = append(messages, ChatMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	// Add current query
	messages = append(messages, ChatMessage{Role: "user", Content: req.Query})

	// Call AI (with tool calling if enabled)
	resp, err := s.client.CompleteWithTools(ctx, messages, s.logger)
	if err != nil {
		return nil, err
	}

	s.logger.Debug("ai response",
		"tokens_prompt", resp.PromptTokens,
		"tokens_completion", resp.CompletionTokens,
		"channel", req.Channel,
		"user", req.UserID,
	)

	return &AnswerResponse{
		Content:          resp.Content,
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.TotalTokens,
	}, nil
}
