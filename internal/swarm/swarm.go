// Package swarm implements multi-agent team coordination for Polypod.
//
// It provides goroutine-based concurrency with channel-based messaging,
// permission delegation from workers to leaders, and full lifecycle
// management for teams of cooperative AI agents.
//
// Architecture mirrors Claude Code's swarm system (teamHelpers, spawnInProcess,
// inProcessRunner, permissionSync, leaderPermissionBridge, coordinatorMode)
// but uses Go idioms: goroutines instead of AsyncLocalStorage, channels
// instead of file-based mailboxes, sync primitives instead of polling loops.
package swarm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// TeamLeaderName is the canonical name for the team leader agent.
const TeamLeaderName = "team-lead"

// TeamState represents the lifecycle state of a team.
type TeamState int

const (
	TeamStateCreated TeamState = iota
	TeamStateRunning
	TeamStatePaused
	TeamStateStopping
	TeamStateStopped
)

// String returns a human-readable team state name.
func (s TeamState) String() string {
	switch s {
	case TeamStateCreated:
		return "created"
	case TeamStateRunning:
		return "running"
	case TeamStatePaused:
		return "paused"
	case TeamStateStopping:
		return "stopping"
	case TeamStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// TeamConfig configures a team's constraints and defaults.
type TeamConfig struct {
	// MaxMembers is the maximum number of teammates (excluding leader). 0 = unlimited.
	MaxMembers int
	// DefaultModel is the LLM model assigned to new teammates.
	DefaultModel string
	// DefaultTokenBudget is the per-teammate token budget. 0 = unlimited.
	DefaultTokenBudget int
	// DefaultTimeout is the per-turn timeout for teammates.
	DefaultTimeout time.Duration
	// Permissions defines the base permission mode for workers.
	Permissions types.PermissionMode
	// AllowedTools is the set of tools teammates are allowed to use.
	// Empty = all tools allowed.
	AllowedTools []string
	// PlanModeRequired forces teammates into plan-then-execute flow.
	PlanModeRequired bool
}

// DefaultTeamConfig returns sensible defaults for a team configuration.
func DefaultTeamConfig() TeamConfig {
	return TeamConfig{
		MaxMembers:         20,
		DefaultTokenBudget: 0,
		DefaultTimeout:     5 * time.Minute,
		Permissions:        types.ModeDefault,
	}
}

// MemberInfo describes a teammate registered in a team.
type MemberInfo struct {
	AgentID          string            `json:"agent_id"`
	Name             string            `json:"name"`
	AgentType        string            `json:"agent_type,omitempty"`
	Model            string            `json:"model,omitempty"`
	Color            string            `json:"color,omitempty"`
	PlanModeRequired bool              `json:"plan_mode_required,omitempty"`
	JoinedAt         time.Time         `json:"joined_at"`
	IsIdle           bool              `json:"is_idle"`
	TaskID           types.TaskID      `json:"task_id,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// Team represents a group of coordinated agents led by a team leader.
type Team struct {
	mu sync.RWMutex

	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Config      TeamConfig `json:"config"`
	State       TeamState  `json:"state"`

	LeaderID   string       `json:"leader_id"`
	LeaderName string       `json:"leader_name"`
	Members    []MemberInfo `json:"members"`

	// Runtime
	router       *MessageRouter
	perms        *PermissionBridge
	cancel       context.CancelFunc
	ctx          context.Context
	runners      map[string]*TeammateRunner // agentName → runner
	retiredNames map[string]bool            // guarded by mu; never reuse a mailbox identity in one run
	logger       *slog.Logger
	journal      *TeamJournal
	checkpoints  *workerCheckpointStore
	runID        string
	// teamLock excludes another process from creating or restoring this name.
	teamLock *os.File
	// resumeLock is held for a team rebuilt from a checkpoint until Stop.
	resumeLock *os.File
}

// --- SwarmManager ---

// SwarmManager manages all active teams in the process.
type SwarmManager struct {
	mu          sync.RWMutex
	teams       map[string]*Team
	logger      *slog.Logger
	journal     *TeamJournal
	checkpoints *workerCheckpointStore
}

// NewSwarmManager creates a new swarm manager.
func NewSwarmManager(logger *slog.Logger) *SwarmManager {
	return &SwarmManager{
		teams:  make(map[string]*Team),
		logger: logger,
	}
}

// EnableJournal keeps team messages and turn outcomes available after restart.
func (sm *SwarmManager) EnableJournal(path string) error {
	j, err := NewTeamJournal(path)
	if err != nil {
		return err
	}
	store, err := newWorkerCheckpointStore(filepath.Join(path, "worker-checkpoints"))
	if err != nil {
		_ = j.Close()
		return err
	}
	j.logger = sm.logger
	sm.mu.Lock()
	sm.journal = j
	sm.checkpoints = store
	sm.mu.Unlock()
	return nil
}

// JournalHistory returns a page of recorded runs, newest created run first.
func (sm *SwarmManager) JournalHistory(query RunQuery) (RunPage, error) {
	j := sm.Journal()
	if j == nil {
		return RunPage{}, nil
	}
	return j.History(query)
}

// PruneJournal applies an explicit retention policy.
func (sm *SwarmManager) PruneJournal(policy RetentionPolicy) (PruneReport, error) {
	j := sm.Journal()
	if j == nil {
		return PruneReport{}, nil
	}
	return j.Prune(policy)
}

func (sm *SwarmManager) Journal() *TeamJournal {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.journal
}

// CreateTeam creates a new team and registers the leader.
// The team starts in Created state. Call team.Start() to begin operations.
func (sm *SwarmManager) CreateTeam(name, description string, cfg TeamConfig) (*Team, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.teams[name]; exists {
		return nil, fmt.Errorf("%w: %s", ErrTeamExists, name)
	}
	var teamLock *os.File
	if sm.checkpoints != nil {
		var err error
		teamLock, err = sm.checkpoints.lockTeam(name)
		if err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	leaderID := FormatAgentID(TeamLeaderName, name)

	router := NewMessageRouter(name, sm.logger)
	router.Register(TeamLeaderName)

	team := &Team{
		Name:        name,
		Description: description,
		CreatedAt:   time.Now(),
		Config:      cfg,
		State:       TeamStateCreated,
		LeaderID:    leaderID,
		LeaderName:  TeamLeaderName,
		Members: []MemberInfo{
			{
				AgentID:  leaderID,
				Name:     TeamLeaderName,
				JoinedAt: time.Now(),
			},
		},
		router:       router,
		cancel:       cancel,
		ctx:          ctx,
		runners:      make(map[string]*TeammateRunner),
		retiredNames: make(map[string]bool),
		logger:       sm.logger,
		journal:      sm.journal,
		checkpoints:  sm.checkpoints,
		teamLock:     teamLock,
		runID:        fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	router.journal = sm.journal
	router.runID = team.runID
	if teamLock != nil {
		if err := persistTeamOwner(teamLock, team.runID); err != nil {
			cancel()
			releaseResumeLock(teamLock)
			return nil, fmt.Errorf("persisting team owner: %w", err)
		}
	}

	team.perms = NewPermissionBridge(team, sm.logger)

	if sm.journal != nil {
		if err := sm.journal.append(journalEvent{Kind: "created", Team: name, RunID: team.runID,
			ResumeProtocol: teamResumeProtocolVersion, Config: &cfg, Summary: ptrSummary(team.Summary())}); err != nil {
			cancel()
			releaseResumeLock(teamLock)
			return nil, err
		}
		if err := sm.journal.append(journalEvent{Kind: "member", Team: name, RunID: team.runID,
			Member: &team.Members[0]}); err != nil {
			cancel()
			releaseResumeLock(teamLock)
			return nil, err
		}
	}
	sm.teams[name] = team
	sm.logger.Info("team created", "team", name, "leader", leaderID)
	return team, nil
}

func ptrSummary(summary TeamSummary) *TeamSummary { return &summary }

// GetTeam returns a team by name.
func (sm *SwarmManager) GetTeam(name string) (*Team, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	t, ok := sm.teams[name]
	return t, ok
}

// DeleteTeam stops a team and removes it from the manager.
// All teammates are shut down gracefully with the given timeout.
func (sm *SwarmManager) DeleteTeam(name string, timeout time.Duration) error {
	sm.mu.RLock()
	team, ok := sm.teams[name]
	sm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrTeamNotFound, name)
	}

	stopErr := team.Stop(timeout)
	if stopErr != nil && team.Summary().State != TeamStateStopped.String() {
		return stopErr
	}
	sm.mu.Lock()
	if sm.teams[name] == team {
		delete(sm.teams, name)
	}
	sm.mu.Unlock()
	sm.logger.Info("team deleted", "team", name)
	return stopErr
}

// ListTeams returns info about all active teams.
func (sm *SwarmManager) ListTeams() []TeamSummary {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	summaries := make([]TeamSummary, 0, len(sm.teams))
	for _, t := range sm.teams {
		summaries = append(summaries, t.Summary())
	}
	return summaries
}

// ListRecordedTeams includes teams left behind by a previous process.
func (sm *SwarmManager) ListRecordedTeams() ([]TeamSummary, error) {
	live := sm.ListTeams()
	j := sm.Journal()
	if j == nil {
		return live, nil
	}
	archives, err := j.Teams()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(live))
	for _, item := range live {
		seen[item.Name] = true
	}
	for _, item := range archives {
		if !seen[item.Summary.Name] {
			live = append(live, item.Summary)
		}
	}
	return live, nil
}

// TeamSummary is a read-only snapshot of team state.
type TeamSummary struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	State       string    `json:"state"`
	MemberCount int       `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	LeaderID    string    `json:"leader_id"`
}

// Shutdown stops all teams with the given timeout.
func (sm *SwarmManager) Shutdown(timeout time.Duration) {
	sm.mu.Lock()
	teams := make([]*Team, 0, len(sm.teams))
	for _, t := range sm.teams {
		teams = append(teams, t)
	}
	sm.teams = make(map[string]*Team)
	sm.mu.Unlock()

	var wg sync.WaitGroup
	for _, t := range teams {
		wg.Add(1)
		go func(team *Team) {
			defer wg.Done()
			if err := team.Stop(timeout); err != nil {
				sm.logger.Warn("team stop error during shutdown", "team", team.Name, "error", err)
			}
		}(t)
	}
	wg.Wait()
	if sm.journal != nil {
		if err := sm.journal.Close(); err != nil {
			sm.logger.Warn("team journal close failed", "error", err)
		}
	}
	sm.logger.Info("swarm manager shut down", "teams_stopped", len(teams))
}

// --- Team methods ---

// Start transitions the team to Running state.
func (t *Team) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.State != TeamStateCreated && t.State != TeamStatePaused {
		return fmt.Errorf("cannot start team in state %s", t.State)
	}
	if err := t.persistStateLocked(TeamStateRunning); err != nil {
		return fmt.Errorf("persisting team start: %w", err)
	}
	t.State = TeamStateRunning
	t.logger.Info("team started", "team", t.Name)
	return nil
}

// Pause transitions the team to Paused state. Running teammates complete
// their current turn but do not pick up new work.
func (t *Team) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.State != TeamStateRunning {
		return fmt.Errorf("cannot pause team in state %s", t.State)
	}
	if err := t.persistStateLocked(TeamStatePaused); err != nil {
		return fmt.Errorf("persisting team pause: %w", err)
	}
	t.State = TeamStatePaused
	t.logger.Info("team paused", "team", t.Name)
	return nil
}

// Resume is an alias for Start when paused.
func (t *Team) Resume() error {
	return t.Start()
}

// Stop gracefully shuts down all teammates, then the team.
// Blocks until all runners exit or the timeout fires.
func (t *Team) Stop(timeout time.Duration) error {
	t.mu.Lock()
	if t.State == TeamStateStopped {
		t.mu.Unlock()
		return nil
	}
	// Commit the stop intent before cancelling workers. After a crash this
	// prevents a resumer from restarting work the owner chose to terminate.
	if t.journal != nil {
		if err := t.journal.append(journalEvent{Kind: "stopping", Team: t.Name, RunID: t.runID}); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("persisting team stop intent: %w", err)
		}
	}
	t.State = TeamStateStopping

	// Copy runners under lock.
	runners := make([]*TeammateRunner, 0, len(t.runners))
	for _, r := range t.runners {
		runners = append(runners, r)
	}
	t.mu.Unlock()

	// Cancel the team context — signals all runners.
	t.cancel()

	// Wait for all runners with a timeout.
	done := make(chan struct{})
	go func() {
		for _, r := range runners {
			r.Wait()
		}
		close(done)
	}()

	var err error
	select {
	case <-done:
		// All runners exited cleanly.
	case <-time.After(timeout):
		err = fmt.Errorf("timeout waiting for %d runners to stop", len(runners))
		t.logger.Warn("team stop timeout", "team", t.Name, "runners", len(runners))
	}
	if err != nil {
		return err
	}

	t.router.CloseAll()

	t.mu.Lock()
	t.State = TeamStateStopped
	t.runners = make(map[string]*TeammateRunner)
	resumeLock := t.resumeLock
	t.resumeLock = nil
	teamLock := t.teamLock
	t.teamLock = nil
	stateErr := t.persistStateLocked(TeamStateStopped)
	t.mu.Unlock()
	releaseResumeLock(resumeLock)
	releaseResumeLock(teamLock)

	t.logger.Info("team stopped", "team", t.Name)
	if stateErr != nil {
		return fmt.Errorf("persisting team stop: %w", stateErr)
	}
	return nil
}

// AddTeammate creates and registers a new teammate in the team.
// The teammate's runner is not started automatically; call StartTeammate
// to begin execution.
func (t *Team) AddTeammate(cfg TeammateConfig) (*MemberInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.State == TeamStateStopped || t.State == TeamStateStopping {
		return nil, ErrTeamNotRunning
	}

	// Check capacity.
	nonLeaderCount := 0
	for _, m := range t.Members {
		if m.Name != TeamLeaderName {
			nonLeaderCount++
		}
	}
	if t.Config.MaxMembers > 0 && nonLeaderCount >= t.Config.MaxMembers {
		return nil, ErrTeamFull
	}

	// Ensure unique name.
	uniqueName := t.generateUniqueName(cfg.Name)
	agentID := FormatAgentID(uniqueName, t.Name)

	// Check duplicate.
	for _, m := range t.Members {
		if m.AgentID == agentID {
			return nil, fmt.Errorf("%w: %s", ErrTeammateDuplicate, uniqueName)
		}
	}

	info := MemberInfo{
		AgentID:          agentID,
		Name:             uniqueName,
		AgentType:        cfg.AgentType,
		Model:            cfg.Model,
		Color:            cfg.Color,
		PlanModeRequired: cfg.PlanModeRequired,
		JoinedAt:         time.Now(),
		IsIdle:           true,
		Metadata:         cfg.Metadata,
	}
	if t.journal != nil {
		if err := t.journal.append(journalEvent{Kind: "member", Team: t.Name, RunID: t.runID,
			Member: &info}); err != nil {
			return nil, fmt.Errorf("persisting teammate: %w", err)
		}
	}
	// The member becomes visible only after its durable identity exists.
	t.router.Register(uniqueName)
	t.Members = append(t.Members, info)

	t.logger.Info("teammate added", "agent_id", agentID, "name", uniqueName, "team", t.Name)
	return &info, nil
}

// RemoveTeammate stops and removes a teammate by name.
func (t *Team) RemoveTeammate(name string) error {
	t.mu.Lock()

	idx := -1
	for i, m := range t.Members {
		if m.Name == name && m.Name != TeamLeaderName {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	if t.journal != nil {
		if err := t.journal.append(journalEvent{Kind: "member_removed", Team: t.Name,
			RunID: t.runID, Agent: name}); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("persisting teammate removal: %w", err)
		}
	}

	// Remove from members.
	t.Members = append(t.Members[:idx], t.Members[idx+1:]...)
	if t.retiredNames == nil {
		t.retiredNames = make(map[string]bool)
	}
	t.retiredNames[strings.ToLower(name)] = true

	// Stop runner if active.
	runner, hasRunner := t.runners[name]
	if hasRunner {
		delete(t.runners, name)
	}
	t.mu.Unlock()

	// Stop runner outside lock to avoid deadlock.
	if hasRunner {
		runner.Stop()
		runner.Wait()
	}

	t.router.Unregister(name)
	t.logger.Info("teammate removed", "name", name, "team", t.Name)
	return nil
}

// StartTeammate launches the execution loop for a teammate.
// The teammate must already be registered via AddTeammate.
func (t *Team) StartTeammate(name string, executor TeammateExecutor) error {
	return t.startTeammate(name, executor, nil)
}

// StartTeammateFromCheckpoint continues one interrupted turn from a validated
// checkpoint, then reads only new mailbox messages.
func (t *Team) StartTeammateFromCheckpoint(name string, executor TeammateExecutor, resume *workerTurnResume) error {
	if resume == nil || resume.Checkpoint == nil {
		return errors.New("checkpoint de retomada ausente")
	}
	return t.startTeammate(name, executor, resume)
}

func (t *Team) startTeammate(name string, executor TeammateExecutor, resume *workerTurnResume) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.State != TeamStateRunning {
		return ErrTeamNotRunning
	}

	// Find the member info.
	var info *MemberInfo
	for i := range t.Members {
		if t.Members[i].Name == name {
			info = &t.Members[i]
			break
		}
	}
	if info == nil {
		return fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	if resume != nil {
		if t.checkpoints == nil {
			return errors.New("retomada sem armazenamento de checkpoint")
		}
		if err := resume.Checkpoint.ValidateResume(); err != nil {
			return err
		}
		if resume.Turn < 1 || resume.Identity.AgentID != info.AgentID || resume.Identity.AgentName != info.Name ||
			resume.Identity.TeamName != t.Name || resume.Identity.PlanModeRequired != info.PlanModeRequired {
			return fmt.Errorf("checkpoint não corresponde à identidade de %s", name)
		}
	}

	// Don't double-start.
	if _, running := t.runners[name]; running {
		return fmt.Errorf("teammate %s is already running", name)
	}

	mb := t.router.GetMailbox(name)
	if mb == nil {
		return fmt.Errorf("no mailbox for teammate %s", name)
	}

	identity := TeammateIdentity{
		AgentID:          info.AgentID,
		AgentName:        info.Name,
		TeamName:         t.Name,
		Color:            info.Color,
		PlanModeRequired: info.PlanModeRequired,
	}
	agentType := info.AgentType
	if resume != nil {
		identity.Color = resume.Identity.Color
		identity.ParentSessionID = resume.Identity.ParentSessionID
		if resume.AgentType != "" {
			agentType = resume.AgentType
		}
	}
	runnerCfg := RunnerConfig{
		Identity:    identity,
		Mailbox:     mb,
		Router:      t.router,
		Executor:    executor,
		TokenBudget: t.Config.DefaultTokenBudget,
		TurnTimeout: t.Config.DefaultTimeout,
		Logger:      t.logger,
		Journal:     t.journal,
		RunID:       t.runID,
		AgentType:   agentType,
		Checkpoints: t.checkpoints,
		Resume:      resume,
	}

	runner := NewTeammateRunner(t.ctx, runnerCfg)
	t.runners[name] = runner
	info.IsIdle = false

	// Start the runner goroutine.
	runner.Start()

	t.logger.Info("teammate started", "name", name, "agent_id", info.AgentID, "team", t.Name)
	return nil
}

// GetMember returns a member's info by name.
func (t *Team) GetMember(name string) (*MemberInfo, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for i := range t.Members {
		if t.Members[i].Name == name {
			return &t.Members[i], true
		}
	}
	return nil, false
}

// ListMembers returns a snapshot of all members.
func (t *Team) ListMembers() []MemberInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]MemberInfo, len(t.Members))
	copy(out, t.Members)
	for i := range out {
		if runner, ok := t.runners[out[i].Name]; ok {
			out[i].IsIdle = runner.State() == RunnerStateIdle
		}
	}
	return out
}

// Router returns the team's message router for inter-agent messaging.
func (t *Team) Router() *MessageRouter {
	return t.router
}

// PermissionBridgeRef returns the team's permission bridge.
func (t *Team) PermissionBridgeRef() *PermissionBridge {
	return t.perms
}

// Context returns the team's context, which is cancelled on Stop.
func (t *Team) Context() context.Context {
	return t.ctx
}

// RunID identifies this process-local incarnation in the durable journal.
func (t *Team) RunID() string { return t.runID }

// Summary returns a read-only snapshot of the team.
func (t *Team) Summary() TeamSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TeamSummary{
		Name:        t.Name,
		Description: t.Description,
		State:       t.State.String(),
		MemberCount: len(t.Members),
		CreatedAt:   t.CreatedAt,
		LeaderID:    t.LeaderID,
	}
}

// RunningTeammates returns the names of currently running (non-idle) teammates.
func (t *Team) RunningTeammates() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var names []string
	for name, runner := range t.runners {
		if runner.State() == RunnerStateWorking {
			names = append(names, name)
		}
	}
	return names
}

// TeammateOutputs returns a snapshot of the worker's recent completed turns.
// The runner keeps a bounded history in memory for explicit result inspection.
func (t *Team) TeammateOutputs(name string) ([]TeammateOutput, bool) {
	t.mu.RLock()
	runner, ok := t.runners[name]
	t.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return runner.Outputs(), true
}

// IdleTeammates returns the names of idle teammates.
func (t *Team) IdleTeammates() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var idle []string
	for _, m := range t.Members {
		if runner, ok := t.runners[m.Name]; ok && runner.State() == RunnerStateIdle {
			idle = append(idle, m.Name)
		}
	}
	return idle
}

// SetMemberIdle updates a member's idle state.
func (t *Team) SetMemberIdle(name string, idle bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.Members {
		if t.Members[i].Name == name {
			t.Members[i].IsIdle = idle
			break
		}
	}
}

func (t *Team) record(event journalEvent) {
	if t.journal == nil {
		return
	}
	event.Team, event.RunID = t.Name, t.runID
	if err := t.journal.append(event); err != nil {
		t.logger.Warn("team journal write failed", "team", t.Name, "kind", event.Kind, "error", err)
	}
}

// persistStateLocked is called only while holding t.mu.
func (t *Team) persistStateLocked(state TeamState) error {
	if t.journal == nil {
		return nil
	}
	summary := TeamSummary{Name: t.Name, Description: t.Description, State: state.String(),
		MemberCount: len(t.Members), CreatedAt: t.CreatedAt, LeaderID: t.LeaderID}
	return t.journal.append(journalEvent{Kind: "state", Team: t.Name, RunID: t.runID, Summary: &summary})
}

// generateUniqueName returns a unique teammate name by appending a
// numeric suffix if the base name is taken.
func (t *Team) generateUniqueName(baseName string) string {
	existing := make(map[string]struct{})
	for _, m := range t.Members {
		existing[strings.ToLower(m.Name)] = struct{}{}
	}
	for name := range t.retiredNames {
		existing[name] = struct{}{}
	}
	if _, taken := existing[strings.ToLower(baseName)]; !taken {
		return baseName
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", baseName, suffix)
		if _, taken := existing[strings.ToLower(candidate)]; !taken {
			return candidate
		}
	}
}

// FormatAgentID builds a deterministic agent ID: "name@team".
func FormatAgentID(name, teamName string) string {
	return fmt.Sprintf("%s@%s", SanitizeName(name), SanitizeName(teamName))
}

// SanitizeName replaces non-alphanumeric characters with hyphens, lowercased.
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
