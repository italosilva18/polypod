package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// --- Legacy Manager ---
// The original hook manager for simple shell/HTTP hooks registered from config.
// Maintained for backward compatibility. The new registry system (registry.go)
// provides the full Claude Code hooks model with async, session-scoped,
// function, frontmatter, and skill hooks.

// Decision returned by PreToolUse hooks.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	DecisionAsk   Decision = "ask"
)

// HandlerType defines how a hook is executed.
type HandlerType string

const (
	HandlerShell HandlerType = "shell" // Run a shell command
	HandlerHTTP  HandlerType = "http"  // POST to a URL
)

// Hook defines a single lifecycle hook (legacy config format).
type Hook struct {
	Name    string      `yaml:"name" json:"name"`
	Event   HookEvent   `yaml:"event" json:"event"`
	Type    HandlerType `yaml:"type" json:"type"`
	Command string      `yaml:"command" json:"command"` // for shell
	URL     string      `yaml:"url" json:"url"`         // for http
	Timeout int         `yaml:"timeout" json:"timeout"` // seconds, default 10
	Matcher string      `yaml:"matcher" json:"matcher"` // regex to match tool name (PreToolUse only)
	Enabled bool        `yaml:"enabled" json:"enabled"`
}

// HookPayload is a type alias for backward compatibility with code that references hooks.HookPayload.
type HookPayload = LegacyPayload

// Event is a type alias for HookEvent for backward compatibility.
type Event = HookEvent

// LegacyPayload is the data passed to legacy hook handlers.
type LegacyPayload struct {
	Event      HookEvent      `json:"event"`
	Timestamp  string         `json:"timestamp"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	Message    string         `json:"message,omitempty"`
	SessionID  string         `json:"session_id,omitempty"`
	Channel    string         `json:"channel,omitempty"`
	UserID     string         `json:"user_id,omitempty"`
}

// LegacyResult is the response from a legacy hook handler.
type LegacyResult struct {
	Decision Decision       `json:"decision,omitempty"` // for PreToolUse
	Message  string         `json:"message,omitempty"`
	Modified map[string]any `json:"modified,omitempty"` // modified tool args
}

// Manager manages registered hooks (legacy and new).
// It wraps the session hook registry and provides backward-compatible methods
// while also supporting the full Claude Code hooks model.
type Manager struct {
	mu     sync.RWMutex
	hooks  []Hook
	logger *slog.Logger

	// New subsystems
	SessionRegistry     *SessionHookRegistry
	AsyncRegistry       *AsyncHookRegistry
	EventEmitter        *HookEventEmitter
	ConfigSnapshot      *HookConfigSnapshot
	PostSamplingHooks   *PostSamplingRegistry
	StopHooks           *StopHookRegistry
	ToolUseSummaryHooks *ToolUseSummaryRegistry
	MessageTransforms   *MessageTransformRegistry
	ResponseFilters     *ResponseFilterRegistry
}

// NewManager creates a hook manager with all subsystems initialized.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		logger:              logger,
		SessionRegistry:     NewSessionHookRegistry(logger),
		AsyncRegistry:       NewAsyncHookRegistry(logger),
		EventEmitter:        NewHookEventEmitter(logger),
		ConfigSnapshot:      NewHookConfigSnapshot(logger),
		PostSamplingHooks:   NewPostSamplingRegistry(logger),
		StopHooks:           NewStopHookRegistry(logger),
		ToolUseSummaryHooks: NewToolUseSummaryRegistry(logger),
		MessageTransforms:   NewMessageTransformRegistry(logger),
		ResponseFilters:     NewResponseFilterRegistry(logger),
	}
}

// Register adds a legacy hook.
func (m *Manager) Register(h Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.Timeout == 0 {
		h.Timeout = 10
	}
	if !h.Enabled {
		h.Enabled = true
	}
	m.hooks = append(m.hooks, h)
	m.logger.Debug("hook registered", "name", h.Name, "event", h.Event)
}

// LoadHooks loads hooks from a slice (e.g., from config).
func (m *Manager) LoadHooks(hooks []Hook) {
	for _, h := range hooks {
		m.Register(h)
	}
}

// LoadHooksSettings loads hooks from Claude Code HooksSettings format into the
// config snapshot. These hooks are used by the new system (registry.go).
func (m *Manager) LoadHooksSettings(settings HooksSettings) {
	m.ConfigSnapshot.CaptureSimple(settings)
}

// Fire triggers all legacy hooks for the given event. For PreToolUse, returns
// the first non-allow decision. For other events, runs all hooks and returns.
func (m *Manager) Fire(ctx context.Context, payload LegacyPayload) (*LegacyResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, h := range m.hooks {
		if !h.Enabled || h.Event != payload.Event {
			continue
		}

		// Matcher check for PreToolUse
		if h.Matcher != "" && payload.ToolName != "" {
			if !matchTool(h.Matcher, payload.ToolName) {
				continue
			}
		}

		result, err := m.executeHook(ctx, h, payload)
		if err != nil {
			m.logger.Warn("hook error", "name", h.Name, "error", err)
			continue
		}

		// For PreToolUse, first deny/ask wins
		if payload.Event == EventPreToolUse && result != nil {
			if result.Decision == DecisionDeny || result.Decision == DecisionAsk {
				return result, nil
			}
		}
	}

	return &LegacyResult{Decision: DecisionAllow}, nil
}

// FireWithProgress triggers hooks with progress reporting via callback.
func (m *Manager) FireWithProgress(ctx context.Context, payload LegacyPayload, onProgress func(types.ToolProgress)) (*LegacyResult, error) {
	m.mu.RLock()
	hooks := make([]Hook, len(m.hooks))
	copy(hooks, m.hooks)
	m.mu.RUnlock()

	total := len(hooks)
	done := 0

	for _, h := range hooks {
		if !h.Enabled || h.Event != payload.Event {
			continue
		}

		if onProgress != nil {
			onProgress(types.ToolProgress{
				Message: fmt.Sprintf("executing hook: %s", h.Name),
				Percent: done * 100 / total,
			})
		}

		if h.Matcher != "" && payload.ToolName != "" {
			if !matchTool(h.Matcher, payload.ToolName) {
				done++
				continue
			}
		}

		result, err := m.executeHook(ctx, h, payload)
		done++

		if err != nil {
			m.logger.Warn("hook error", "name", h.Name, "error", err)
			continue
		}

		if payload.Event == EventPreToolUse && result != nil {
			if result.Decision == DecisionDeny || result.Decision == DecisionAsk {
				return result, nil
			}
		}
	}

	if onProgress != nil {
		onProgress(types.ToolProgress{
			Message: "hooks completed",
			Percent: 100,
		})
	}

	return &LegacyResult{Decision: DecisionAllow}, nil
}

// FireNew triggers hooks using the new Claude Code hooks system.
// It runs both legacy hooks and session hooks, aggregating results.
func (m *Manager) FireNew(
	ctx context.Context,
	event HookEvent,
	matcher string,
	jsonInput string,
	sessionID string,
	shellInput ShellHookInput,
	toolUseID string,
	promptExec PromptHookExecutor,
	agentExec AgentHookExecutor,
	allowlist *DomainAllowlist,
	env map[string]string,
	transcriptPath string,
) *AggregatedHookResult {
	var results []HookResult

	// 1. Run hooks from config snapshot
	config := m.ConfigSnapshot.Get()
	if config != nil {
		if matchers, ok := config[event]; ok {
			for _, hm := range matchers {
				if matcher != "" && hm.Matcher != "" && hm.Matcher != matcher {
					continue
				}
				for _, hook := range hm.Hooks {
					hookName := hook.Command
					if hook.Type == HookTypePrompt || hook.Type == HookTypeAgent {
						hookName = hook.Prompt
					} else if hook.Type == HookTypeHTTP {
						hookName = hook.URL
					}
					result := DispatchHook(ctx, hook, hookName, event, jsonInput,
						shellInput, toolUseID, promptExec, agentExec,
						allowlist, env, transcriptPath, m.logger)
					results = append(results, *result)
				}
			}
		}
	}

	// 2. Run session hooks
	sessionMatchers := m.SessionRegistry.GetSessionHooks(sessionID, event)
	for _, sm := range sessionMatchers {
		if matcher != "" && sm.Matcher != "" && sm.Matcher != matcher {
			continue
		}
		for _, hook := range sm.Hooks {
			hookName := hook.Command
			if hook.Type == HookTypePrompt || hook.Type == HookTypeAgent {
				hookName = hook.Prompt
			} else if hook.Type == HookTypeHTTP {
				hookName = hook.URL
			}
			result := DispatchHook(ctx, hook, hookName, event, jsonInput,
				shellInput, toolUseID, promptExec, agentExec,
				allowlist, env, transcriptPath, m.logger)
			results = append(results, *result)
		}
	}

	return Aggregate(results)
}

// CheckToolPermission integrates with the permission system by running
// PreToolUse hooks and translating their decisions to PermissionBehavior.
func (m *Manager) CheckToolPermission(ctx context.Context, toolName string, toolArgs map[string]any) types.PermissionDecision {
	result, err := m.Fire(ctx, LegacyPayload{
		Event:     EventPreToolUse,
		Timestamp: time.Now().Format(time.RFC3339),
		ToolName:  toolName,
		ToolArgs:  toolArgs,
	})
	if err != nil {
		m.logger.Warn("hook permission check failed", "error", err)
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "hook errored, allowed by default",
		}
	}

	switch result.Decision {
	case DecisionDeny:
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  result.Message,
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "hook",
				Detail: fmt.Sprintf("hook denied: %s", result.Message),
			},
		}
	case DecisionAsk:
		return types.PermissionDecision{
			Behavior: types.PermissionAsk,
			Message:  result.Message,
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "hook",
				Detail: fmt.Sprintf("hook requested confirmation: %s", result.Message),
			},
		}
	default:
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  result.Message,
		}
	}
}

func (m *Manager) executeHook(ctx context.Context, h Hook, payload LegacyPayload) (*LegacyResult, error) {
	timeout := time.Duration(h.Timeout) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payloadJSON, _ := json.Marshal(payload)

	switch h.Type {
	case HandlerShell:
		return m.executeShell(execCtx, h.Command, payloadJSON)
	case HandlerHTTP:
		return m.executeHTTP(execCtx, h.URL, payloadJSON)
	default:
		return nil, fmt.Errorf("unknown hook type: %s", h.Type)
	}
}

func (m *Manager) executeShell(ctx context.Context, command string, payloadJSON []byte) (*LegacyResult, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Stdin = bytes.NewReader(payloadJSON)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("shell hook: %w (output: %s)", err, string(out))
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return &LegacyResult{Decision: DecisionAllow}, nil
	}

	// Try to parse as JSON result
	var result LegacyResult
	if err := json.Unmarshal([]byte(output), &result); err == nil {
		return &result, nil
	}

	// Plain text: treat as message, allow
	return &LegacyResult{Decision: DecisionAllow, Message: output}, nil
}

func (m *Manager) executeHTTP(ctx context.Context, url string, payloadJSON []byte) (*LegacyResult, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("http hook: %w", err)
	}
	defer resp.Body.Close()

	var result LegacyResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &LegacyResult{Decision: DecisionAllow}, nil
	}
	return &result, nil
}

func matchTool(pattern, toolName string) bool {
	if pattern == "*" {
		return true
	}
	// Simple glob: "git_*" matches "git_commit", "git_push", etc.
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(toolName, prefix)
	}
	return pattern == toolName
}

// List returns all registered legacy hooks.
func (m *Manager) List() []Hook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Hook, len(m.hooks))
	copy(result, m.hooks)
	return result
}

// Remove removes a legacy hook by name.
func (m *Manager) Remove(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, h := range m.hooks {
		if h.Name == name {
			m.hooks = append(m.hooks[:i], m.hooks[i+1:]...)
			return true
		}
	}
	return false
}

// SetEnabled enables/disables a legacy hook by name.
func (m *Manager) SetEnabled(name string, enabled bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, h := range m.hooks {
		if h.Name == name {
			m.hooks[i].Enabled = enabled
			return true
		}
	}
	return false
}

// ClearAll clears all hooks across all subsystems.
func (m *Manager) ClearAll() {
	m.mu.Lock()
	m.hooks = nil
	m.mu.Unlock()

	m.AsyncRegistry.Clear()
	m.ConfigSnapshot.Reset()
	m.PostSamplingHooks.Clear()
	m.StopHooks.Clear()
	m.ToolUseSummaryHooks.Clear()
	m.MessageTransforms.Clear()
	m.ResponseFilters.Clear()
	m.EventEmitter.Clear()
}
