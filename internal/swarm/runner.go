package swarm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// TeammateIdentity identifies a teammate within a team. Equivalent to
// Claude Code's TeammateIdentity type in InProcessTeammateTask/types.ts.
type TeammateIdentity struct {
	AgentID          string `json:"agent_id"`   // e.g., "researcher@my-team"
	AgentName        string `json:"agent_name"` // e.g., "researcher"
	TeamName         string `json:"team_name"`
	Color            string `json:"color,omitempty"`
	PlanModeRequired bool   `json:"plan_mode_required,omitempty"`
	ParentSessionID  string `json:"parent_session_id,omitempty"`
}

// TeammateConfig is the configuration for adding a teammate to a team.
type TeammateConfig struct {
	Name             string
	AgentType        string
	Model            string
	Color            string
	PlanModeRequired bool
	Prompt           string
	SystemPrompt     string
	SystemPromptMode string // "default", "replace", "append"
	AllowedTools     []string
	Description      string
	Metadata         map[string]string
}

// TeammateExecutor is the function that performs one "turn" of agent work.
// It receives the prompt, returns the assistant's response text, or an error.
// The executor is responsible for calling the LLM, executing tool calls, etc.
// It MUST respect context cancellation.
type TeammateExecutor func(ctx context.Context, prompt string, identity TeammateIdentity) (response string, err error)

// RunnerConfig configures a TeammateRunner.
type RunnerConfig struct {
	Identity    TeammateIdentity
	Mailbox     *Mailbox
	Router      *MessageRouter
	Executor    TeammateExecutor
	TokenBudget int           // 0 = unlimited
	TurnTimeout time.Duration // per-turn timeout; 0 = 5 minutes
	Logger      *slog.Logger
	Journal     *TeamJournal
	RunID       string
}

// RunnerState tracks the teammate runner's lifecycle.
type RunnerState int32

const (
	RunnerStateIdle    RunnerState = 0
	RunnerStateWorking RunnerState = 1
	RunnerStateStopped RunnerState = 2
)

// TeammateOutput holds the result of a single teammate turn.
type TeammateOutput struct {
	Response  string
	Error     error
	Prompt    string
	StartTime time.Time
	EndTime   time.Time
	TurnIndex int
}

// TeammateRunner runs a single teammate agent in a goroutine. It reads
// messages from the teammate's mailbox, feeds them to the executor, collects
// output, and sends idle/completion notifications back to the leader.
//
// Lifecycle: Start → (work turns) → Stop → Wait.
// The runner stays alive between turns (idle), picking up new messages.
// This mirrors Claude Code's inProcessRunner.ts which loops
// runAgent() + waitForNextPromptOrShutdown().
type TeammateRunner struct {
	mu      sync.Mutex
	cfg     RunnerConfig
	state   int32 // atomic RunnerState
	started atomic.Bool
	cancel  context.CancelFunc
	ctx     context.Context
	done    chan struct{}
	logger  *slog.Logger

	// Accumulated outputs.
	outputsMu sync.Mutex
	outputs   []TeammateOutput
	turnCount int32 // atomic

	// Token accounting.
	tokensUsed int64 // atomic

	// Work-abort: allows aborting current turn without killing the runner.
	// Mirrors Claude Code's currentWorkAbortController.
	workCancel context.CancelFunc
	workCtx    context.Context

	// Callbacks notified on idle transition.
	idleCbs   []func()
	idleCbsMu sync.Mutex
}

// NewTeammateRunner creates a runner but does not start it. Call Start().
func NewTeammateRunner(parentCtx context.Context, cfg RunnerConfig) *TeammateRunner {
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = 5 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(parentCtx)

	r := &TeammateRunner{
		cfg:    cfg,
		cancel: cancel,
		ctx:    ctx,
		done:   make(chan struct{}),
		logger: cfg.Logger,
	}
	// Initialize work context as cancelled (no work yet).
	r.workCtx, r.workCancel = context.WithCancel(ctx)
	r.workCancel()

	return r
}

// Start launches the runner goroutine. Idempotent if already started.
func (r *TeammateRunner) Start() {
	if !r.started.CompareAndSwap(false, true) {
		return // already running or stopped
	}
	go r.loop()
}

// Stop signals the runner to exit after its current turn.
func (r *TeammateRunner) Stop() {
	r.cancel()
}

// AbortCurrentWork cancels the current turn only, not the runner lifecycle.
// The runner returns to idle and picks up the next message.
func (r *TeammateRunner) AbortCurrentWork() {
	r.mu.Lock()
	if r.workCancel != nil {
		r.workCancel()
	}
	r.mu.Unlock()
}

// Wait blocks until the runner goroutine exits.
func (r *TeammateRunner) Wait() {
	<-r.done
}

// State returns the current runner state.
func (r *TeammateRunner) State() RunnerState {
	return RunnerState(atomic.LoadInt32(&r.state))
}

// Outputs returns a copy of all turn outputs.
func (r *TeammateRunner) Outputs() []TeammateOutput {
	r.outputsMu.Lock()
	defer r.outputsMu.Unlock()
	out := make([]TeammateOutput, len(r.outputs))
	copy(out, r.outputs)
	return out
}

// TurnCount returns the number of completed turns.
func (r *TeammateRunner) TurnCount() int {
	return int(atomic.LoadInt32(&r.turnCount))
}

// TokensUsed returns the total tokens consumed.
func (r *TeammateRunner) TokensUsed() int64 {
	return atomic.LoadInt64(&r.tokensUsed)
}

// OnIdle registers a callback invoked when the runner transitions to idle.
func (r *TeammateRunner) OnIdle(cb func()) {
	r.idleCbsMu.Lock()
	r.idleCbs = append(r.idleCbs, cb)
	r.idleCbsMu.Unlock()
}

// loop is the main goroutine. It processes prompts from the mailbox
// in a continuous loop, going idle between turns.
func (r *TeammateRunner) loop() {
	defer close(r.done)
	defer atomic.StoreInt32(&r.state, int32(RunnerStateStopped))

	r.logger.Info("teammate runner started",
		"agent_id", r.cfg.Identity.AgentID,
		"agent_name", r.cfg.Identity.AgentName,
		"team", r.cfg.Identity.TeamName,
	)

	for {
		atomic.StoreInt32(&r.state, int32(RunnerStateIdle))
		// Check lifecycle cancellation.
		if r.ctx.Err() != nil {
			r.logger.Debug("teammate runner context cancelled",
				"agent_name", r.cfg.Identity.AgentName)
			return
		}

		// Block until a message arrives or context is cancelled.
		msg, err := r.cfg.Mailbox.Receive(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				r.logger.Debug("teammate runner shutting down",
					"agent_name", r.cfg.Identity.AgentName)
			} else {
				r.logger.Error("mailbox receive error",
					"agent_name", r.cfg.Identity.AgentName, "error", err)
			}
			return
		}

		// Handle shutdown requests by exiting the loop.
		if msg.Type == MessageShutdownRequest {
			r.logger.Info("teammate received shutdown request",
				"agent_name", r.cfg.Identity.AgentName,
				"from", msg.Sender,
			)
			// Send approval back to leader.
			approval := NewMessage(MessageShutdownApproved, r.cfg.Identity.AgentName, msg.Sender,
				fmt.Sprintf(`{"request_id":"%s","from":"%s"}`, msg.ID, r.cfg.Identity.AgentName))
			if mb := r.cfg.Mailbox; mb != nil {
				// Route through the same mailbox system — the router
				// will have the leader's mailbox too.
			}
			// Notify leader via the router if available.
			r.sendToLeader(approval)
			return
		}

		// Transition to working.
		atomic.StoreInt32(&r.state, int32(RunnerStateWorking))

		prompt := r.buildPrompt(msg)
		r.executeTurn(prompt)

		// Transition to idle.
		atomic.StoreInt32(&r.state, int32(RunnerStateIdle))
		r.fireIdleCallbacks()

		// Send idle notification to leader.
		r.sendIdleNotification()
	}
}

// executeTurn runs one turn of the executor with the given prompt.
func (r *TeammateRunner) executeTurn(prompt string) {
	turn := int(atomic.AddInt32(&r.turnCount, 1))

	// Create a per-turn cancellable context.
	r.mu.Lock()
	r.workCtx, r.workCancel = context.WithTimeout(r.ctx, r.cfg.TurnTimeout)
	workCancel := r.workCancel
	workCtx := r.workCtx
	r.mu.Unlock()
	defer workCancel()

	start := time.Now()
	r.recordTurn(journalEvent{Kind: "turn_started", Prompt: prompt, Turn: turn})

	r.logger.Debug("teammate turn starting",
		"agent_name", r.cfg.Identity.AgentName,
		"turn", turn,
		"prompt_len", len(prompt),
	)

	response, err := r.cfg.Executor(workCtx, prompt, r.cfg.Identity)

	output := TeammateOutput{
		Response:  response,
		Error:     err,
		Prompt:    prompt,
		StartTime: start,
		EndTime:   time.Now(),
		TurnIndex: turn,
	}

	r.outputsMu.Lock()
	r.outputs = append(r.outputs, output)
	if len(r.outputs) > 100 {
		r.outputs = append([]TeammateOutput(nil), r.outputs[len(r.outputs)-100:]...)
	}
	r.outputsMu.Unlock()
	finished := journalEvent{Kind: "turn_finished", Turn: turn, Result: response}
	if err != nil {
		finished.Error = err.Error()
	}
	r.recordTurn(finished)

	if err != nil {
		if r.ctx.Err() != nil {
			// Lifecycle cancellation — don't log as error.
			r.logger.Debug("teammate turn cancelled by shutdown",
				"agent_name", r.cfg.Identity.AgentName, "turn", turn)
			r.sendTurnNotification("cancelled", err.Error(), turn)
		} else if workCtx.Err() != nil {
			r.logger.Warn("teammate turn aborted",
				"agent_name", r.cfg.Identity.AgentName, "turn", turn, "error", err)
			r.sendTurnNotification("cancelled", err.Error(), turn)
		} else {
			r.logger.Error("teammate turn failed",
				"agent_name", r.cfg.Identity.AgentName, "turn", turn, "error", err)
			r.sendTurnNotification("failed", err.Error(), turn)
		}
	} else {
		r.logger.Debug("teammate turn completed",
			"agent_name", r.cfg.Identity.AgentName,
			"turn", turn,
			"duration", output.EndTime.Sub(output.StartTime),
			"response_len", len(response),
		)
		r.sendTurnNotification("completed", response, turn)
	}

	// Check token budget.
	if r.cfg.TokenBudget > 0 && r.TokensUsed() >= int64(r.cfg.TokenBudget) {
		r.logger.Warn("teammate token budget exhausted",
			"agent_name", r.cfg.Identity.AgentName,
			"budget", r.cfg.TokenBudget,
			"used", r.TokensUsed(),
		)
		r.Stop()
	}
}

func (r *TeammateRunner) recordTurn(event journalEvent) {
	if r.cfg.Journal == nil {
		return
	}
	event.Team = r.cfg.Identity.TeamName
	event.RunID = r.cfg.RunID
	event.Agent = r.cfg.Identity.AgentName
	if err := r.cfg.Journal.append(event); err != nil {
		r.logger.Warn("team turn journal write failed", "team", event.Team,
			"agent", event.Agent, "kind", event.Kind, "error", err)
	}
}

// buildPrompt formats an incoming message as a prompt string.
// Wraps teammate messages in XML tags like Claude Code's
// formatAsTeammateMessage().
func (r *TeammateRunner) buildPrompt(msg *Message) string {
	if msg.Sender == "user" || msg.Sender == "" {
		return msg.Content
	}
	colorAttr := ""
	if msg.Color != "" {
		colorAttr = fmt.Sprintf(` color="%s"`, msg.Color)
	}
	summaryAttr := ""
	if msg.Summary != "" {
		summaryAttr = fmt.Sprintf(` summary="%s"`, msg.Summary)
	}
	return fmt.Sprintf(`<teammate-message teammate_id="%s"%s%s>
%s
</teammate-message>`, msg.Sender, colorAttr, summaryAttr, msg.Content)
}

// sendIdleNotification tells the leader this teammate is available.
func (r *TeammateRunner) sendIdleNotification() {
	idleMsg := NewMessage(MessageStatusUpdate, r.cfg.Identity.AgentName, TeamLeaderName,
		fmt.Sprintf(`{"type":"idle","from":"%s","reason":"available"}`, r.cfg.Identity.AgentName))
	if idleMsg.Metadata == nil {
		idleMsg.Metadata = make(map[string]string)
	}
	idleMsg.Metadata["idle_reason"] = "available"
	r.sendToLeader(idleMsg)
}

// sendTurnNotification delivers a bounded preview; the complete result stays
// available through Outputs for explicit inspection by the team lead.
func (r *TeammateRunner) sendTurnNotification(status, content string, turn int) {
	const maxPreviewBytes = 16 << 10
	truncated := false
	if len(content) > maxPreviewBytes {
		cut := maxPreviewBytes
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		content = content[:cut]
		truncated = true
	}
	msg := NewMessage(MessageStatusUpdate, r.cfg.Identity.AgentName, TeamLeaderName, content)
	msg.Metadata = map[string]string{"status": status, "turn": fmt.Sprint(turn)}
	if truncated {
		msg.Metadata["truncated"] = "true"
	}
	r.sendToLeader(msg)
}

// sendToLeader routes a status or completion into the leader's real mailbox.
func (r *TeammateRunner) sendToLeader(msg *Message) {
	if r.cfg.Router == nil {
		r.logger.Warn("teammate leader mailbox unavailable", "agent_name", r.cfg.Identity.AgentName)
		return
	}
	if err := r.cfg.Router.SendMessage(msg); err != nil {
		r.logger.Warn("teammate leader delivery failed", "agent_name", r.cfg.Identity.AgentName, "error", err)
	}
}

// fireIdleCallbacks invokes and clears all registered idle callbacks.
func (r *TeammateRunner) fireIdleCallbacks() {
	r.idleCbsMu.Lock()
	cbs := r.idleCbs
	r.idleCbs = nil
	r.idleCbsMu.Unlock()

	for _, cb := range cbs {
		cb()
	}
}

// AddTokens records tokens consumed by the executor.
// Called by the executor to track against the budget.
func (r *TeammateRunner) AddTokens(n int64) {
	atomic.AddInt64(&r.tokensUsed, n)
}

// Identity returns this runner's teammate identity.
func (r *TeammateRunner) Identity() TeammateIdentity {
	return r.cfg.Identity
}

// --- System prompt generation ---

// TeammateSystemPromptAddendum explains the runtime team tools to workers.
const TeammateSystemPromptAddendum = `
# Comunicação da equipe

Você está trabalhando em uma equipe de agentes. Para se comunicar:
- Use team_send com to: "<nome>" para falar com outro integrante ou com team-lead.
- Use team_send com to: "*" para uma mensagem a toda a equipe.
- Use team_status para consultar os integrantes.

Sua resposta final é entregue automaticamente ao líder da equipe. Use team_send para atualizações durante a tarefa.

O usuário interage principalmente com o líder, que coordena as tarefas.
`

// BuildTeammateSystemPrompt constructs the full system prompt for a teammate.
func BuildTeammateSystemPrompt(basePrompt, customPrompt, mode string) string {
	var parts []string

	switch mode {
	case "replace":
		if customPrompt != "" {
			return customPrompt
		}
		parts = append(parts, basePrompt)
	case "append":
		parts = append(parts, basePrompt, TeammateSystemPromptAddendum)
		if customPrompt != "" {
			parts = append(parts, customPrompt)
		}
	default: // "default"
		parts = append(parts, basePrompt, TeammateSystemPromptAddendum)
	}

	result := ""
	for _, p := range parts {
		result += p + "\n"
	}
	return result
}
