package query

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/costa/polypod/internal/types"
)

// Stop hook evaluation after model response.
// Migrated from Claude Code query/stopHooks.ts.
//
// Stop hooks run between the model's final response and the next turn (or
// conversation end). They inspect the assistant's output and can:
//   - Block the response with an error (blocking error → retry turn)
//   - Prevent continuation entirely (end the conversation)
//   - Produce progress/attachment messages for the stream
//
// The evaluation order is:
//  1. Custom stop hooks (user-configured pre-turn validation)
//  2. Task completed hooks (for teammate agents)
//  3. Teammate idle hooks (for teammate agents)

// StopHookResult is the outcome of running all stop hooks for a turn.
type StopHookResult struct {
	// BlockingErrors are messages that should be fed back to the model
	// for correction. If non-empty, the query loop retries with these
	// appended to the conversation.
	BlockingErrors []Message

	// PreventContinuation stops the query loop entirely when true.
	PreventContinuation bool

	// StopReason is a human-readable explanation when PreventContinuation is true.
	StopReason string

	// HookMessages are additional messages produced by hooks (progress,
	// attachments) that should be yielded to the stream.
	HookMessages []Message
}

// StopHookConfig configures stop hook behavior.
type StopHookConfig struct {
	// Hooks is the list of registered stop hooks, evaluated in order.
	Hooks []StopHook

	// Logger for structured logging.
	Logger *slog.Logger

	// AbortChan allows cancelling hook execution.
	AbortChan <-chan struct{}

	// AgentID is set for subagent queries; empty for main thread.
	AgentID string

	// PermissionMode controls hook execution permissions.
	PermissionMode types.PermissionMode

	// StopHookActive tracks whether a stop hook already fired in a previous
	// iteration of the same turn. Prevents double-firing.
	StopHookActive bool

	// IsTeammate is true when the current agent is a teammate.
	IsTeammate bool

	// TeammateName is the name of this teammate (if IsTeammate).
	TeammateName string

	// TeamName is the name of the team this teammate belongs to.
	TeamName string
}

// StopHook defines the interface for a stop condition evaluator.
type StopHook interface {
	// Name returns a unique identifier for this hook.
	Name() string

	// Evaluate inspects the conversation state and returns a decision.
	// The hook receives all messages including the latest assistant response.
	Evaluate(ctx context.Context, req StopHookRequest) (*StopHookDecision, error)

	// Priority determines evaluation order (lower = earlier).
	// Standard priorities: 0-99 = system, 100-199 = user, 200+ = low priority.
	Priority() int
}

// StopHookRequest is the input to a stop hook evaluation.
type StopHookRequest struct {
	// Messages is the full conversation including the latest assistant response.
	Messages []Message

	// AssistantMessages are only the assistant messages from this turn
	// (may include multiple if the model produced parallel tool calls).
	AssistantMessages []Message

	// SystemPrompt is the active system prompt.
	SystemPrompt string

	// QuerySource identifies where the query originated.
	QuerySource string
}

// StopHookDecision is the output of a single stop hook evaluation.
type StopHookDecision struct {
	// BlockingError is a message to feed back to the model.
	// Empty means the hook passed.
	BlockingError string

	// PreventContinuation stops the query loop when true.
	PreventContinuation bool

	// StopReason explains why continuation was prevented.
	StopReason string

	// ProgressMessages are informational messages to yield.
	ProgressMessages []Message
}

// HandleStopHooks evaluates all registered stop hooks after a model response.
// It returns a StopHookResult that the query loop uses to decide whether to
// continue, retry, or stop.
//
// The function respects context cancellation and the abort channel.
func HandleStopHooks(
	ctx context.Context,
	cfg StopHookConfig,
	req StopHookRequest,
) StopHookResult {
	startTime := time.Now()
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := StopHookResult{}

	// Sort hooks by priority
	hooks := sortHooksByPriority(cfg.Hooks)

	// Evaluate each hook
	for _, hook := range hooks {
		// Check for abort
		select {
		case <-ctx.Done():
			logger.Debug("stop hooks cancelled by context", "hook", hook.Name())
			result.PreventContinuation = true
			result.StopReason = "cancelled"
			return result
		default:
		}

		if cfg.AbortChan != nil {
			select {
			case <-cfg.AbortChan:
				logger.Debug("stop hooks aborted", "hook", hook.Name())
				result.PreventContinuation = true
				result.StopReason = "aborted"
				return result
			default:
			}
		}

		hookStart := time.Now()
		decision, err := hook.Evaluate(ctx, req)
		hookDuration := time.Since(hookStart)

		if err != nil {
			logger.Warn("stop hook evaluation failed",
				"hook", hook.Name(),
				"error", err,
				"duration_ms", hookDuration.Milliseconds(),
			)
			// Hook errors are non-fatal: log and continue to next hook.
			result.HookMessages = append(result.HookMessages, Message{
				Role:    "system",
				Content: fmt.Sprintf("Stop hook '%s' failed: %v", hook.Name(), err),
			})
			continue
		}

		if decision == nil {
			continue
		}

		// Collect progress messages
		result.HookMessages = append(result.HookMessages, decision.ProgressMessages...)

		logger.Debug("stop hook evaluated",
			"hook", hook.Name(),
			"blocking", decision.BlockingError != "",
			"prevent", decision.PreventContinuation,
			"duration_ms", hookDuration.Milliseconds(),
		)

		// Handle blocking error
		if decision.BlockingError != "" {
			blockingMsg := Message{
				Role:    "user",
				Content: formatStopHookMessage(hook.Name(), decision.BlockingError),
			}
			result.BlockingErrors = append(result.BlockingErrors, blockingMsg)
		}

		// Handle prevention
		if decision.PreventContinuation {
			result.PreventContinuation = true
			result.StopReason = decision.StopReason
			if result.StopReason == "" {
				result.StopReason = fmt.Sprintf("Stop hook '%s' prevented continuation", hook.Name())
			}

			logger.Info("stop hook prevented continuation",
				"hook", hook.Name(),
				"reason", result.StopReason,
				"total_duration_ms", time.Since(startTime).Milliseconds(),
			)

			return result
		}
	}

	// Run teammate-specific hooks if applicable
	if cfg.IsTeammate && !result.PreventContinuation {
		teammateResult := handleTeammateHooks(ctx, cfg, req, logger)
		result.BlockingErrors = append(result.BlockingErrors, teammateResult.BlockingErrors...)
		result.HookMessages = append(result.HookMessages, teammateResult.HookMessages...)
		if teammateResult.PreventContinuation {
			result.PreventContinuation = true
			result.StopReason = teammateResult.StopReason
		}
	}

	logger.Debug("stop hooks completed",
		"blocking_errors", len(result.BlockingErrors),
		"prevent_continuation", result.PreventContinuation,
		"total_duration_ms", time.Since(startTime).Milliseconds(),
	)

	return result
}

// handleTeammateHooks evaluates TaskCompleted and TeammateIdle hooks
// for agents operating as teammates.
func handleTeammateHooks(
	ctx context.Context,
	cfg StopHookConfig,
	req StopHookRequest,
	logger *slog.Logger,
) StopHookResult {
	result := StopHookResult{}

	// Evaluate task completion hooks
	for _, hook := range cfg.Hooks {
		if tcHook, ok := hook.(TaskCompletedHook); ok {
			decision, err := tcHook.EvaluateTaskCompletion(ctx, TaskCompletionRequest{
				Messages:     req.Messages,
				TeammateName: cfg.TeammateName,
				TeamName:     cfg.TeamName,
			})
			if err != nil {
				logger.Warn("task completed hook failed",
					"hook", hook.Name(),
					"error", err,
				)
				continue
			}
			if decision != nil {
				result.HookMessages = append(result.HookMessages, decision.ProgressMessages...)
				if decision.BlockingError != "" {
					result.BlockingErrors = append(result.BlockingErrors, Message{
						Role:    "user",
						Content: formatTaskCompletedMessage(hook.Name(), decision.BlockingError),
					})
				}
				if decision.PreventContinuation {
					result.PreventContinuation = true
					result.StopReason = decision.StopReason
					return result
				}
			}
		}
	}

	// Evaluate teammate idle hooks
	for _, hook := range cfg.Hooks {
		if idleHook, ok := hook.(TeammateIdleHook); ok {
			decision, err := idleHook.EvaluateTeammateIdle(ctx, TeammateIdleRequest{
				Messages:     req.Messages,
				TeammateName: cfg.TeammateName,
				TeamName:     cfg.TeamName,
			})
			if err != nil {
				logger.Warn("teammate idle hook failed",
					"hook", hook.Name(),
					"error", err,
				)
				continue
			}
			if decision != nil {
				result.HookMessages = append(result.HookMessages, decision.ProgressMessages...)
				if decision.BlockingError != "" {
					result.BlockingErrors = append(result.BlockingErrors, Message{
						Role:    "user",
						Content: formatTeammateIdleMessage(hook.Name(), decision.BlockingError),
					})
				}
				if decision.PreventContinuation {
					result.PreventContinuation = true
					result.StopReason = decision.StopReason
					return result
				}
			}
		}
	}

	return result
}

// --- Optional hook interfaces for specialized evaluations ---

// TaskCompletedHook extends StopHook with task completion detection.
type TaskCompletedHook interface {
	StopHook
	EvaluateTaskCompletion(ctx context.Context, req TaskCompletionRequest) (*StopHookDecision, error)
}

// TaskCompletionRequest provides context for task completion evaluation.
type TaskCompletionRequest struct {
	Messages     []Message
	TeammateName string
	TeamName     string
	TaskID       string
	TaskSubject  string
}

// TeammateIdleHook extends StopHook with teammate idle detection.
type TeammateIdleHook interface {
	StopHook
	EvaluateTeammateIdle(ctx context.Context, req TeammateIdleRequest) (*StopHookDecision, error)
}

// TeammateIdleRequest provides context for teammate idle evaluation.
type TeammateIdleRequest struct {
	Messages     []Message
	TeammateName string
	TeamName     string
}

// --- Hook sorting ---

type hooksByPriority []StopHook

func (h hooksByPriority) Len() int           { return len(h) }
func (h hooksByPriority) Less(i, j int) bool { return h[i].Priority() < h[j].Priority() }
func (h hooksByPriority) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func sortHooksByPriority(hooks []StopHook) []StopHook {
	sorted := make([]StopHook, len(hooks))
	copy(sorted, hooks)
	// Simple insertion sort — hook count is always small (<10).
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j].Priority() > key.Priority() {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}
	return sorted
}

// --- Message formatting ---

func formatStopHookMessage(hookName, error string) string {
	return fmt.Sprintf("[Stop hook '%s']: %s\nPlease address this issue and try again.", hookName, error)
}

func formatTaskCompletedMessage(hookName, error string) string {
	return fmt.Sprintf("[TaskCompleted hook '%s']: %s", hookName, error)
}

func formatTeammateIdleMessage(hookName, error string) string {
	return fmt.Sprintf("[TeammateIdle hook '%s']: %s", hookName, error)
}

// --- Built-in stop hooks ---

// MaxTurnsStopHook stops the query loop when the turn count exceeds a limit.
type MaxTurnsStopHook struct {
	MaxTurns int
}

func (h *MaxTurnsStopHook) Name() string  { return "max_turns" }
func (h *MaxTurnsStopHook) Priority() int { return 0 }

func (h *MaxTurnsStopHook) Evaluate(_ context.Context, req StopHookRequest) (*StopHookDecision, error) {
	if h.MaxTurns <= 0 {
		return nil, nil
	}
	// Count user messages as a proxy for turns
	turns := 0
	for _, m := range req.Messages {
		if m.Role == "user" {
			turns++
		}
	}
	if turns > h.MaxTurns {
		return &StopHookDecision{
			PreventContinuation: true,
			StopReason:          fmt.Sprintf("reached maximum number of turns (%d)", h.MaxTurns),
		}, nil
	}
	return nil, nil
}

// APIErrorStopHook prevents retrying after API errors (rate limit, auth, etc.)
// to avoid death spirals: error → hook → retry → error → ...
type APIErrorStopHook struct{}

func (h *APIErrorStopHook) Name() string  { return "api_error" }
func (h *APIErrorStopHook) Priority() int { return 0 }

func (h *APIErrorStopHook) Evaluate(_ context.Context, req StopHookRequest) (*StopHookDecision, error) {
	if len(req.AssistantMessages) == 0 {
		return nil, nil
	}
	last := req.AssistantMessages[len(req.AssistantMessages)-1]
	if last.IsAPIError {
		return &StopHookDecision{
			PreventContinuation: true,
			StopReason:          "api error — skipping hooks to prevent retry spiral",
		}, nil
	}
	return nil, nil
}
