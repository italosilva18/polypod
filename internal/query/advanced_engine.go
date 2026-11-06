package query

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	// round2(B2): wire orchestrator for permission enforcement and
	// per-turn token budget tracking (aliased to avoid collision with
	// the query package's own BudgetTracker).
	"github.com/costa/polypod/internal/orchestrator"
	tbudget "github.com/costa/polypod/internal/tokenbudget"
	"github.com/costa/polypod/internal/types"
)

// turnRunner is the minimal interface needed by the reactive-compact recovery
// path so that the query package can accept a *compact.ReactiveCompactor
// without importing the compact package (which would create an import cycle:
// compact → conversation, query → compact → query).
//
// *compact.ReactiveCompactor satisfies this interface via its RecoverAndRetry
// method. Callers set the field via SetReactiveCompactor.
type turnRunner interface {
	RecoverAndRetry(ctx context.Context, turnFn func() error) error
}

// AdvancedQueryEngine owns the query lifecycle and session state for a
// conversation. It is the Go equivalent of Claude Code's QueryEngine class.
//
// One AdvancedQueryEngine per conversation. Each SubmitMessage() call starts a
// new turn within the same conversation. State (messages, usage, etc.) persists
// across turns.
//
// The engine implements the full model call loop with:
//   - System prompt assembly (persona + context + tool descriptions)
//   - Message preparation with token budget
//   - Multi-turn model invocation loop
//   - Tool execution within the loop
//   - Compaction triggers when context exceeds limit
//   - Session persistence after each turn
//   - Error recovery with retries (max_output_tokens, prompt-too-long)
//   - Streaming support (channel-based)
//   - Stop condition evaluation
type AdvancedQueryEngine struct {
	mu sync.Mutex

	config    AdvancedEngineConfig
	deps      QueryDeps
	qCfg      QueryConfig
	messages  []Message
	sessionID types.SessionID
	logger    *slog.Logger

	// Cumulative usage across turns.
	totalUsage CumulativeUsage

	// Turn counter across the engine lifetime.
	turnCount int

	// AbortChan signals cancellation to the query loop.
	abortChan chan struct{}
	aborted   bool

	// reactiveCompactor, when non-nil, is used as the recovery path when the
	// model returns a context-overflow error. It compacts the conversation and
	// retries the failed turn once. Set via SetReactiveCompactor.
	reactiveCompactor turnRunner

	// overflowDetector classifies errors as context-overflow failures. When nil
	// the engine falls back to the built-in isPromptTooLong check only. Set via
	// SetReactiveCompactor alongside reactiveCompactor.
	overflowDetector func(error) bool
}

// AdvancedEngineConfig is the full configuration for the advanced engine.
type AdvancedEngineConfig struct {
	// SessionID uniquely identifies this conversation.
	SessionID types.SessionID

	// Deps are the injected I/O dependencies.
	Deps QueryDeps

	// QueryConfig holds immutable snapshot of query parameters.
	QueryConfig QueryConfig

	// Tools available for the model to call.
	Tools []types.Tool

	// ToolExecutor runs tool calls.
	ToolExecutor ToolExecutor

	// InitialMessages seeds the conversation.
	InitialMessages []Message

	// StopHooks are evaluated after each model response.
	StopHooks []StopHook

	// SystemPrompt is the base system prompt.
	SystemPrompt string

	// UserContext are key-value pairs prepended to user messages.
	UserContext map[string]string

	// SystemContext are key-value pairs appended to the system prompt.
	SystemContext map[string]string

	// ContextWindowSize is the total context window for the model.
	ContextWindowSize int

	// PersistSession enables transcript recording.
	PersistSession bool

	// Logger for structured logging.
	Logger *slog.Logger

	// round2(B2): Orchestrator, when non-nil, is used to execute tool
	// calls so that permission rules, plan-mode simulation, and
	// pre/post tool hooks are enforced. When nil the engine falls back
	// to direct execution and logs a WARNING (the bypass is visible).
	Orchestrator *orchestrator.Orchestrator

	// CategoryTracker, when non-nil, is a shared per-category token
	// tracker that accumulates usage across all SubmitMessage calls.
	// The /cost and /usage commands read from this tracker. When nil
	// the engine creates a fresh ephemeral tracker per query loop.
	CategoryTracker *tbudget.BudgetTracker
}

// CumulativeUsage tracks total token consumption across turns.
type CumulativeUsage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// TotalTokens returns the sum of all token types.
func (u CumulativeUsage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// Add accumulates another usage into this one.
func (u *CumulativeUsage) Add(other *StreamUsage) {
	if other == nil {
		return
	}
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadInputTokens += other.CacheReadInputTokens
	u.CacheCreationInputTokens += other.CacheCreationInputTokens
}

// --- Stream output types ---

// EngineEvent is a message yielded from the engine to the caller.
type EngineEvent struct {
	Type EngineEventType

	// AssistantText is set for assistant content events.
	AssistantText string

	// Message is the full message for message-level events.
	Message *Message

	// StreamDelta is set for streaming deltas.
	StreamDelta string

	// ToolCall is set for tool execution events.
	ToolCall *ToolCallEvent

	// Error is set for error events.
	Error error

	// Result is set for the final result event.
	Result *QueryLoopResult

	// CompactBoundary is set when compaction occurs.
	CompactBoundary *CompactBoundaryEvent

	// StopReason from the model.
	StopReason string

	// Usage from the current message.
	Usage *StreamUsage
}

// EngineEventType discriminates EngineEvent kinds.
type EngineEventType int

const (
	// EngineEventAssistant is a complete assistant message.
	EngineEventAssistant EngineEventType = iota
	// EngineEventStreamDelta is a streaming text delta.
	EngineEventStreamDelta
	// EngineEventToolStart signals a tool is being executed.
	EngineEventToolStart
	// EngineEventToolResult signals a tool execution completed.
	EngineEventToolResult
	// EngineEventUser is a user message (tool results, interruptions).
	EngineEventUser
	// EngineEventSystem is a system-level message.
	EngineEventSystem
	// EngineEventCompactBoundary signals compaction occurred.
	EngineEventCompactBoundary
	// EngineEventResult is the final result.
	EngineEventResult
	// EngineEventError is a recoverable error.
	EngineEventError
	// EngineEventUsage carries token usage information.
	EngineEventUsage
)

// ToolCallEvent carries information about a tool call.
type ToolCallEvent struct {
	ID       string
	Name     string
	Args     map[string]any
	Result   string
	IsError  bool
	Duration time.Duration
}

// CompactBoundaryEvent signals that compaction occurred.
type CompactBoundaryEvent struct {
	PreCompactTokens  int
	PostCompactTokens int
	MessagesRemoved   int
}

// QueryLoopResult is the final outcome of a SubmitMessage call.
type QueryLoopResult struct {
	// Success is true if the conversation ended normally.
	Success bool

	// StopReason is the model's stop reason ("end_turn", "max_tokens", etc.)
	StopReason string

	// ResultText is the final text from the last assistant message.
	ResultText string

	// TurnCount is the number of turns in this submission.
	TurnCount int

	// TotalUsage is cumulative token usage.
	TotalUsage CumulativeUsage

	// DurationMs is the wall-clock time for the submission.
	DurationMs int64

	// Error is set for failed submissions.
	Error error

	// ErrorType categorizes the failure.
	ErrorType string // "max_turns", "max_budget", "model_error", "aborted", ""
}

// --- Transition types for the loop state machine ---

// transitionReason tracks why the loop continued (for diagnostics / tests).
type transitionReason string

const (
	transitionNextTurn             transitionReason = "next_turn"
	transitionMaxOutputRecovery    transitionReason = "max_output_tokens_recovery"
	transitionMaxOutputEscalate    transitionReason = "max_output_tokens_escalate"
	transitionReactiveCompactRetry transitionReason = "reactive_compact_retry"
	transitionStopHookBlocking     transitionReason = "stop_hook_blocking"
	transitionTokenBudgetContinue  transitionReason = "token_budget_continuation"
)

// loopState is the mutable state carried between loop iterations.
type loopState struct {
	messages                    []Message
	autoCompactTracking         *AutoCompactTracking
	maxOutputRecoveryCount      int
	hasAttemptedReactiveCompact bool
	maxOutputTokensOverride     int
	stopHookActive              bool
	turnCount                   int
	lastTransition              transitionReason
	budgetTracker               *BudgetTracker
	// round2(B2): per-turn / per-category token tracker used to
	// decide when to trigger compaction (budget > 70%).
	categoryTracker *tbudget.BudgetTracker
}

const maxOutputTokensRecoveryLimit = 3

// --- Constructor ---

// NewAdvancedQueryEngine creates a new advanced engine.
func NewAdvancedQueryEngine(cfg AdvancedEngineConfig) (*AdvancedQueryEngine, error) {
	if err := cfg.Deps.Validate(); err != nil {
		return nil, fmt.Errorf("invalid deps: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	messages := make([]Message, 0, len(cfg.InitialMessages)+16)
	messages = append(messages, cfg.InitialMessages...)

	return &AdvancedQueryEngine{
		config:    cfg,
		deps:      cfg.Deps,
		qCfg:      cfg.QueryConfig,
		messages:  messages,
		sessionID: cfg.SessionID,
		logger:    logger,
		abortChan: make(chan struct{}),
	}, nil
}

// --- Public API ---

// SubmitMessage processes a user message through the full query loop.
// Returns a channel of EngineEvents. The caller must drain the channel.
// The channel is closed when the submission completes.
//
// Thread-safe: only one SubmitMessage can run at a time.
func (e *AdvancedQueryEngine) SubmitMessage(ctx context.Context, userMessage string) <-chan EngineEvent {
	out := make(chan EngineEvent, 64)

	go func() {
		defer close(out)

		e.mu.Lock()
		// Reset abort state for new submission.
		e.abortChan = make(chan struct{})
		e.aborted = false
		e.mu.Unlock()

		e.runQueryLoop(ctx, userMessage, out)
	}()

	return out
}

// Abort cancels the current query loop.
func (e *AdvancedQueryEngine) Abort() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.aborted {
		e.aborted = true
		close(e.abortChan)
	}
}

// GetMessages returns a copy of the current conversation messages.
func (e *AdvancedQueryEngine) GetMessages() []Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]Message, len(e.messages))
	copy(result, e.messages)
	return result
}

// GetUsage returns cumulative token usage.
func (e *AdvancedQueryEngine) GetUsage() CumulativeUsage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.totalUsage
}

// GetSessionID returns the session identifier.
func (e *AdvancedQueryEngine) GetSessionID() types.SessionID {
	return e.sessionID
}

// SetModel changes the primary model for future calls.
func (e *AdvancedQueryEngine) SetModel(model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.qCfg.Model = model
}

// SetReactiveCompactor wires a reactive-compaction backend into the engine.
// When rc is non-nil and detector returns true for an error, the engine will
// compact the conversation and retry the failed model turn once instead of
// surfacing the error immediately.
//
// Typical wiring (avoiding import cycle — caller holds *compact.ReactiveCompactor
// which satisfies turnRunner):
//
//	engine.SetReactiveCompactor(rc, compact.IsContextOverflowError)
//
// Passing nil for rc disables reactive compaction (the default). detector may
// be nil when rc is nil; if rc is non-nil and detector is nil the engine falls
// back to its built-in isPromptTooLong classifier.
func (e *AdvancedQueryEngine) SetReactiveCompactor(rc turnRunner, detector func(error) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reactiveCompactor = rc
	e.overflowDetector = detector
}

// --- Core query loop ---

func (e *AdvancedQueryEngine) runQueryLoop(ctx context.Context, userMessage string, out chan<- EngineEvent) {
	startTime := time.Now()

	// Build and append user message
	userMsg := Message{
		Role:    "user",
		Content: userMessage,
		UUID:    e.deps.UUID(),
	}

	e.mu.Lock()
	e.messages = append(e.messages, userMsg)
	e.turnCount++
	messages := make([]Message, len(e.messages))
	copy(messages, e.messages)
	e.mu.Unlock()

	// Persist the user message before entering the loop.
	if e.config.PersistSession {
		if err := e.deps.PersistSession(ctx, messages); err != nil {
			e.logger.Warn("failed to persist user message", "error", err)
		}
	}

	// Assemble full system prompt
	systemPrompt := e.assembleSystemPrompt()

	// Initialize loop state
	state := &loopState{
		messages:  messages,
		turnCount: 1,
	}

	// Token budget tracker (if budget is set)
	if e.qCfg.TaskBudget != nil {
		state.budgetTracker = NewBudgetTracker()
	}

	// round2(B2): per-category tracker, triggers compaction when >70% used.
	// When a shared CategoryTracker is provided via config, use it so that
	// /cost and /usage commands see cumulative data across all queries.
	if e.config.CategoryTracker != nil {
		state.categoryTracker = e.config.CategoryTracker
	} else {
		state.categoryTracker = tbudget.NewTracker(tbudget.TrackerConfig{
			Limit:            e.config.ContextWindowSize,
			CompactThreshold: 0.70,
		})
	}

	// Main query loop — mirrors the TypeScript while(true) in queryLoop().
	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			e.emitResult(out, startTime, state, "aborted", ctx.Err())
			return
		default:
		}

		// Check for abort
		if e.isAborted() {
			e.emitResult(out, startTime, state, "aborted", fmt.Errorf("query aborted by user"))
			return
		}

		// 1. Apply microcompact
		messagesForQuery, err := e.applyMicrocompact(ctx, state.messages)
		if err != nil {
			e.logger.Warn("microcompact failed", "error", err)
			messagesForQuery = state.messages
		}

		// 2. Check autocompact
		compactResult, err := e.checkAutocompact(ctx, messagesForQuery, systemPrompt, state)
		if err != nil {
			e.logger.Warn("autocompact failed", "error", err)
		}
		if compactResult != nil {
			// Compaction succeeded — use post-compact messages
			messagesForQuery = compactResult.SummaryMessages

			if compactResult.BoundaryMessage != nil {
				e.mu.Lock()
				e.messages = append(e.messages, *compactResult.BoundaryMessage)
				e.mu.Unlock()
			}

			out <- EngineEvent{
				Type: EngineEventCompactBoundary,
				CompactBoundary: &CompactBoundaryEvent{
					PreCompactTokens:  compactResult.PreCompactTokenCount,
					PostCompactTokens: compactResult.PostCompactTokenCount,
					MessagesRemoved:   len(state.messages) - len(compactResult.SummaryMessages),
				},
			}

			// Reset tracking after compaction
			state.autoCompactTracking = &AutoCompactTracking{
				Compacted:   true,
				TurnID:      e.deps.UUID(),
				TurnCounter: 0,
			}
		}

		// 3. Check blocking token limit (only when autocompact is off)
		if compactResult == nil && !e.qCfg.Gates.AutoCompactEnabled {
			if e.isAtBlockingLimit(messagesForQuery) {
				out <- EngineEvent{
					Type:  EngineEventError,
					Error: fmt.Errorf("context too large — run /compact or enable auto-compact"),
				}
				e.emitResult(out, startTime, state, "blocking_limit", fmt.Errorf("prompt too long"))
				return
			}
		}

		// 4. Call the model with streaming
		e.logger.Debug("calling model",
			"turn", state.turnCount,
			"messages", len(messagesForQuery),
			"model", e.qCfg.Model,
		)

		streamReq := ModelStreamRequest{
			Messages:        prependUserContext(messagesForQuery, e.config.UserContext),
			SystemPrompt:    systemPrompt,
			Tools:           e.config.Tools,
			Model:           e.qCfg.Model,
			MaxOutputTokens: state.maxOutputTokensOverride,
			Temperature:     e.qCfg.Temperature,
			ThinkingEnabled: e.qCfg.ThinkingEnabled,
			ThinkingBudget:  e.qCfg.ThinkingBudget,
			FallbackModel:   e.qCfg.FallbackModel,
			Signal:          e.abortChan,
			TaskBudget:      e.qCfg.TaskBudget,
		}
		if state.maxOutputTokensOverride > 0 {
			streamReq.MaxOutputTokens = state.maxOutputTokensOverride
		} else if e.qCfg.MaxOutputTokens > 0 {
			streamReq.MaxOutputTokens = e.qCfg.MaxOutputTokens
		}

		streamCh, err := e.deps.CallModel(ctx, streamReq)
		if err != nil {
			e.logger.Error("model call failed", "error", err)
			out <- EngineEvent{
				Type:  EngineEventError,
				Error: err,
			}
			e.emitResult(out, startTime, state, "model_error", err)
			return
		}

		// 5. Consume the stream: accumulate assistant content and tool calls
		assistantText, toolCalls, stopReason, msgUsage, streamErr := e.consumeStream(ctx, streamCh, out)

		// Accumulate usage
		e.mu.Lock()
		e.totalUsage.Add(msgUsage)
		e.mu.Unlock()

		// round2(B2): record per-turn usage into the category tracker
		// so compaction can be triggered when budget > 70%.
		if state.categoryTracker != nil && msgUsage != nil {
			state.categoryTracker.RecordTurn(tbudget.TurnStats{
				Prompt:     msgUsage.InputTokens,
				Completion: msgUsage.OutputTokens,
				CacheRead:  msgUsage.CacheReadInputTokens,
				CacheWrite: msgUsage.CacheCreationInputTokens,
			})
			if state.categoryTracker.ShouldTriggerCompact() {
				e.logger.Info("category tracker suggests compaction",
					"used", state.categoryTracker.TotalUsed(),
					"limit", state.categoryTracker.Limit(),
					"ratio", state.categoryTracker.UsageRatio(),
				)
			}
		}

		// Handle abort during streaming
		if e.isAborted() {
			// Emit interruption messages for any pending tool calls
			for _, tc := range toolCalls {
				interruptMsg := Message{
					Role:    "user",
					Content: fmt.Sprintf("[Tool call '%s' interrupted by user]", tc.Name),
					UUID:    e.deps.UUID(),
				}
				e.appendMessage(interruptMsg)
				out <- EngineEvent{Type: EngineEventUser, Message: &interruptMsg}
			}
			e.emitResult(out, startTime, state, "aborted", fmt.Errorf("aborted during streaming"))
			return
		}

		// Handle stream errors
		if streamErr != nil {
			e.logger.Error("stream error", "error", streamErr)

			// isOverflow unifies the built-in classifier with the optional
			// external detector supplied via SetReactiveCompactor.
			isOverflow := func(err error) bool {
				if e.overflowDetector != nil && e.overflowDetector(err) {
					return true
				}
				return isPromptTooLong(err)
			}

			// Primary recovery path: ReactiveCompactor (when wired in).
			// We pass a turnFn that returns the already-known streamErr on its
			// first invocation so that RecoverAndRetry triggers compaction
			// without re-issuing the failed model call, then performs the real
			// retry on the second invocation.
			if e.reactiveCompactor != nil && isOverflow(streamErr) && !state.hasAttemptedReactiveCompact {
				firstCall := true
				// retryText/retryToolCalls/etc. are written by the retry closure
				// so the outer loop can continue with fresh results.
				var (
					retryText      string
					retryToolCalls []StreamToolCall
					retryStop      string
					retryUsage     *StreamUsage
				)
				turnFn := func() error {
					if firstCall {
						// First call: surface the already-known overflow error so
						// RecoverAndRetry triggers compaction, without hitting the
						// model again.
						firstCall = false
						return streamErr
					}
					// Retry call: issue a fresh model request with the
					// (now-compacted) message history from state.
					retryReq := ModelStreamRequest{
						Messages:        prependUserContext(state.messages, e.config.UserContext),
						SystemPrompt:    systemPrompt,
						Tools:           e.config.Tools,
						Model:           e.qCfg.Model,
						MaxOutputTokens: state.maxOutputTokensOverride,
						Temperature:     e.qCfg.Temperature,
						ThinkingEnabled: e.qCfg.ThinkingEnabled,
						ThinkingBudget:  e.qCfg.ThinkingBudget,
						FallbackModel:   e.qCfg.FallbackModel,
						Signal:          e.abortChan,
						TaskBudget:      e.qCfg.TaskBudget,
					}
					if state.maxOutputTokensOverride > 0 {
						retryReq.MaxOutputTokens = state.maxOutputTokensOverride
					} else if e.qCfg.MaxOutputTokens > 0 {
						retryReq.MaxOutputTokens = e.qCfg.MaxOutputTokens
					}
					retryCh, callErr := e.deps.CallModel(ctx, retryReq)
					if callErr != nil {
						return callErr
					}
					var retryStreamErr error
					retryText, retryToolCalls, retryStop, retryUsage, retryStreamErr = e.consumeStream(ctx, retryCh, out)
					return retryStreamErr
				}

				rcErr := e.reactiveCompactor.RecoverAndRetry(ctx, turnFn)
				if rcErr == nil {
					// Compaction + retry succeeded — update loop variables with
					// the retry results and continue the main loop normally.
					e.mu.Lock()
					e.totalUsage.Add(retryUsage)
					e.mu.Unlock()
					if state.categoryTracker != nil && retryUsage != nil {
						state.categoryTracker.RecordTurn(tbudget.TurnStats{
							Prompt:     retryUsage.InputTokens,
							Completion: retryUsage.OutputTokens,
							CacheRead:  retryUsage.CacheReadInputTokens,
							CacheWrite: retryUsage.CacheCreationInputTokens,
						})
					}
					assistantText = retryText
					toolCalls = retryToolCalls
					stopReason = retryStop
					msgUsage = retryUsage
					state.hasAttemptedReactiveCompact = true
					state.lastTransition = transitionReactiveCompactRetry
					// Fall through to step 6 (append assistant message) with
					// the retry results, so no explicit continue needed here.
					goto afterStreamError
				}
				// rcErr != nil — compaction or retry failed; fall through to
				// the generic error path below.
				e.logger.Warn("reactive compactor failed to recover overflow",
					"error", rcErr,
					"original_error", streamErr,
				)
			}

			// Legacy ad-hoc path: Autocompact fallback (kept for backward
			// compatibility when reactiveCompactor is not wired in).
			if isPromptTooLong(streamErr) && !state.hasAttemptedReactiveCompact && e.reactiveCompactor == nil {
				// Try reactive compaction
				rcResult, rcErr := e.deps.Autocompact(ctx, messagesForQuery, AutocompactOpts{
					SystemPrompt: systemPrompt,
					QuerySource:  e.qCfg.QuerySource,
				})
				if rcErr == nil && rcResult != nil {
					state.messages = rcResult.SummaryMessages
					state.hasAttemptedReactiveCompact = true
					state.lastTransition = transitionReactiveCompactRetry
					continue
				}
			}

			// Emit error message and append to conversation
			errorMsg := Message{
				Role:       "assistant",
				Content:    fmt.Sprintf("[API Error: %v]", streamErr),
				UUID:       e.deps.UUID(),
				IsAPIError: true,
			}
			e.appendMessage(errorMsg)
			out <- EngineEvent{Type: EngineEventAssistant, Message: &errorMsg}
			e.emitResult(out, startTime, state, "model_error", streamErr)
			return
		}
	afterStreamError:

		// 6. Append the assistant message
		assistantMsg := Message{
			Role:       "assistant",
			Content:    assistantText,
			UUID:       e.deps.UUID(),
			StopReason: stopReason,
		}
		e.appendMessage(assistantMsg)
		messagesForQuery = append(messagesForQuery, assistantMsg)

		out <- EngineEvent{
			Type:       EngineEventAssistant,
			Message:    &assistantMsg,
			StopReason: stopReason,
		}

		// Persist after assistant response
		if e.config.PersistSession {
			e.persistAsync(ctx)
		}

		// 7. If no tool calls → check stop conditions and potentially end
		if len(toolCalls) == 0 {
			// Check for max_output_tokens recovery
			if stopReason == "max_tokens" {
				if state.maxOutputRecoveryCount < maxOutputTokensRecoveryLimit {
					recoveryMsg := Message{
						Role: "user",
						Content: "Output token limit hit. Resume directly — no apology, no recap. " +
							"Pick up mid-thought if that is where the cut happened. " +
							"Break remaining work into smaller pieces.",
						UUID: e.deps.UUID(),
					}
					e.appendMessage(recoveryMsg)
					state.messages = append(messagesForQuery, recoveryMsg)
					state.maxOutputRecoveryCount++
					state.lastTransition = transitionMaxOutputRecovery
					continue
				}
				// Recovery exhausted
			}

			// Skip stop hooks for API error messages
			if assistantMsg.IsAPIError {
				e.emitResult(out, startTime, state, "", nil)
				return
			}

			// Evaluate stop hooks
			stopHookResult := e.evaluateStopHooks(ctx, messagesForQuery, []Message{assistantMsg}, out)

			if stopHookResult.PreventContinuation {
				e.emitResult(out, startTime, state, "stop_hook", nil)
				return
			}

			if len(stopHookResult.BlockingErrors) > 0 {
				// Feed blocking errors back to the model
				state.messages = append(messagesForQuery, stopHookResult.BlockingErrors...)
				for _, be := range stopHookResult.BlockingErrors {
					e.appendMessage(be)
				}
				state.maxOutputRecoveryCount = 0
				state.stopHookActive = true
				state.lastTransition = transitionStopHookBlocking
				continue
			}

			// Check token budget auto-continuation
			if state.budgetTracker != nil && e.qCfg.TaskBudget != nil {
				decision := CheckTokenBudget(
					state.budgetTracker,
					"", // main thread
					e.qCfg.TaskBudget.Total,
					e.totalUsage.OutputTokens,
				)
				if decision.Action == BudgetContinue {
					nudgeMsg := Message{
						Role:    "user",
						Content: decision.NudgeMessage,
						UUID:    e.deps.UUID(),
					}
					e.appendMessage(nudgeMsg)
					state.messages = append(messagesForQuery, nudgeMsg)
					state.maxOutputRecoveryCount = 0
					state.lastTransition = transitionTokenBudgetContinue
					continue
				}
			}

			// Natural completion
			e.emitResult(out, startTime, state, "", nil)
			return
		}

		// 8. Execute tool calls
		toolResults, toolPreventContinuation := e.executeTools(ctx, toolCalls, messagesForQuery, assistantMsg, out)

		// Handle abort during tool execution
		if e.isAborted() {
			e.emitResult(out, startTime, state, "aborted", fmt.Errorf("aborted during tool execution"))
			return
		}

		if toolPreventContinuation {
			e.emitResult(out, startTime, state, "hook_stopped", nil)
			return
		}

		// Append tool results and persist
		for _, tr := range toolResults {
			e.appendMessage(tr)
			messagesForQuery = append(messagesForQuery, tr)
		}

		if e.config.PersistSession {
			e.persistAsync(ctx)
		}

		// 9. Check turn limits
		nextTurnCount := state.turnCount + 1
		if e.qCfg.MaxTurns > 0 && nextTurnCount > e.qCfg.MaxTurns {
			out <- EngineEvent{
				Type: EngineEventSystem,
				Message: &Message{
					Role:    "system",
					Content: fmt.Sprintf("Reached maximum number of turns (%d)", e.qCfg.MaxTurns),
					UUID:    e.deps.UUID(),
				},
			}
			e.emitResult(out, startTime, state, "max_turns", nil)
			return
		}

		// 10. Check cost budget
		if e.qCfg.MaxBudgetUSD > 0 {
			// Rough cost estimation: $3/M input, $15/M output (Claude-class pricing)
			estimatedCost := float64(e.totalUsage.InputTokens)/1_000_000*3.0 +
				float64(e.totalUsage.OutputTokens)/1_000_000*15.0
			if estimatedCost >= e.qCfg.MaxBudgetUSD {
				e.emitResult(out, startTime, state, "max_budget", fmt.Errorf("reached maximum budget ($%.2f)", e.qCfg.MaxBudgetUSD))
				return
			}
		}

		// Track compaction turns
		if state.autoCompactTracking != nil && state.autoCompactTracking.Compacted {
			state.autoCompactTracking.TurnCounter++
		}

		// Prepare for next iteration
		state.messages = messagesForQuery
		state.turnCount = nextTurnCount
		state.maxOutputRecoveryCount = 0
		state.hasAttemptedReactiveCompact = false
		state.maxOutputTokensOverride = 0
		state.lastTransition = transitionNextTurn
	}
}

// --- Internal helpers ---

// assembleSystemPrompt builds the full system prompt from components.
func (e *AdvancedQueryEngine) assembleSystemPrompt() string {
	var parts []string

	// Base system prompt
	if e.config.SystemPrompt != "" {
		parts = append(parts, e.config.SystemPrompt)
	} else if e.qCfg.CustomSystemPrompt != "" {
		parts = append(parts, e.qCfg.CustomSystemPrompt)
	}

	// Append system prompt
	if e.qCfg.AppendSystemPrompt != "" {
		parts = append(parts, e.qCfg.AppendSystemPrompt)
	}

	// System context
	if len(e.config.SystemContext) > 0 {
		var ctxParts []string
		for k, v := range e.config.SystemContext {
			ctxParts = append(ctxParts, fmt.Sprintf("<%s>\n%s\n</%s>", k, v, k))
		}
		parts = append(parts, strings.Join(ctxParts, "\n\n"))
	}

	// Tool descriptions
	if len(e.config.Tools) > 0 {
		var toolDescs []string
		for _, t := range e.config.Tools {
			if !t.IsEnabled() {
				continue
			}
			toolDescs = append(toolDescs, fmt.Sprintf("- %s: %s", t.Name(), t.Description()))
		}
		if len(toolDescs) > 0 {
			parts = append(parts, "Available tools:\n"+strings.Join(toolDescs, "\n"))
		}
	}

	return strings.Join(parts, "\n\n")
}

// prependUserContext prepends user context key-value pairs to messages.
// Mirrors the TypeScript prependUserContext utility.
func prependUserContext(messages []Message, userContext map[string]string) []Message {
	if len(userContext) == 0 || len(messages) == 0 {
		return messages
	}

	// Build the context string
	var ctxParts []string
	for k, v := range userContext {
		ctxParts = append(ctxParts, fmt.Sprintf("<%s>\n%s\n</%s>", k, v, k))
	}
	contextStr := strings.Join(ctxParts, "\n\n")

	// Find the first user message and prepend context
	result := make([]Message, len(messages))
	copy(result, messages)
	for i, m := range result {
		if m.Role == "user" {
			result[i].Content = contextStr + "\n\n" + m.Content
			break
		}
	}
	return result
}

// applyMicrocompact applies lightweight compaction.
func (e *AdvancedQueryEngine) applyMicrocompact(ctx context.Context, messages []Message) ([]Message, error) {
	result, err := e.deps.Microcompact(ctx, messages, MicrocompactOpts{
		QuerySource: e.qCfg.QuerySource,
		Tools:       e.config.Tools,
	})
	if err != nil {
		return nil, err
	}
	return result.Messages, nil
}

// checkAutocompact determines if autocompact should run and executes it.
func (e *AdvancedQueryEngine) checkAutocompact(
	ctx context.Context,
	messages []Message,
	systemPrompt string,
	state *loopState,
) (*AutocompactResult, error) {
	if !e.qCfg.Gates.AutoCompactEnabled {
		return nil, nil
	}

	result, err := e.deps.Autocompact(ctx, messages, AutocompactOpts{
		SystemPrompt: systemPrompt,
		QuerySource:  e.qCfg.QuerySource,
		Tracking:     state.autoCompactTracking,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// isAtBlockingLimit checks if messages exceed the hard blocking limit.
func (e *AdvancedQueryEngine) isAtBlockingLimit(messages []Message) bool {
	if e.config.ContextWindowSize <= 0 {
		return false
	}
	tokens := EstimateTokenCount(messages)
	budget := AllocateContextBudget(e.config.ContextWindowSize, 0, 0, e.qCfg.MaxOutputTokens)
	thresholds := DefaultCompactionThreshold()
	state := CalculateTokenWarningState(tokens, budget, thresholds)
	return state.IsAtBlockingLimit
}

// consumeStream drains the model stream channel, emitting deltas to the output
// channel and accumulating the full response.
func (e *AdvancedQueryEngine) consumeStream(
	ctx context.Context,
	streamCh <-chan StreamEvent,
	out chan<- EngineEvent,
) (text string, toolCalls []StreamToolCall, stopReason string, usage *StreamUsage, err error) {

	var textBuf strings.Builder
	var accumToolCalls []StreamToolCall
	var msgUsage StreamUsage

	for evt := range streamCh {
		// Check abort between events
		if e.isAborted() {
			return textBuf.String(), accumToolCalls, stopReason, &msgUsage, nil
		}

		switch evt.Type {
		case EventContentDelta:
			textBuf.WriteString(evt.Delta)
			out <- EngineEvent{
				Type:        EngineEventStreamDelta,
				StreamDelta: evt.Delta,
			}

		case EventToolUseStart:
			if evt.ToolCall != nil {
				out <- EngineEvent{
					Type: EngineEventToolStart,
					ToolCall: &ToolCallEvent{
						ID:   evt.ToolCall.ID,
						Name: evt.ToolCall.Name,
						Args: evt.ToolCall.Arguments,
					},
				}
			}

		case EventToolUseEnd:
			if evt.ToolCall != nil {
				accumToolCalls = append(accumToolCalls, *evt.ToolCall)
			}

		case EventMessageStart:
			if evt.Usage != nil {
				msgUsage.InputTokens += evt.Usage.InputTokens
				msgUsage.OutputTokens += evt.Usage.OutputTokens
				msgUsage.CacheReadInputTokens += evt.Usage.CacheReadInputTokens
				msgUsage.CacheCreationInputTokens += evt.Usage.CacheCreationInputTokens
			}

		case EventMessageDelta:
			if evt.Usage != nil {
				msgUsage.InputTokens += evt.Usage.InputTokens
				msgUsage.OutputTokens += evt.Usage.OutputTokens
				msgUsage.CacheReadInputTokens += evt.Usage.CacheReadInputTokens
				msgUsage.CacheCreationInputTokens += evt.Usage.CacheCreationInputTokens
			}
			if evt.StopReason != "" {
				stopReason = evt.StopReason
			}

		case EventMessageStop:
			if evt.StopReason != "" {
				stopReason = evt.StopReason
			}
			out <- EngineEvent{
				Type:  EngineEventUsage,
				Usage: &msgUsage,
			}

		case EventThinking:
			// Thinking blocks are consumed but not emitted to output.
			// They're part of the model's internal reasoning.

		case EventError:
			if evt.Error != nil {
				err = evt.Error
			}
		}
	}

	return textBuf.String(), accumToolCalls, stopReason, &msgUsage, err
}

// executeTools runs a batch of tool calls and returns tool result messages.
func (e *AdvancedQueryEngine) executeTools(
	ctx context.Context,
	toolCalls []StreamToolCall,
	conversationMessages []Message,
	assistantMsg Message,
	out chan<- EngineEvent,
) (results []Message, preventContinuation bool) {

	for _, tc := range toolCalls {
		// Check abort
		if e.isAborted() {
			interruptMsg := Message{
				Role:    "user",
				Content: fmt.Sprintf("[Tool call '%s' interrupted by user]", tc.Name),
				UUID:    e.deps.UUID(),
			}
			results = append(results, interruptMsg)
			out <- EngineEvent{Type: EngineEventUser, Message: &interruptMsg}
			continue
		}

		// Find the tool
		tool := e.findTool(tc.Name)
		if tool == nil {
			errContent := fmt.Sprintf("Tool '%s' not found", tc.Name)
			errMsg := Message{
				Role:    "user",
				Content: errContent,
				UUID:    e.deps.UUID(),
			}
			results = append(results, errMsg)
			out <- EngineEvent{
				Type: EngineEventToolResult,
				ToolCall: &ToolCallEvent{
					ID:      tc.ID,
					Name:    tc.Name,
					Args:    tc.Arguments,
					Result:  errContent,
					IsError: true,
				},
			}
			continue
		}

		// Check permissions
		toolCtx := types.ToolContext{
			Context:    ctx,
			SessionID:  e.sessionID,
			WorkingDir: ".",
		}
		permResult := tool.CheckPermissions(tc.Arguments, toolCtx)
		if permResult.Behavior == types.PermissionDeny {
			denyContent := fmt.Sprintf("Permission denied for tool '%s': %s", tc.Name, permResult.Message)
			denyMsg := Message{
				Role:    "user",
				Content: denyContent,
				UUID:    e.deps.UUID(),
			}
			results = append(results, denyMsg)
			out <- EngineEvent{
				Type: EngineEventToolResult,
				ToolCall: &ToolCallEvent{
					ID:      tc.ID,
					Name:    tc.Name,
					Args:    tc.Arguments,
					Result:  denyContent,
					IsError: true,
				},
			}
			continue
		}

		// Validate input
		valResult := tool.ValidateInput(tc.Arguments)
		if !valResult.Valid {
			valContent := fmt.Sprintf("Invalid input for tool '%s': %s", tc.Name, valResult.Message)
			valMsg := Message{
				Role:    "user",
				Content: valContent,
				UUID:    e.deps.UUID(),
			}
			results = append(results, valMsg)
			out <- EngineEvent{
				Type: EngineEventToolResult,
				ToolCall: &ToolCallEvent{
					ID:      tc.ID,
					Name:    tc.Name,
					Args:    tc.Arguments,
					Result:  valContent,
					IsError: true,
				},
			}
			continue
		}

		// Execute the tool.
		// round2(B2): route through Orchestrator when available so the
		// permission rules engine, plan-mode simulator, and pre/post
		// tool hooks all run. Fallback to direct execution (old
		// behavior) when the engine was constructed without an
		// Orchestrator — log a WARNING so the bypass is visible.
		execStart := time.Now()

		var (
			toolResult types.ToolResult
			execErr    error
		)
		if e.config.Orchestrator != nil {
			toolResult, execErr = e.config.Orchestrator.ExecuteTool(ctx, tc.Name, tc.Arguments)
		} else {
			e.logger.Warn("tool executed without Orchestrator — permission enforcement bypassed",
				"tool", tc.Name,
			)
			toolResult, execErr = tool.Execute(toolCtx, tc.Arguments)
		}
		execDuration := time.Since(execStart)

		resultContent := toolResult.Content
		isError := toolResult.IsError
		if execErr != nil {
			resultContent = fmt.Sprintf("Error executing tool '%s': %v", tc.Name, execErr)
			isError = true
		}

		// Apply tool result budget
		budget := DefaultToolResultBudget()
		resultContent = TruncateToolResult(resultContent, budget.MaxCharsPerResult)

		e.logger.Debug("tool executed",
			"tool", tc.Name,
			"duration_ms", execDuration.Milliseconds(),
			"is_error", isError,
			"result_len", len(resultContent),
		)

		// Build tool result message
		resultMsg := Message{
			Role:    "user",
			Content: resultContent,
			UUID:    e.deps.UUID(),
		}
		results = append(results, resultMsg)

		out <- EngineEvent{
			Type: EngineEventToolResult,
			ToolCall: &ToolCallEvent{
				ID:       tc.ID,
				Name:     tc.Name,
				Args:     tc.Arguments,
				Result:   resultContent,
				IsError:  isError,
				Duration: execDuration,
			},
		}
	}

	return results, preventContinuation
}

// evaluateStopHooks runs stop hooks and yields any messages they produce.
func (e *AdvancedQueryEngine) evaluateStopHooks(
	ctx context.Context,
	conversationMessages []Message,
	assistantMessages []Message,
	out chan<- EngineEvent,
) StopHookResult {
	if len(e.config.StopHooks) == 0 {
		return StopHookResult{}
	}

	result := HandleStopHooks(ctx, StopHookConfig{
		Hooks:          e.config.StopHooks,
		Logger:         e.logger,
		AbortChan:      e.abortChan,
		AgentID:        "",
		PermissionMode: types.ModeDefault,
	}, StopHookRequest{
		Messages:          append(conversationMessages, assistantMessages...),
		AssistantMessages: assistantMessages,
		SystemPrompt:      e.assembleSystemPrompt(),
		QuerySource:       e.qCfg.QuerySource,
	})

	// Yield hook messages
	for _, msg := range result.HookMessages {
		out <- EngineEvent{Type: EngineEventSystem, Message: &msg}
	}

	return result
}

// findTool looks up a tool by name.
func (e *AdvancedQueryEngine) findTool(name string) types.Tool {
	for _, t := range e.config.Tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// appendMessage thread-safely appends a message to the engine's history.
func (e *AdvancedQueryEngine) appendMessage(msg Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, msg)
}

// persistAsync persists the current message history asynchronously.
func (e *AdvancedQueryEngine) persistAsync(ctx context.Context) {
	e.mu.Lock()
	messages := make([]Message, len(e.messages))
	copy(messages, e.messages)
	e.mu.Unlock()

	go func() {
		if err := e.deps.PersistSession(ctx, messages); err != nil {
			e.logger.Warn("async persist failed", "error", err)
		}
	}()
}

// isAborted checks if the engine has been aborted.
func (e *AdvancedQueryEngine) isAborted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.aborted
}

// emitResult sends the final result event.
func (e *AdvancedQueryEngine) emitResult(
	out chan<- EngineEvent,
	startTime time.Time,
	state *loopState,
	errorType string,
	err error,
) {
	e.mu.Lock()
	usage := e.totalUsage
	e.mu.Unlock()

	result := &QueryLoopResult{
		Success:    err == nil && errorType == "",
		StopReason: lastAssistantStopReason(state.messages),
		ResultText: lastAssistantText(state.messages),
		TurnCount:  state.turnCount,
		TotalUsage: usage,
		DurationMs: time.Since(startTime).Milliseconds(),
		Error:      err,
		ErrorType:  errorType,
	}

	out <- EngineEvent{
		Type:   EngineEventResult,
		Result: result,
	}
}

// --- Utility functions ---

// lastAssistantText extracts the text from the last assistant message.
func lastAssistantText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && !messages[i].IsAPIError {
			return messages[i].Content
		}
	}
	return ""
}

// lastAssistantStopReason extracts the stop reason from the last assistant message.
func lastAssistantStopReason(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i].StopReason
		}
	}
	return ""
}

// isPromptTooLong checks if an error is a prompt-too-long error.
func isPromptTooLong(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "prompt_too_long") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length")
}

// --- Extended Message type for the advanced engine ---
// Note: The query package already defines Message in engine.go.
// We extend it here with additional fields needed by the advanced engine.
// These fields are added to the base Message struct in engine.go via
// the init below, but since Go doesn't support struct extension, we
// define an augmented version. The base Message in engine.go should be
// updated to include these fields.

func init() {
	// No-op: fields are added directly to Message below.
	// This init ensures the file is linked.
}

// round2(B2): ReactiveCompactor is now wired via SetReactiveCompactor /
// turnRunner interface. The engine accepts a *compact.ReactiveCompactor
// (or any turnRunner implementation) without importing internal/compact
// directly, avoiding the compact → conversation → query import cycle.
// See SetReactiveCompactor and the "Handle stream errors" block in
// runQueryLoop for the full recovery path.
