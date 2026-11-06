package permissions

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// SettingsPermissions represents the permissions section of a settings JSON file.
type SettingsPermissions struct {
	Allow                 []string `json:"allow,omitempty"`
	Deny                  []string `json:"deny,omitempty"`
	Ask                   []string `json:"ask,omitempty"`
	DefaultMode           string   `json:"defaultMode,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

// SettingsJSON represents a partial settings file structure.
type SettingsJSON struct {
	Permissions                     *SettingsPermissions `json:"permissions,omitempty"`
	AllowManagedPermissionRulesOnly bool                 `json:"allowManagedPermissionRulesOnly,omitempty"`
}

// PermissionLoader loads and merges permissions from multiple sources.
type PermissionLoader struct {
	mu               sync.RWMutex
	logger           *slog.Logger
	projectDir       string
	userHomeDir      string
	ruleSet          *PermissionRuleSet
	mode             types.PermissionMode
	allowManagedOnly bool
	watchers         []chan struct{}
	lastLoadedPaths  map[string]time.Time // path -> last modified time
}

// NewPermissionLoader creates a new permission loader.
func NewPermissionLoader(logger *slog.Logger, projectDir string) *PermissionLoader {
	homeDir, _ := os.UserHomeDir()
	return &PermissionLoader{
		logger:          logger,
		projectDir:      projectDir,
		userHomeDir:     homeDir,
		ruleSet:         NewPermissionRuleSet(),
		mode:            types.ModeDefault,
		lastLoadedPaths: make(map[string]time.Time),
	}
}

// LoadPermissions loads all permission rules from all sources.
// Source hierarchy (highest to lowest priority):
// 1. Policy settings (enterprise managed)
// 2. Project settings (.polypod/settings.json)
// 3. Local settings (.polypod/settings.local.json)
// 4. User settings (~/.polypod/settings.json)
func (pl *PermissionLoader) LoadPermissions() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	pl.ruleSet = NewPermissionRuleSet()

	// Check for managed-only mode first
	policyPath := pl.settingsPath(types.SourcePolicySettings)
	if policyPath != "" {
		policySettings := pl.readSettingsFile(policyPath)
		if policySettings != nil {
			if policySettings.AllowManagedPermissionRulesOnly {
				pl.allowManagedOnly = true
				pl.logger.Debug("policy enforces managed permission rules only")
			}
			pl.loadRulesFromSettings(policySettings, types.SourcePolicySettings)
		}
	}

	if pl.allowManagedOnly {
		pl.logger.Debug("managed-only mode: skipping non-policy settings")
		return nil
	}

	// Load in priority order (lower sources first, higher sources override)
	sources := []struct {
		source types.PermissionRuleSource
		path   string
	}{
		{types.SourceUserSettings, pl.settingsPath(types.SourceUserSettings)},
		{types.SourceLocalSettings, pl.settingsPath(types.SourceLocalSettings)},
		{types.SourceProjectSettings, pl.settingsPath(types.SourceProjectSettings)},
	}

	for _, s := range sources {
		if s.path == "" {
			continue
		}
		settings := pl.readSettingsFile(s.path)
		if settings == nil {
			continue
		}
		pl.loadRulesFromSettings(settings, s.source)

		// Apply mode from settings if present
		if settings.Permissions != nil && settings.Permissions.DefaultMode != "" {
			pl.mode = types.PermissionMode(settings.Permissions.DefaultMode)
		}
	}

	pl.logger.Debug("permissions loaded",
		"allow_rules", len(pl.ruleSet.GetAllRules("allow")),
		"deny_rules", len(pl.ruleSet.GetAllRules("deny")),
		"ask_rules", len(pl.ruleSet.GetAllRules("ask")),
		"mode", string(pl.mode),
	)

	return nil
}

// settingsPath returns the settings file path for a given source.
func (pl *PermissionLoader) settingsPath(source types.PermissionRuleSource) string {
	switch source {
	case types.SourceUserSettings:
		if pl.userHomeDir == "" {
			return ""
		}
		return filepath.Join(pl.userHomeDir, ".polypod", "settings.json")
	case types.SourceProjectSettings:
		if pl.projectDir == "" {
			return ""
		}
		return filepath.Join(pl.projectDir, ".polypod", "settings.json")
	case types.SourceLocalSettings:
		if pl.projectDir == "" {
			return ""
		}
		return filepath.Join(pl.projectDir, ".polypod", "settings.local.json")
	case types.SourcePolicySettings:
		if pl.userHomeDir == "" {
			return ""
		}
		return filepath.Join(pl.userHomeDir, ".polypod", "policy.json")
	case types.SourceFlagSettings:
		// Flag settings come from CLI args, not files
		return ""
	default:
		return ""
	}
}

// readSettingsFile reads and parses a settings JSON file.
func (pl *PermissionLoader) readSettingsFile(path string) *SettingsJSON {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var settings SettingsJSON
	if err := json.Unmarshal(data, &settings); err != nil {
		pl.logger.Warn("failed to parse settings file", "path", path, "error", err)
		return nil
	}

	// Track the file modification time
	if info, err := os.Stat(path); err == nil {
		pl.lastLoadedPaths[path] = info.ModTime()
	}

	return &settings
}

// loadRulesFromSettings extracts permission rules from a settings struct.
func (pl *PermissionLoader) loadRulesFromSettings(settings *SettingsJSON, source types.PermissionRuleSource) {
	if settings.Permissions == nil {
		return
	}
	perms := settings.Permissions

	for _, ruleStr := range perms.Allow {
		parsed := ParsePermissionRuleValue(ruleStr)
		parsed.Source = source
		parsed.Behavior = "allow"
		pl.ruleSet.AddRule(parsed)
	}
	for _, ruleStr := range perms.Deny {
		parsed := ParsePermissionRuleValue(ruleStr)
		parsed.Source = source
		parsed.Behavior = "deny"
		pl.ruleSet.AddRule(parsed)
	}
	for _, ruleStr := range perms.Ask {
		parsed := ParsePermissionRuleValue(ruleStr)
		parsed.Source = source
		parsed.Behavior = "ask"
		pl.ruleSet.AddRule(parsed)
	}
}

// MergePermissions merges additional rules into the current rule set.
func (pl *PermissionLoader) MergePermissions(rules []types.PermissionRule) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	for _, rule := range rules {
		pl.ruleSet.AddRule(rule)
	}
}

// ReplaceRulesForSource replaces all rules for a specific source and behavior.
func (pl *PermissionLoader) ReplaceRulesForSource(source types.PermissionRuleSource, behavior string, rules []types.PermissionRule) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	switch behavior {
	case "allow":
		pl.ruleSet.AllowRules[source] = rules
	case "deny":
		pl.ruleSet.DenyRules[source] = rules
	case "ask":
		pl.ruleSet.AskRules[source] = rules
	}
}

// GetRuleSet returns the current rule set (read-only).
func (pl *PermissionLoader) GetRuleSet() *PermissionRuleSet {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.ruleSet
}

// GetMode returns the current permission mode.
func (pl *PermissionLoader) GetMode() types.PermissionMode {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.mode
}

// SetMode sets the permission mode.
func (pl *PermissionLoader) SetMode(mode types.PermissionMode) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.mode = mode
}

// WatchPermissionFiles starts watching permission files for changes.
// Returns a channel that emits when files change.
func (pl *PermissionLoader) WatchPermissionFiles(pollInterval time.Duration) <-chan struct{} {
	ch := make(chan struct{}, 1)

	pl.mu.Lock()
	pl.watchers = append(pl.watchers, ch)
	pl.mu.Unlock()

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for range ticker.C {
			if pl.checkForChanges() {
				if err := pl.LoadPermissions(); err != nil {
					pl.logger.Warn("failed to reload permissions", "error", err)
					continue
				}
				// Non-blocking notify
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()

	return ch
}

// checkForChanges checks if any settings files have been modified.
func (pl *PermissionLoader) checkForChanges() bool {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	for path, lastMod := range pl.lastLoadedPaths {
		info, err := os.Stat(path)
		if err != nil {
			// File was deleted
			return true
		}
		if info.ModTime().After(lastMod) {
			return true
		}
	}

	// Check for newly created files
	sources := []types.PermissionRuleSource{
		types.SourceUserSettings,
		types.SourceProjectSettings,
		types.SourceLocalSettings,
		types.SourcePolicySettings,
	}
	for _, source := range sources {
		path := pl.settingsPath(source)
		if path == "" {
			continue
		}
		if _, exists := pl.lastLoadedPaths[path]; !exists {
			if _, err := os.Stat(path); err == nil {
				return true // New file appeared
			}
		}
	}

	return false
}

// SavePermissions saves rules to a specific settings file.
func (pl *PermissionLoader) SavePermissions(source types.PermissionRuleSource) error {
	path := pl.settingsPath(source)
	if path == "" {
		return nil
	}

	pl.mu.RLock()
	defer pl.mu.RUnlock()

	// Build settings structure
	settings := SettingsJSON{
		Permissions: &SettingsPermissions{},
	}

	if rules, ok := pl.ruleSet.AllowRules[source]; ok {
		for _, r := range rules {
			settings.Permissions.Allow = append(settings.Permissions.Allow, PermissionRuleValueToString(r))
		}
	}
	if rules, ok := pl.ruleSet.DenyRules[source]; ok {
		for _, r := range rules {
			settings.Permissions.Deny = append(settings.Permissions.Deny, PermissionRuleValueToString(r))
		}
	}
	if rules, ok := pl.ruleSet.AskRules[source]; ok {
		for _, r := range rules {
			settings.Permissions.Ask = append(settings.Permissions.Ask, PermissionRuleValueToString(r))
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// AddRulesToSettings adds permission rules to a settings file.
func (pl *PermissionLoader) AddRulesToSettings(rules []types.PermissionRule, source types.PermissionRuleSource) error {
	path := pl.settingsPath(source)
	if path == "" {
		return nil
	}

	// Read existing settings
	existing := pl.readSettingsFile(path)
	if existing == nil {
		existing = &SettingsJSON{
			Permissions: &SettingsPermissions{},
		}
	}
	if existing.Permissions == nil {
		existing.Permissions = &SettingsPermissions{}
	}

	// Add new rules
	for _, rule := range rules {
		ruleStr := PermissionRuleValueToString(rule)

		switch rule.Behavior {
		case "allow":
			if !containsString(existing.Permissions.Allow, ruleStr) {
				existing.Permissions.Allow = append(existing.Permissions.Allow, ruleStr)
			}
		case "deny":
			if !containsString(existing.Permissions.Deny, ruleStr) {
				existing.Permissions.Deny = append(existing.Permissions.Deny, ruleStr)
			}
		case "ask":
			if !containsString(existing.Permissions.Ask, ruleStr) {
				existing.Permissions.Ask = append(existing.Permissions.Ask, ruleStr)
			}
		}
	}

	// Write back
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// DeleteRuleFromSettings removes a rule from a settings file.
func (pl *PermissionLoader) DeleteRuleFromSettings(rule types.PermissionRule, source types.PermissionRuleSource) error {
	path := pl.settingsPath(source)
	if path == "" {
		return nil
	}

	existing := pl.readSettingsFile(path)
	if existing == nil || existing.Permissions == nil {
		return nil
	}

	ruleStr := PermissionRuleValueToString(rule)

	switch rule.Behavior {
	case "allow":
		existing.Permissions.Allow = removeString(existing.Permissions.Allow, ruleStr)
	case "deny":
		existing.Permissions.Deny = removeString(existing.Permissions.Deny, ruleStr)
	case "ask":
		existing.Permissions.Ask = removeString(existing.Permissions.Ask, ruleStr)
	}

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// DefaultPermissionRulesForMode returns the default permission rules for a given mode.
func DefaultPermissionRulesForMode(mode types.PermissionMode) []types.PermissionRule {
	switch mode {
	case types.ModePlan:
		// Plan mode: allow all read, ask for write
		return []types.PermissionRule{
			{Behavior: "allow", ToolName: "Read"},
			{Behavior: "allow", ToolName: "Grep"},
			{Behavior: "allow", ToolName: "Glob"},
			{Behavior: "allow", ToolName: "LSP"},
			{Behavior: "ask", ToolName: "Bash"},
			{Behavior: "ask", ToolName: "Write"},
			{Behavior: "ask", ToolName: "Edit"},
		}
	case types.ModeAcceptEdits:
		// Accept edits: allow file operations, ask for bash
		return []types.PermissionRule{
			{Behavior: "allow", ToolName: "Read"},
			{Behavior: "allow", ToolName: "Grep"},
			{Behavior: "allow", ToolName: "Glob"},
			{Behavior: "allow", ToolName: "Write"},
			{Behavior: "allow", ToolName: "Edit"},
			{Behavior: "allow", ToolName: "MultiEdit"},
			{Behavior: "ask", ToolName: "Bash"},
		}
	case types.ModeBypass:
		// Bypass: allow everything
		return []types.PermissionRule{
			{Behavior: "allow", ToolName: "*"},
		}
	case types.ModeDontAsk:
		// Don't ask: deny everything that would ask
		return nil
	default:
		// Default: standard rules
		return nil
	}
}

// --- Helpers ---

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		// Normalize via parse→serialize roundtrip
		normalized := PermissionRuleValueToString(ParsePermissionRuleValue(item))
		if normalized != s {
			result = append(result, item)
		}
	}
	return result
}
