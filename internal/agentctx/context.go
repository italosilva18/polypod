// Package agentctx provides agent identity context propagation across goroutines.
//
// Equivalent of Claude Code's agentContext.ts + standaloneAgent.ts.
// Uses context.Context value keys instead of AsyncLocalStorage.
// When multiple agents run concurrently (background tasks), each goroutine
// carries its own AgentContext in its context.Context, preventing
// interference between concurrent agents.
package agentctx

import (
	"context"
	"sync"

	"github.com/costa/polypod/internal/types"
)

// AgentType discriminates between different agent execution contexts.
type AgentType string

const (
	AgentTypeSubagent   AgentType = "subagent"
	AgentTypeTeammate   AgentType = "teammate"
	AgentTypeStandalone AgentType = "standalone"
)

// InvocationKind indicates whether the agent was freshly spawned or resumed.
type InvocationKind string

const (
	InvocationSpawn  InvocationKind = "spawn"
	InvocationResume InvocationKind = "resume"
)

// AgentContext carries identity and tracking state for an agent execution.
type AgentContext struct {
	// AgentID is the unique identifier for this agent.
	AgentID types.AgentID

	// SessionID is the session this agent belongs to.
	SessionID types.SessionID

	// ParentSessionID links to the parent session for transcript correlation.
	// For subagents of the main REPL, this may be empty.
	ParentSessionID types.SessionID

	// AgentType discriminates between subagent, teammate, standalone.
	AgentType AgentType

	// SubagentName is the type name for subagents (e.g. "Explore", "code-reviewer").
	SubagentName string

	// TeamName is the team this teammate belongs to (only for AgentTypeTeammate).
	TeamName string

	// AgentColor is the UI color assigned to a teammate.
	AgentColor string

	// IsBuiltIn is true for built-in agents (vs user-defined custom agents).
	IsBuiltIn bool

	// IsTeamLead is true if this teammate is the team lead.
	IsTeamLead bool

	// PlanModeRequired indicates if the teammate must plan before implementing.
	PlanModeRequired bool

	// Depth tracks nesting level for subagent call chains.
	Depth int

	// TokenBudget is the maximum token allocation for this agent.
	TokenBudget int

	// InvokingRequestID is the request_id that spawned or resumed this agent.
	InvokingRequestID string

	// InvocationKind indicates spawn vs resume.
	InvocationKind InvocationKind

	// mu protects mutable fields.
	mu sync.Mutex

	// invocationEmitted tracks if this invocation edge was emitted to telemetry.
	invocationEmitted bool
}

// contextKey is an unexported type for context value keys.
type contextKey struct{}

// agentContextKey is the key used to store AgentContext in context.Context.
var agentContextKey = contextKey{}

// GetAgentContext extracts the AgentContext from a context.Context.
// Returns nil if no agent context is set (running on the main thread).
func GetAgentContext(ctx context.Context) *AgentContext {
	v := ctx.Value(agentContextKey)
	if v == nil {
		return nil
	}
	ac, ok := v.(*AgentContext)
	if !ok {
		return nil
	}
	return ac
}

// SetAgentContext returns a new context with the given AgentContext attached.
// All goroutines spawned from this context will inherit the agent identity.
func SetAgentContext(ctx context.Context, ac *AgentContext) context.Context {
	return context.WithValue(ctx, agentContextKey, ac)
}

// IsSubagentContext returns true if the agent context represents a subagent.
func IsSubagentContext(ac *AgentContext) bool {
	return ac != nil && ac.AgentType == AgentTypeSubagent
}

// IsTeammateContext returns true if the agent context represents a teammate.
func IsTeammateContext(ac *AgentContext) bool {
	return ac != nil && ac.AgentType == AgentTypeTeammate
}

// IsStandaloneContext returns true if the agent context is a standalone agent.
func IsStandaloneContext(ac *AgentContext) bool {
	return ac != nil && ac.AgentType == AgentTypeStandalone
}

// GetSubagentLogName returns the subagent name suitable for analytics logging.
// Returns the agent type name for built-in agents, "user-defined" for custom agents,
// or empty string if not running within a subagent context.
func GetSubagentLogName(ac *AgentContext) string {
	if !IsSubagentContext(ac) || ac.SubagentName == "" {
		return ""
	}
	if ac.IsBuiltIn {
		return ac.SubagentName
	}
	return "user-defined"
}

// ConsumeInvokingRequestID returns the invoking request_id once per invocation.
// Returns empty strings on second call until the next spawn/resume.
// This implements sparse edge semantics: the value appears exactly once per
// invocation on the first terminal API event.
func ConsumeInvokingRequestID(ac *AgentContext) (requestID string, kind InvocationKind, ok bool) {
	if ac == nil || ac.InvokingRequestID == "" {
		return "", "", false
	}

	ac.mu.Lock()
	defer ac.mu.Unlock()

	if ac.invocationEmitted {
		return "", "", false
	}
	ac.invocationEmitted = true
	return ac.InvokingRequestID, ac.InvocationKind, true
}

// ResetInvocationEmitted resets the invocation emission flag.
// Called on each new spawn or resume boundary.
func ResetInvocationEmitted(ac *AgentContext) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.invocationEmitted = false
}

// ContextStack manages nested agent contexts for sub-agent call chains.
type ContextStack struct {
	mu    sync.Mutex
	stack []*AgentContext
}

// NewContextStack creates an empty context stack.
func NewContextStack() *ContextStack {
	return &ContextStack{
		stack: make([]*AgentContext, 0, 4),
	}
}

// Push adds an agent context to the top of the stack.
func (cs *ContextStack) Push(ac *AgentContext) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.stack = append(cs.stack, ac)
}

// Pop removes and returns the top agent context from the stack.
// Returns nil if the stack is empty.
func (cs *ContextStack) Pop() *AgentContext {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.stack) == 0 {
		return nil
	}
	top := cs.stack[len(cs.stack)-1]
	cs.stack = cs.stack[:len(cs.stack)-1]
	return top
}

// Peek returns the top agent context without removing it.
// Returns nil if the stack is empty.
func (cs *ContextStack) Peek() *AgentContext {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.stack) == 0 {
		return nil
	}
	return cs.stack[len(cs.stack)-1]
}

// Depth returns the current depth of the context stack.
func (cs *ContextStack) Depth() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.stack)
}

// NewSubagentContext creates a new AgentContext for a subagent with the given name.
func NewSubagentContext(sessionID types.SessionID, name string, isBuiltIn bool) *AgentContext {
	return &AgentContext{
		AgentID:      types.NewAgentID(name),
		SessionID:    sessionID,
		AgentType:    AgentTypeSubagent,
		SubagentName: name,
		IsBuiltIn:    isBuiltIn,
	}
}

// NewTeammateContext creates a new AgentContext for a swarm teammate.
func NewTeammateContext(
	agentID types.AgentID,
	sessionID types.SessionID,
	parentSessionID types.SessionID,
	teamName string,
	isTeamLead bool,
	planModeRequired bool,
) *AgentContext {
	return &AgentContext{
		AgentID:          agentID,
		SessionID:        sessionID,
		ParentSessionID:  parentSessionID,
		AgentType:        AgentTypeTeammate,
		TeamName:         teamName,
		IsTeamLead:       isTeamLead,
		PlanModeRequired: planModeRequired,
	}
}

// GetStandaloneAgentName returns the standalone agent name if set and not a swarm teammate.
// Returns empty string if the agent is part of a swarm or has no standalone context.
func GetStandaloneAgentName(ac *AgentContext) string {
	if ac == nil {
		return ""
	}
	// If in a team (swarm), don't return standalone name
	if ac.AgentType == AgentTypeTeammate {
		return ""
	}
	if ac.AgentType == AgentTypeStandalone {
		return ac.SubagentName
	}
	return ""
}
