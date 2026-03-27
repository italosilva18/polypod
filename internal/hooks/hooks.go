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
)

// Event types for the lifecycle hook system.
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventSessionEnd       Event = "SessionEnd"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventPreCompact       Event = "PreCompact"
	EventPostCompact      Event = "PostCompact"
	EventUserPrompt       Event = "UserPrompt"
	EventAssistantResponse Event = "AssistantResponse"
	EventError            Event = "Error"
	EventStop             Event = "Stop"
)

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
	HandlerShell   HandlerType = "shell"   // Run a shell command
	HandlerHTTP    HandlerType = "http"    // POST to a URL
)

// Hook defines a single lifecycle hook.
type Hook struct {
	Name    string      `yaml:"name" json:"name"`
	Event   Event       `yaml:"event" json:"event"`
	Type    HandlerType `yaml:"type" json:"type"`
	Command string      `yaml:"command" json:"command"` // for shell
	URL     string      `yaml:"url" json:"url"`         // for http
	Timeout int         `yaml:"timeout" json:"timeout"` // seconds, default 10
	Matcher string      `yaml:"matcher" json:"matcher"` // regex to match tool name (PreToolUse only)
	Enabled bool        `yaml:"enabled" json:"enabled"`
}

// HookPayload is the data passed to hook handlers.
type HookPayload struct {
	Event     Event          `json:"event"`
	Timestamp string         `json:"timestamp"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolArgs  map[string]any `json:"tool_args,omitempty"`
	ToolResult string        `json:"tool_result,omitempty"`
	Message   string         `json:"message,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Channel   string         `json:"channel,omitempty"`
	UserID    string         `json:"user_id,omitempty"`
}

// HookResult is the response from a hook handler.
type HookResult struct {
	Decision Decision `json:"decision,omitempty"` // for PreToolUse
	Message  string   `json:"message,omitempty"`
	Modified map[string]any `json:"modified,omitempty"` // modified tool args
}

// Manager manages registered hooks.
type Manager struct {
	mu     sync.RWMutex
	hooks  []Hook
	logger *slog.Logger
}

// NewManager creates a hook manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Register adds a hook.
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

// Fire triggers all hooks for the given event. For PreToolUse, returns the first
// non-allow decision. For other events, runs all hooks and returns.
func (m *Manager) Fire(ctx context.Context, payload HookPayload) (*HookResult, error) {
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

	return &HookResult{Decision: DecisionAllow}, nil
}

func (m *Manager) executeHook(ctx context.Context, h Hook, payload HookPayload) (*HookResult, error) {
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
		return nil, fmt.Errorf("tipo de hook desconhecido: %s", h.Type)
	}
}

func (m *Manager) executeShell(ctx context.Context, command string, payloadJSON []byte) (*HookResult, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Stdin = bytes.NewReader(payloadJSON)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("shell hook: %w (output: %s)", err, string(out))
	}

	output := strings.TrimSpace(string(out))
	if output == "" {
		return &HookResult{Decision: DecisionAllow}, nil
	}

	// Try to parse as JSON result
	var result HookResult
	if err := json.Unmarshal([]byte(output), &result); err == nil {
		return &result, nil
	}

	// Plain text: treat as message, allow
	return &HookResult{Decision: DecisionAllow, Message: output}, nil
}

func (m *Manager) executeHTTP(ctx context.Context, url string, payloadJSON []byte) (*HookResult, error) {
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

	var result HookResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &HookResult{Decision: DecisionAllow}, nil
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

// List returns all registered hooks.
func (m *Manager) List() []Hook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Hook, len(m.hooks))
	copy(result, m.hooks)
	return result
}

// Remove removes a hook by name.
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

// SetEnabled enables/disables a hook by name.
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
