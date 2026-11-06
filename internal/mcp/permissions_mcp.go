package mcp

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// ─── Server Trust Levels ────────────────────────────────────────────────────

// TrustLevel represents how much trust is placed in an MCP server.
type TrustLevel string

const (
	TrustTrusted   TrustLevel = "trusted"   // All tools auto-approved
	TrustUntrusted TrustLevel = "untrusted" // Tools require approval
	TrustBlocked   TrustLevel = "blocked"   // Server cannot be used
)

// ─── Permission Rules ───────────────────────────────────────────────────────

// PermissionRule defines access control for a server's tools.
type PermissionRule struct {
	// ServerName is the MCP server this rule applies to.
	ServerName string `json:"serverName" yaml:"serverName"`

	// TrustLevel sets the overall trust for this server.
	Trust TrustLevel `json:"trust,omitempty" yaml:"trust,omitempty"`

	// AllowedTools is the allowlist of tool names. Empty means all allowed
	// (subject to TrustLevel).
	AllowedTools []string `json:"allowedTools,omitempty" yaml:"allowedTools,omitempty"`

	// BlockedTools is the blocklist of tool names.
	BlockedTools []string `json:"blockedTools,omitempty" yaml:"blockedTools,omitempty"`
}

// ─── Permission Decision ────────────────────────────────────────────────────

// PermissionDecision is the result of a permission check.
type PermissionDecision string

const (
	PermissionAllow PermissionDecision = "allow"
	PermissionDeny  PermissionDecision = "deny"
	PermissionAsk   PermissionDecision = "ask"
)

// PermissionCheckResult contains the decision and reason.
type PermissionCheckResult struct {
	Decision PermissionDecision
	Reason   string
}

// ─── Permission Approval Callback ───────────────────────────────────────────

// PermissionApproval is a callback to request user approval for a tool call.
type PermissionApproval func(serverName, toolName string, args map[string]any) (bool, error)

// ─── MCP Tool Permission Checker ────────────────────────────────────────────

// MCPToolPermissionChecker manages permission rules for MCP tool calls.
type MCPToolPermissionChecker struct {
	mu       sync.RWMutex
	rules    map[string]*PermissionRule // keyed by server name
	logger   *slog.Logger
	approval PermissionApproval

	// Session-level overrides: tools the user has already approved/denied.
	sessionMu       sync.RWMutex
	sessionApproved map[string]bool // "server:tool" -> true/false
}

// NewMCPToolPermissionChecker creates a new permission checker.
func NewMCPToolPermissionChecker(logger *slog.Logger) *MCPToolPermissionChecker {
	return &MCPToolPermissionChecker{
		rules:           make(map[string]*PermissionRule),
		logger:          logger,
		sessionApproved: make(map[string]bool),
	}
}

// SetApprovalCallback sets the function called to ask the user for permission.
func (c *MCPToolPermissionChecker) SetApprovalCallback(fn PermissionApproval) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approval = fn
}

// AddRule adds or replaces a permission rule for a server.
func (c *MCPToolPermissionChecker) AddRule(rule *PermissionRule) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rules[rule.ServerName] = rule
}

// RemoveRule removes the permission rule for a server.
func (c *MCPToolPermissionChecker) RemoveRule(serverName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rules, serverName)
}

// CheckPermission checks whether a tool call is allowed.
func (c *MCPToolPermissionChecker) CheckPermission(serverName, toolName string) PermissionCheckResult {
	c.mu.RLock()
	rule, hasRule := c.rules[serverName]
	c.mu.RUnlock()

	key := serverName + ":" + toolName

	// Check session-level overrides first
	c.sessionMu.RLock()
	sessionDecision, hasSessionDecision := c.sessionApproved[key]
	c.sessionMu.RUnlock()

	if hasSessionDecision {
		if sessionDecision {
			return PermissionCheckResult{Decision: PermissionAllow, Reason: "approved in session"}
		}
		return PermissionCheckResult{Decision: PermissionDeny, Reason: "denied in session"}
	}

	// No rule: default to ask
	if !hasRule {
		return PermissionCheckResult{Decision: PermissionAsk, Reason: "no rule configured"}
	}

	// Blocked server
	if rule.Trust == TrustBlocked {
		return PermissionCheckResult{
			Decision: PermissionDeny,
			Reason:   fmt.Sprintf("server %q is blocked", serverName),
		}
	}

	// Check blocklist
	for _, blocked := range rule.BlockedTools {
		if matchToolName(toolName, blocked) {
			return PermissionCheckResult{
				Decision: PermissionDeny,
				Reason:   fmt.Sprintf("tool %q is blocked for server %q", toolName, serverName),
			}
		}
	}

	// Check allowlist
	if len(rule.AllowedTools) > 0 {
		allowed := false
		for _, a := range rule.AllowedTools {
			if matchToolName(toolName, a) {
				allowed = true
				break
			}
		}
		if !allowed {
			return PermissionCheckResult{
				Decision: PermissionDeny,
				Reason:   fmt.Sprintf("tool %q is not in allowlist for server %q", toolName, serverName),
			}
		}
	}

	// Trusted server: auto-allow
	if rule.Trust == TrustTrusted {
		return PermissionCheckResult{Decision: PermissionAllow, Reason: "trusted server"}
	}

	// Untrusted or default: ask
	return PermissionCheckResult{Decision: PermissionAsk, Reason: "untrusted server requires approval"}
}

// RequestApproval asks the user for permission and caches the result.
func (c *MCPToolPermissionChecker) RequestApproval(serverName, toolName string, args map[string]any) (bool, error) {
	c.mu.RLock()
	approval := c.approval
	c.mu.RUnlock()

	if approval == nil {
		return false, fmt.Errorf("no approval callback set")
	}

	approved, err := approval(serverName, toolName, args)
	if err != nil {
		return false, err
	}

	// Cache the decision for this session
	key := serverName + ":" + toolName
	c.sessionMu.Lock()
	c.sessionApproved[key] = approved
	c.sessionMu.Unlock()

	return approved, nil
}

// ClearSessionApprovals clears all session-level approval decisions.
func (c *MCPToolPermissionChecker) ClearSessionApprovals() {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	c.sessionApproved = make(map[string]bool)
}

// matchToolName checks if a tool name matches a pattern.
// Supports exact match and wildcard (*) suffix.
func matchToolName(toolName, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(toolName, prefix)
	}
	return toolName == pattern
}

// ─── Channel-based Permission Approval ──────────────────────────────────────

// ChannelPermissionApproval implements permission approval via a channel
// (for use with terminal UI or other async systems).
type ChannelPermissionApproval struct {
	mu      sync.Mutex
	pending map[string]chan bool
	logger  *slog.Logger
}

// NewChannelPermissionApproval creates a channel-based approval system.
func NewChannelPermissionApproval(logger *slog.Logger) *ChannelPermissionApproval {
	return &ChannelPermissionApproval{
		pending: make(map[string]chan bool),
		logger:  logger,
	}
}

// Request sends an approval request and waits for a response via the channel.
func (a *ChannelPermissionApproval) Request(serverName, toolName string, args map[string]any) (bool, error) {
	key := fmt.Sprintf("%s:%s", serverName, toolName)

	ch := make(chan bool, 1)

	a.mu.Lock()
	a.pending[key] = ch
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.pending, key)
		a.mu.Unlock()
	}()

	if a.logger != nil {
		a.logger.Info("awaiting permission approval",
			"server", serverName,
			"tool", toolName,
		)
	}

	// Wait for response (with timeout)
	select {
	case approved := <-ch:
		return approved, nil
	}
}

// Resolve resolves a pending approval request.
func (a *ChannelPermissionApproval) Resolve(serverName, toolName string, approved bool) bool {
	key := fmt.Sprintf("%s:%s", serverName, toolName)

	a.mu.Lock()
	ch, ok := a.pending[key]
	a.mu.Unlock()

	if !ok {
		return false
	}

	select {
	case ch <- approved:
		return true
	default:
		return false
	}
}

// PendingRequests returns a list of pending approval requests.
func (a *ChannelPermissionApproval) PendingRequests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	keys := make([]string, 0, len(a.pending))
	for k := range a.pending {
		keys = append(keys, k)
	}
	return keys
}
