package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
	"github.com/costa/polypod/internal/project"
	"github.com/costa/polypod/internal/sessionmem"
	"github.com/costa/polypod/internal/smartroute"
	"github.com/costa/polypod/internal/watcher"
	"github.com/costa/polypod/internal/workdir"
)

// KnowledgeSearcher is implemented by the knowledge service.
type KnowledgeSearcher interface {
	Search(ctx context.Context, query, channel, userID string) ([]string, error)
}

// AnswerRequest contains the data needed to generate an answer.
type AnswerRequest struct {
	Channel               string
	UserID                string
	RoutingCategory       string // Trusted internal evaluation category; empty uses known channel mapping.
	Effort                string // Optional reasoning effort override ("low", "medium", "high", "max").
	Query                 string
	CurrentMessageID      string // Durable journal ID of Query, when it was saved before this turn.
	History               []conversation.Message
	HistoryPrepared       bool   // The caller already checked and persisted auto-compaction.
	SessionMemoryScope    string // Internal persisted session or branch ID; never trust remote message metadata.
	ToolTranscriptSummary string // Short historical metadata; never executable tool calls or raw results.
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
	client              *Client
	knowledge           KnowledgeSearcher
	agentReg            *agent.Registry
	agentName           string
	memStore            memory.Store
	journal             *conversation.Store
	sessionMem          *sessionmem.FileStore
	logger              *slog.Logger
	autoCompact         *compact.AutoCompactService
	onActivity          func()
	workdirs            *workdir.Store
	routingMu           sync.RWMutex // protects routingPolicy and manualModel
	routingPolicy       *smartroute.CalibratedPolicy
	manualModel         bool
	experienceMu        sync.RWMutex // protects experienceSearcher and experienceOwnerChat
	experienceSearcher  ExperienceSearcher
	experienceOwnerChat string
	repoPlaybook        *RepoPlaybookStore
	externalChanges     *watcher.ExternalChangeBuffer
	// Budget-triggered compaction is retried after a short delay when the
	// summarizer is unavailable. Each session has its own delay so one failed
	// summary does not hold back another session. Capacity-triggered compaction
	// remains active.
	historyBudgetMu      sync.Mutex
	historyBudgetRetryAt map[string]time.Time
}

// SetEffort sets the default reasoning effort ("low", "medium", "high", "max")
// on the underlying AI client.
func (s *Service) SetEffort(effort string) {
	if s.client != nil {
		s.client.SetEffort(effort)
	}
}

// SetRepoPlaybook enables verified repository playbook lesson injection and
// automatic recovery-episode recording on the underlying client.
func (s *Service) SetRepoPlaybook(store *RepoPlaybookStore) {
	s.repoPlaybook = store
	if s.client != nil {
		s.client.SetRepoPlaybook(store)
	}
}

// SetExternalChangeBuffer attaches the live workspace FileChanged buffer so
// external edits/deletions are injected into the model's context before the next turn.
func (s *Service) SetExternalChangeBuffer(buf *watcher.ExternalChangeBuffer) {
	s.externalChanges = buf
}

// SetVisionModel configures an explicit multimodal fallback/override model (`/model --vision`).
func (s *Service) SetVisionModel(providerName, model, apiKey, baseURL string) error {
	if s.client == nil {
		return fmt.Errorf("serviço de IA indisponível")
	}
	return s.client.SetVisionModel(providerName, model, apiKey, baseURL)
}

// VisionModelConfig returns any explicit vision override plus the effective auto-picked fallback.
func (s *Service) VisionModelConfig() (explicitProvider, explicitModel, effectiveAutoModel string) {
	if s.client == nil {
		return "", "", ""
	}
	return s.client.VisionModelConfig()
}

// LatestWaterfall formats the active or most recent trajectory waterfall (`/waterfall`).
func (s *Service) LatestWaterfall() string {
	if s.client == nil {
		return "Serviço de IA indisponível."
	}
	wf, ok := s.client.Waterfall().Latest()
	if !ok {
		return "Nenhum turno registrado na cascata de execução (/waterfall) ainda."
	}
	return FormatWaterfall(wf)
}

// AnswerTextOnly generates a one-shot answer without exposing any tools or
// adding messages to the persistent conversation.
func (s *Service) AnswerTextOnly(ctx context.Context, prompt string) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("serviço de IA indisponível")
	}
	response, err := s.client.Complete(ctx, []ChatMessage{{Role: "user", Content: prompt}})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

const maxHistoryBudgetCooldowns = 1024

// SetWorkdirStore enables conversation-scoped working directories for tools.
func (s *Service) SetWorkdirStore(store *workdir.Store) { s.workdirs = store }

// WorkingDir resolves the same conversation scope used by model tools. Local
// CLI commands can use it without changing the process-wide working directory.
func (s *Service) WorkingDir(channel, userID, sessionID, fallback string) string {
	ctx := workdir.WithScope(context.Background(), s.workdirs, channel, userID, sessionID)
	return workdir.Dir(ctx, fallback)
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
	if model == "" {
		return
	}
	s.routingMu.Lock()
	s.manualModel = true
	s.routingMu.Unlock()
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

// CurrentProvider returns the selected backend for the interactive CLI.
func (s *Service) CurrentProvider() string {
	if s.client == nil {
		return ""
	}
	return s.client.Provider()
}

// SwitchProvider changes the backend and model for subsequent turns.
func (s *Service) SwitchProvider(name, model, apiKey, baseURL string) error {
	if s.client == nil {
		return fmt.Errorf("serviço de IA indisponível")
	}
	if err := s.client.SwitchProvider(name, model, apiKey, baseURL); err != nil {
		return err
	}
	s.routingMu.Lock()
	s.manualModel = true
	s.routingMu.Unlock()
	if s.autoCompact != nil {
		s.autoCompact.SetModel(model)
	}
	return nil
}

func (s *Service) requestModel(ctx context.Context) string {
	if s.client == nil {
		return ""
	}
	return s.client.modelFor(ctx)
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

// SetConversationStore enables scope-checked references to long journaled
// turns in the transient model prompt.
func (s *Service) SetConversationStore(store *conversation.Store) {
	s.journal = store
}

// PrepareHistory compacts a session before a new user message is appended. The
// caller can persist the returned history before sending the next model turn.
// It also runs at the session snapshot limit so short conversations are not
// silently evicted without a continuity summary.
func (s *Service) PrepareHistory(ctx context.Context, history []conversation.Message) ([]conversation.Message, bool) {
	return s.PrepareHistoryForScope(ctx, "", history)
}

// PrepareHistoryForScope applies the budget retry delay only to the trusted
// conversation scope supplied by the caller. An empty scope never delays a
// retry because it cannot safely identify the conversation.
func (s *Service) PrepareHistoryForScope(ctx context.Context, scope string, history []conversation.Message) ([]conversation.Message, bool) {
	if s.autoCompact == nil || !s.autoCompact.IsEnabled() || len(history) < 4 {
		return history, false
	}
	estimated := compact.EstimateTokens(history)
	atCapacity := len(history) >= conversation.MaxMessages
	budget := historyWindowBudget(s.requestModel(ctx))
	budgetTrigger := estimated > budget && !atCapacity
	if budgetTrigger {
		budgetTrigger = s.historyBudgetRetryReady(scope)
	}
	if !s.autoCompact.CheckWithCapacity(estimated, atCapacity || budgetTrigger) {
		return history, false
	}

	compacted, err := s.autoCompact.CompactHistory(ctx, history)
	if err != nil {
		if budgetTrigger {
			s.setHistoryBudgetRetry(scope, true)
		}
		s.logger.Warn("auto-compact failed; preserving original history", "error", err, "messages", len(history))
		return history, false
	}
	// A single oversized turn or summary may remain above the budget even
	// after successful compaction. Avoid re-summarizing it every turn. A
	// capacity-triggered success also clears an earlier delay for this scope.
	s.setHistoryBudgetRetry(scope, compact.EstimateTokens(compacted) > budget)
	s.logger.Info("auto-compact prepared",
		"messages_before", len(history),
		"messages_after", len(compacted),
		"tokens_before", estimated,
		"tokens_after", compact.EstimateTokens(compacted),
		"history_budget", budget,
	)
	return compacted, true
}

func (s *Service) historyBudgetRetryReady(scope string) bool {
	if scope == "" {
		return true
	}
	s.historyBudgetMu.Lock()
	defer s.historyBudgetMu.Unlock()
	retryAt := s.historyBudgetRetryAt[scope]
	if time.Now().Before(retryAt) {
		return false
	}
	delete(s.historyBudgetRetryAt, scope)
	return true
}

func (s *Service) setHistoryBudgetRetry(scope string, delay bool) {
	if scope == "" {
		return
	}
	s.historyBudgetMu.Lock()
	defer s.historyBudgetMu.Unlock()
	if !delay {
		delete(s.historyBudgetRetryAt, scope)
		return
	}
	if s.historyBudgetRetryAt == nil {
		s.historyBudgetRetryAt = make(map[string]time.Time)
	}
	now := time.Now()
	for key, retryAt := range s.historyBudgetRetryAt {
		if !now.Before(retryAt) {
			delete(s.historyBudgetRetryAt, key)
		}
	}
	if len(s.historyBudgetRetryAt) >= maxHistoryBudgetCooldowns {
		var oldestKey string
		var oldestAt time.Time
		for key, retryAt := range s.historyBudgetRetryAt {
			if oldestKey == "" || retryAt.Before(oldestAt) {
				oldestKey, oldestAt = key, retryAt
			}
		}
		delete(s.historyBudgetRetryAt, oldestKey)
	}
	s.historyBudgetRetryAt[scope] = now.Add(time.Minute)
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
func (s *Service) buildMessages(ctx context.Context, req AnswerRequest) ([]ChatMessage, []string, error) {
	ag := s.agentReg.Get(s.agentName)

	// Search knowledge base
	var knowledgeCtx string
	if s.knowledge != nil {
		fragments, err := s.knowledge.Search(ctx, req.Query, req.Channel, req.UserID)
		if err != nil {
			s.logger.Warn("knowledge search failed, continuing without context", "error", err)
		} else {
			knowledgeCtx = FormatKnowledgeContext(fragments)
		}
	}

	// Auto-inject relevant memories
	memoryCtxScope := s.ownerMemoryContext(ctx, req)
	if req.Channel != "" && req.UserID != "" {
		scope := req.Channel + ":" + req.UserID
		if req.Channel == "cli" && req.SessionMemoryScope != "" {
			memoryCtxScope = memory.WithLegacyEpisodeScope(memoryCtxScope, scope)
			scope = "cli-branch:" + req.SessionMemoryScope
		}
		memoryCtxScope = memory.WithSessionScope(memoryCtxScope, scope)
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
	if notes := s.relevantMarkdownMemory(ctx, req); notes != "" {
		if memoryCtx != "" {
			memoryCtx += "\n\n"
		}
		memoryCtx += notes
	}
	if s.externalChanges != nil && req.Channel == "cli" {
		if extDiff := s.externalChanges.DrainFormatted(); extDiff != "" {
			if memoryCtx != "" {
				memoryCtx += "\n\n"
			}
			memoryCtx += extDiff
		}
	}

	// Build messages using agent persona
	systemPrompt := BuildSystemPrompt(ag.Persona, knowledgeCtx, memoryCtx)
	if experiences := s.relevantExperiences(ctx, req); experiences != "" {
		systemPrompt += "\n\n" + experiences
	}
	if s.repoPlaybook != nil {
		if lessons := s.repoPlaybook.RelevantLessons(workdir.Dir(ctx, "."), req.Query, 4); lessons != "" {
			systemPrompt += "\n\n" + lessons
		}
	}
	if req.ToolTranscriptSummary != "" {
		systemPrompt += "\n\n" + req.ToolTranscriptSummary
	}
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
	history := req.History
	if !req.HistoryPrepared {
		// Direct service callers still get a compact model context. Session
		// adapters call PrepareHistory first so they can persist the summary.
		scope := req.SessionMemoryScope
		if scope == "" && req.Channel != "" && req.UserID != "" {
			scope = req.Channel + ":" + req.UserID
		}
		history, _ = s.PrepareHistoryForScope(ctx, scope, history)
	}
	budget, err := promptConversationBudget(s.requestModel(ctx), systemPrompt)
	if err != nil {
		return nil, nil, err
	}
	var canRecall journalReferenceLookup
	if s.journal != nil && agentHasHistoryTools(ag.Skills) {
		canRecall = func(ctx context.Context, ids []string) (map[string]bool, error) {
			scope, ok := conversation.JournalScopeFromContext(ctx)
			if !ok {
				return nil, nil
			}
			return s.journal.CanReadJournalScopedBatch(ctx, scope, ids)
		}
	}
	messages, err := windowConversationPrompt(ctx, history, req.Query, req.CurrentMessageID, budget, canRecall)
	if err != nil {
		return nil, nil, err
	}
	messages = append([]ChatMessage{{Role: "system", Content: systemPrompt}}, messages...)
	return messages, ag.Skills, nil
}

// Answer processes a query through the full pipeline (synchronous).
func (s *Service) Answer(ctx context.Context, req AnswerRequest) (*AnswerResponse, error) {
	ctx = s.ownerMemoryContext(ctx, req)
	if s.onActivity != nil {
		s.onActivity()
	}
	ctx = workdir.WithScope(ctx, s.workdirs, req.Channel, req.UserID, req.SessionMemoryScope)
	ctx = s.withRoutedModel(ctx, req)
	ctx = s.withProjectInstructions(ctx)
	messages, names, err := s.buildMessages(ctx, req)
	if err != nil {
		return nil, err
	}

	ctx = withRequestToolSession(ctx, req.Channel, req.UserID, s.agentName, req.SessionMemoryScope)
	ctx = withRequestQueryHint(ctx, req.Query)
	if req.Effort != "" {
		ctx = WithEffort(ctx, req.Effort)
	}
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
	ctx = s.ownerMemoryContext(ctx, req)
	if s.onActivity != nil {
		s.onActivity()
	}
	ctx = workdir.WithScope(ctx, s.workdirs, req.Channel, req.UserID, req.SessionMemoryScope)
	ctx = s.withRoutedModel(ctx, req)
	ctx = s.withProjectInstructions(ctx)
	messages, names, err := s.buildMessages(ctx, req)
	if err != nil {
		return nil, err
	}

	ctx = withRequestToolSession(ctx, req.Channel, req.UserID, s.agentName, req.SessionMemoryScope)
	ctx = withRequestQueryHint(ctx, req.Query)
	if req.Effort != "" {
		ctx = WithEffort(ctx, req.Effort)
	}
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
	collector, err := project.NewInstructionCollector(workdir.Dir(ctx, "."))
	if err != nil {
		s.logger.Warn("project instruction collector unavailable", "error", err)
		return ctx
	}
	return project.WithInstructionScope(ctx, collector)
}
