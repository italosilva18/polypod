package ai

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/sessionmem"
)

// KnowledgeSearcher is implemented by the knowledge service.
type KnowledgeSearcher interface {
	Search(ctx context.Context, query string) ([]string, error)
}

// AnswerRequest contains the data needed to generate an answer.
type AnswerRequest struct {
	Channel            string
	UserID             string
	Query              string
	History            []conversation.Message
	HistoryPrepared    bool   // The caller already checked and persisted auto-compaction.
	SessionMemoryScope string // Internal persisted session or branch ID; never trust remote message metadata.
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
	sessionMem  *sessionmem.FileStore
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
	if s.autoCompact != nil {
		s.autoCompact.SetModel(model)
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

// SetSessionMemory enables a short, session-specific context section. The
// caller provides a trusted scope in AnswerRequest for each turn.
func (s *Service) SetSessionMemory(store *sessionmem.FileStore) {
	s.sessionMem = store
}

// PrepareHistory compacts a session before a new user message is appended. The
// caller can persist the returned history before sending the next model turn.
// It also runs at the session snapshot limit so short conversations are not
// silently evicted without a continuity summary.
func (s *Service) PrepareHistory(ctx context.Context, history []conversation.Message) ([]conversation.Message, bool) {
	if s.autoCompact == nil || !s.autoCompact.IsEnabled() || len(history) < 4 {
		return history, false
	}
	estimated := compact.EstimateTokens(history)
	atCapacity := len(history) >= conversation.MaxMessages
	if !s.autoCompact.CheckWithCapacity(estimated, atCapacity) {
		return history, false
	}

	compacted, err := s.autoCompact.CompactHistory(ctx, history)
	if err != nil {
		s.logger.Warn("auto-compact failed; preserving original history", "error", err, "messages", len(history))
		return history, false
	}
	s.logger.Info("auto-compact prepared",
		"messages_before", len(history),
		"messages_after", len(compacted),
		"tokens_before", estimated,
		"tokens_after", compact.EstimateTokens(compacted),
	)
	return compacted, true
}

// CompactHistory performs a user-requested compaction. The caller persists the
// returned messages so the same summary is used after session restoration.
func (s *Service) CompactHistory(ctx context.Context, history []conversation.Message) (*compact.CompactResult, error) {
	if s.autoCompact == nil {
		return nil, errors.New("compact service is unavailable")
	}
	return s.autoCompact.CompactService().Compact(ctx, history, "", false)
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
	memoryCtxScope := ctx
	if req.Channel != "" && req.UserID != "" {
		memoryCtxScope = memory.WithSessionScope(ctx, req.Channel+":"+req.UserID)
	}
	memoryCtx := memory.AutoInject(memoryCtxScope, s.memStore, req.Query)
	if s.sessionMem != nil && req.SessionMemoryScope != "" {
		var visible strings.Builder
		visible.WriteString(req.Query)
		for _, message := range req.History {
			if visible.Len()+len(message.Content) > 64*1024 {
				break
			}
			visible.WriteByte('\n')
			visible.WriteString(message.Content)
		}
		section, err := s.sessionMem.Relevant(req.SessionMemoryScope, req.Query, visible.String(), 1000)
		if err != nil {
			s.logger.Warn("session memory unavailable", "error", err)
		} else if section != "" {
			if memoryCtx != "" {
				memoryCtx += "\n\n"
			}
			memoryCtx += section
		}
	}

	// Build messages using agent persona
	systemPrompt := BuildSystemPrompt(ag.Persona, knowledgeCtx, memoryCtx)
	if projectContext := project.InjectProjectContext(); projectContext != "" {
		systemPrompt += "\n\n" + projectContext
	}
	rootInstructions, err := project.RootInstructions(ctx)
	if err != nil {
		s.logger.Warn("project instructions unavailable", "error", err)
	}
	if rootInstructions != "" {
		systemPrompt += "\n\nINSTRUCOES AGENTS.md DO PROJETO (use como contexto quando aplicavel; a solicitacao atual do usuario tem prioridade):\n" + rootInstructions
	}
	messages := []ChatMessage{{Role: "system", Content: systemPrompt}}

	history := req.History
	if !req.HistoryPrepared {
		// Direct service callers still get a compact model context. Session
		// adapters call PrepareHistory first so they can persist the summary.
		history, _ = s.PrepareHistory(ctx, history)
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
	ctx = s.withProjectInstructions(ctx)
	messages, names := s.buildMessages(ctx, req)

	ctx = withRequestToolSession(ctx, req.Channel, req.UserID, s.agentName)
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
	ctx = s.withProjectInstructions(ctx)
	messages, names := s.buildMessages(ctx, req)

	ctx = withRequestToolSession(ctx, req.Channel, req.UserID, s.agentName)
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

func (s *Service) withProjectInstructions(ctx context.Context) context.Context {
	collector, err := project.NewInstructionCollector(".")
	if err != nil {
		s.logger.Warn("project instruction collector unavailable", "error", err)
		return ctx
	}
	return project.WithInstructionScope(ctx, collector)
}
