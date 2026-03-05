package ai

import (
	"context"
	"log/slog"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
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
	memStore  memory.Store
	logger    *slog.Logger
}

// NewService creates a new AI service.
func NewService(client *Client, knowledge KnowledgeSearcher, agentReg *agent.Registry, agentName string, memStore memory.Store, logger *slog.Logger) *Service {
	if agentReg == nil {
		agentReg = agent.NewRegistry()
	}
	return &Service{
		client:    client,
		knowledge: knowledge,
		agentReg:  agentReg,
		agentName: agentName,
		memStore:  memStore,
		logger:    logger,
	}
}

// SetAgent switches the active agent by name.
func (s *Service) SetAgent(name string) {
	s.agentName = name
}

// ActiveAgent returns the name of the currently active agent.
func (s *Service) ActiveAgent() string {
	return s.agentName
}

// AgentRegistry returns the agent registry.
func (s *Service) AgentRegistry() *agent.Registry {
	return s.agentReg
}

// buildMessages assembles the full message list for a request.
func (s *Service) buildMessages(ctx context.Context, req AnswerRequest) []ChatMessage {
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

	// Auto-inject relevant memories
	memoryCtx := memory.AutoInject(ctx, s.memStore, req.Query)

	// Build messages using agent persona
	systemPrompt := BuildSystemPrompt(ag.Persona, knowledgeCtx, memoryCtx)
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
	return messages
}

// Answer processes a query through the full pipeline (synchronous).
func (s *Service) Answer(ctx context.Context, req AnswerRequest) (*AnswerResponse, error) {
	messages := s.buildMessages(ctx, req)

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

// AnswerStream processes a query through the full pipeline with streaming.
// callback is called for each content delta token.
func (s *Service) AnswerStream(ctx context.Context, req AnswerRequest, callback StreamCallback) (*AnswerResponse, error) {
	messages := s.buildMessages(ctx, req)

	resp, err := s.client.CompleteWithToolsStream(ctx, messages, callback, s.logger)
	if err != nil {
		return nil, err
	}

	s.logger.Debug("ai stream response",
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
