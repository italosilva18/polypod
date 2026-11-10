package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/subagent"
	"github.com/costa/polypod/internal/swarm"
)

// Limit the number of archived runs examined on one boot. Runs beyond this
// bound remain available to the explicit team_resume tool.
const maxAutoTeamResumeAttempts = 32

type teamRecovery struct {
	once     sync.Once
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	manager  *swarm.SwarmManager
	agents   *agent.Registry
	executor *subagent.SubAgentExecutor
	logger   *slog.Logger
}

// StartTeamRecovery starts a bounded background pass after all runtime tools,
// agents, channels, and delivery handlers have been wired. Ineligible runs
// are left untouched; ApplyResumeInterrupted remains the sole recovery gate.
func (sys *Systems) StartTeamRecovery(ctx context.Context) {
	if sys == nil || sys.teamRecovery == nil || ctx == nil {
		return
	}
	r := sys.teamRecovery
	r.once.Do(func() {
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		r.mu.Lock()
		r.cancel, r.done = cancel, done
		r.mu.Unlock()
		go func() {
			defer close(done)
			r.run(runCtx)
		}()
	})
}

// StopTeamRecovery waits for the recovery pass before Swarm.Shutdown snapshots
// live teams. Otherwise a late restoration could escape the shutdown sweep.
func (sys *Systems) StopTeamRecovery() {
	if sys == nil || sys.teamRecovery == nil {
		return
	}
	r := sys.teamRecovery
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (r *teamRecovery) run(ctx context.Context) {
	if err := ctx.Err(); err != nil || r.manager == nil || r.manager.Journal() == nil {
		return
	}
	logger := r.logger
	if logger == nil {
		logger = slog.Default()
	}
	archived, err := r.manager.Journal().Teams()
	if err != nil {
		logger.Warn("team auto recovery unavailable", "error", err)
		return
	}
	candidates := make([]swarm.ArchivedTeam, 0)
	for _, team := range archived {
		if team.Summary.State == swarm.TeamStateRunning.String() && !team.OwnerLive {
			candidates = append(candidates, team)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].Summary.CreatedAt.Equal(candidates[j].Summary.CreatedAt) {
			return candidates[i].Summary.CreatedAt.After(candidates[j].Summary.CreatedAt)
		}
		return candidates[i].Summary.Name < candidates[j].Summary.Name
	})
	deferred := 0
	if len(candidates) > maxAutoTeamResumeAttempts {
		deferred = len(candidates) - maxAutoTeamResumeAttempts
		candidates = candidates[:maxAutoTeamResumeAttempts]
	}
	factory := resumeExecutorFactory(r.agents, r.executor)
	resumed, skipped := 0, 0
	for _, team := range candidates {
		if ctx.Err() != nil {
			return
		}
		name := team.Summary.Name
		report, resumeErr := r.manager.ApplyResumeInterrupted(name, factory)
		if ctx.Err() != nil {
			// The run may have been restored while shutdown began. The caller
			// waits for this pass before Swarm.Shutdown, which will stop it.
			return
		}
		if resumeErr != nil {
			skipped++
			logger.Warn("team auto recovery skipped", "team", name, "error", resumeErr)
			continue
		}
		if !report.Continued {
			skipped++
			logger.Info("team auto recovery skipped", "team", name, "reason", report.Note)
			continue
		}
		resumed++
		logger.Info("team auto recovery resumed", "team", name, "run_id", report.RunID,
			"members", len(report.Members))
	}
	if len(candidates) > 0 || deferred > 0 {
		logger.Info("team auto recovery finished", "resumed", resumed, "skipped", skipped,
			"deferred", deferred)
	}
}

func resumeExecutorFactory(agents *agent.Registry, executor *subagent.SubAgentExecutor) swarm.ResumeExecutorFactory {
	return func(info swarm.MemberInfo) (swarm.TeammateExecutor, error) {
		if agents == nil || executor == nil || info.AgentType == "" || !agents.Has(info.AgentType) {
			return nil, fmt.Errorf("agente %s indisponível", info.AgentType)
		}
		ag := agents.Get(info.AgentType)
		if ag == nil || ag.Name != info.AgentType {
			return nil, fmt.Errorf("agente %s não corresponde ao tipo salvo", info.AgentType)
		}
		return checkpointTeammateExecutor(executor, ag.Name), nil
	}
}
