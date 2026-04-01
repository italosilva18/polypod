package permissions

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/types"
)

// Rule defines a permission rule for a tool.
type Rule struct {
	Pattern  string `json:"pattern" yaml:"pattern"`   // glob: "git_*", "run_command", "*"
	Decision string `json:"decision" yaml:"decision"` // "allow", "deny", "ask"
	Source   string `json:"source,omitempty" yaml:"source,omitempty"`
}

// Settings holds permission configuration with hierarchy.
type Settings struct {
	AllowedTools []Rule `json:"allowed_tools" yaml:"allowed_tools"`
	DeniedTools  []Rule `json:"denied_tools" yaml:"denied_tools"`
	AskTools     []Rule `json:"ask_tools" yaml:"ask_tools"`
	Mode         string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// Manager evaluates permissions for tool calls.
type Manager struct {
	mu          sync.RWMutex
	rules       []Rule // ordered: deny first, then ask, then allow
	logger      *slog.Logger
	mode        types.PermissionMode
	modeBlocked []string // tools blocked by current mode
}

// NewManager creates a permission manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		logger: logger,
		mode:   types.ModeDefault,
	}
}

// Mode returns the current permission mode.
func (m *Manager) Mode() types.PermissionMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

// SetMode changes the permission evaluation mode.
func (m *Manager) SetMode(mode types.PermissionMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
	m.logger.Info("permission mode changed", "mode", string(mode))
}

// LoadFromFiles loads settings from the hierarchy:
// 1. Project: .polypod/settings.json
// 2. User: ~/.polypod/settings.json
// Lower-level (project) overrides higher-level (user).
func (m *Manager) LoadFromFiles() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = nil

	// User-level settings
	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".polypod", "settings.json")
		m.loadFile(userPath, "userSettings")
	}

	// Project-level settings (overrides user)
	projectPath := filepath.Join(".polypod", "settings.json")
	m.loadFile(projectPath, "projectSettings")

	m.logger.Debug("permissions loaded", "rules", len(m.rules), "mode", string(m.mode))
	return nil
}

func (m *Manager) loadFile(path string, source string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		m.logger.Warn("failed to parse settings", "path", path, "error", err)
		return
	}

	// Apply mode from settings if present
	if settings.Mode != "" {
		m.mode = types.PermissionMode(settings.Mode)
	}

	// Add rules in priority order: deny > ask > allow
	for _, r := range settings.DeniedTools {
		r.Decision = "deny"
		if r.Source == "" {
			r.Source = source
		}
		m.rules = append(m.rules, r)
	}
	for _, r := range settings.AskTools {
		r.Decision = "ask"
		if r.Source == "" {
			r.Source = source
		}
		m.rules = append(m.rules, r)
	}
	for _, r := range settings.AllowedTools {
		r.Decision = "allow"
		if r.Source == "" {
			r.Source = source
		}
		m.rules = append(m.rules, r)
	}
}

// AddRule adds a permission rule programmatically.
func (m *Manager) AddRule(rule Rule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append(m.rules, rule)
}

// SetModeBlocked sets tools blocked by the current mode.
func (m *Manager) SetModeBlocked(tools []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modeBlocked = tools
}

// Check evaluates whether a tool call is allowed.
// Returns "allow", "deny", or "ask".
// Respects the current permission mode.
func (m *Manager) Check(toolName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkLocked(toolName)
}

// checkLocked evaluates permissions assuming the caller holds at least an RLock.
func (m *Manager) checkLocked(toolName string) string {
	// Bypass mode: allow everything
	if m.mode == types.ModeBypass {
		return "allow"
	}

	// Mode blocks take precedence
	for _, blocked := range m.modeBlocked {
		if matchGlob(blocked, toolName) {
			return "deny"
		}
	}

	// Evaluate rules in order (first match wins)
	for _, rule := range m.rules {
		if matchGlob(rule.Pattern, toolName) {
			return rule.Decision
		}
	}

	// Mode-based defaults
	switch m.mode {
	case types.ModeDontAsk:
		return "allow"
	case types.ModePlan:
		// In plan mode, destructive tools are simulated (allow but don't execute)
		return "allow"
	default:
		return "allow"
	}
}

// CheckWithReason returns decision + reason for display.
func (m *Manager) CheckWithReason(toolName string) (string, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.mode == types.ModeBypass {
		return "allow", "modo bypass ativo — todas as permissoes concedidas"
	}

	for _, blocked := range m.modeBlocked {
		if matchGlob(blocked, toolName) {
			return "deny", "bloqueado pelo modo atual"
		}
	}

	for _, rule := range m.rules {
		if matchGlob(rule.Pattern, toolName) {
			sourceLabel := rule.Source
			if sourceLabel == "" {
				sourceLabel = "config"
			}
			return rule.Decision, fmt.Sprintf("regra [%s]: %s → %s", sourceLabel, rule.Pattern, rule.Decision)
		}
	}

	return "allow", "permitido por padrao"
}

// CheckDetailed returns a full PermissionDecision with typed reason.
func (m *Manager) CheckDetailed(toolName string) types.PermissionDecision {
	m.mu.RLock()
	defer m.mu.RUnlock()

	behavior, reason := m.checkDetailedLocked(toolName)
	return types.PermissionDecision{
		Behavior: types.PermissionBehavior(behavior),
		Message:  reason,
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "default",
			Detail: reason,
		},
	}
}

func (m *Manager) checkDetailedLocked(toolName string) (string, string) {
	if m.mode == types.ModeBypass {
		return "allow", "modo bypass ativo"
	}

	for _, blocked := range m.modeBlocked {
		if matchGlob(blocked, toolName) {
			return "deny", fmt.Sprintf("ferramenta %s bloqueada pelo modo", toolName)
		}
	}

	for _, rule := range m.rules {
		if matchGlob(rule.Pattern, toolName) {
			return rule.Decision, fmt.Sprintf("regra %s: %s → %s", rule.Source, rule.Pattern, rule.Decision)
		}
	}

	return "allow", "permitido por padrao"
}

// SaveProjectSettings saves settings to .polypod/settings.json.
func SaveProjectSettings(settings Settings) error {
	dir := ".polypod"
	os.MkdirAll(dir, 0755)
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0644)
}

func matchGlob(pattern, name string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(name, strings.TrimPrefix(pattern, "*"))
	}
	return pattern == name
}
