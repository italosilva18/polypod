package ai

import (
	"context"
	"log/slog"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/compact"
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
	client      *Client
	knowledge   KnowledgeSearcher
	agentReg    *agent.Registry
	agentName   string
	memStore    memory.Store
	logger      *slog.Logger
	autoCompact *compact.AutoCompactService
	onActivity  func()
}

// SetActivityCallback registers a lightweight hook invoked whenever a user or
// routine starts an AI turn. The dreaming service uses it to avoid learning
// from a conversation while that conversation is still active.
func (s *Service) SetActivityCallback(fn func()) {
	s.onActivity = fn
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

// SetModel switches the underlying client's model. Used by the
// interactive model picker so users can swap models mid-session
// without restarting polypod.
func (s *Service) SetModel(model string) {
	if s.client != nil {
		s.client.SetModel(model)
	}
}

// CurrentModel returns the model ID currently configured on the
// underlying client. Returns "" when no client is wired.
func (s *Service) CurrentModel() string {
	if s.client == nil {
		return ""
	}
	return s.client.Model()
}

// AgentRegistry returns the agent registry.
func (s *Service) AgentRegistry() *agent.Registry {
	return s.agentReg
}

// SetAutoCompact attaches an AutoCompactService to the AI service.
// When set, buildMessages will check whether the conversation history needs
// compaction before sending messages to the model.
func (s *Service) SetAutoCompact(svc *compact.AutoCompactService) {
	s.autoCompact = svc
}

// SetToolEventCallback sets a callback for tool lifecycle events on the
// underlying client. This allows adapters (e.g. CLI) to display real-time
// tool execution progress.
func (s *Service) SetToolEventCallback(cb ToolEventCallback) {
	s.client.SetToolEventCallback(cb)
}

// buildMessages assembles the full message list for a request.
func (s *Service) buildMessages(ctx context.Context, req AnswerRequest) ([]ChatMessage, []string) {
	ag := s.agentReg.Get(s.agentName)

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

	// Auto-compact conversation history if needed
	history := req.History
	if s.autoCompact != nil && s.autoCompact.IsEnabled() {
		tokenEstimate := compact.EstimateTokens(history)
		if s.autoCompact.Check(tokenEstimate) {
			s.logger.Info("auto-compacting conversation history",
				"history_len", len(history),
				"estimated_tokens", tokenEstimate,
			)
			compacted, err := s.autoCompact.CompactHistory(ctx, history)
			if err != nil {
				s.logger.Warn("auto-compact failed, using original history", "error", err)
			} else {
				s.logger.Info("auto-compact succeeded",
					"original_messages", len(history),
					"compacted_messages", len(compacted),
				)
				history = compacted
			}
		}
	}

	// Add conversation history
	for _, m := range history {
		messages = append(messages, ChatMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	// Add current query
	messages = append(messages, ChatMessage{Role: "user", Content: req.Query})
	return messages, ag.Skills
}

// Answer processes a query through the full pipeline (synchronous).
func (s *Service) Answer(ctx context.Context, req AnswerRequest) (*AnswerResponse, error) {
	if s.onActivity != nil {
		s.onActivity()
	}
	messages, names := s.buildMessages(ctx, req)

	resp, err := s.client.CompleteWithTools(withRequestSkillNames(ctx, names), messages, s.logger)
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
	if s.onActivity != nil {
		s.onActivity()
	}
	messages, names := s.buildMessages(ctx, req)

	resp, err := s.client.CompleteWithToolsStream(withRequestSkillNames(ctx, names), messages, callback, s.logger)
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
