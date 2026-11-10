package subagent

import (
	"context"
	"log/slog"
	"time"

	"github.com/costa/polypod/internal/hooks"
)

// fireLifecycle delivers sub-agent events through both supported hook APIs.
// Lifecycle hooks observe a run; they do not bypass the ordinary tool path.
func (e *SubAgentExecutor) fireLifecycle(
	ctx context.Context, event hooks.HookEvent, agentName, runID string, duration time.Duration, runErr error,
) {
	if e.hooks == nil {
		return
	}
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	var agentEvent hooks.AgentEvent
	switch event {
	case hooks.EventSubagentStart:
		agentEvent = hooks.StartEvent(agentName, runID)
	case hooks.EventSubagentStop:
		if runErr != nil {
			agentEvent = hooks.ErrorEvent(agentName, runID, runErr)
		} else {
			agentEvent = hooks.FinishEvent(agentName, runID, duration.Milliseconds())
		}
	default:
		return
	}
	hooks.FireAgentEvent(hookCtx, e.hooks, agentEvent)

	args := map[string]any{
		"agent_name": agentName,
		"agent_type": agentName,
		"agent_id":   runID,
		"run_id":     runID,
	}
	if event == hooks.EventSubagentStop {
		args["duration_ms"] = duration.Milliseconds()
		args["status"] = "stopped"
		if runErr != nil {
			args["status"] = "failed"
			args["error"] = runErr.Error()
		}
	}
	_, err := e.hooks.Fire(hookCtx, hooks.LegacyPayload{
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		ToolName:  agentName,
		ToolArgs:  args,
	})
	if err != nil {
		logger := e.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("subagent lifecycle hook failed", "event", event, "agent", agentName, "error", err)
	}
}
