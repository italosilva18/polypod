package hooks

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// HookType discriminates between command, prompt, agent, and http hooks.
type HookType string

const (
	HookTypeCommand HookType = "command"
	HookTypePrompt  HookType = "prompt"
	HookTypeAgent   HookType = "agent"
	HookTypeHTTP    HookType = "http"
)

// ShellType controls which shell interpreter is used for command hooks.
type ShellType string

const (
	ShellBash       ShellType = "bash"
	ShellPowerShell ShellType = "powershell"
)

// HookCommand is the discriminated union for hook definitions, matching
// Claude Code's HookCommand schema with command/prompt/agent/http variants.
type HookCommand struct {
	// Discriminator
	Type HookType `json:"type" yaml:"type"`

	// Command hook fields
	Command     string    `json:"command,omitempty" yaml:"command,omitempty"`
	Shell       ShellType `json:"shell,omitempty" yaml:"shell,omitempty"`
	Async       bool      `json:"async,omitempty" yaml:"async,omitempty"`
	AsyncRewake bool      `json:"asyncRewake,omitempty" yaml:"asyncRewake,omitempty"`

	// Prompt/Agent hook fields
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Model  string `json:"model,omitempty" yaml:"model,omitempty"`

	// HTTP hook fields
	URL            string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers        map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	AllowedEnvVars []string          `json:"allowedEnvVars,omitempty" yaml:"allowedEnvVars,omitempty"`

	// Shared fields
	If            string `json:"if,omitempty" yaml:"if,omitempty"`
	Timeout       int    `json:"timeout,omitempty" yaml:"timeout,omitempty"` // seconds
	StatusMessage string `json:"statusMessage,omitempty" yaml:"statusMessage,omitempty"`
	Once          bool   `json:"once,omitempty" yaml:"once,omitempty"`
}

// Validate checks if the HookCommand is well-formed.
func (hc *HookCommand) Validate() error {
	switch hc.Type {
	case HookTypeCommand:
		if hc.Command == "" {
			return fmt.Errorf("command hook requires 'command' field")
		}
	case HookTypePrompt:
		if hc.Prompt == "" {
			return fmt.Errorf("prompt hook requires 'prompt' field")
		}
	case HookTypeAgent:
		if hc.Prompt == "" {
			return fmt.Errorf("agent hook requires 'prompt' field")
		}
	case HookTypeHTTP:
		if hc.URL == "" {
			return fmt.Errorf("http hook requires 'url' field")
		}
	default:
		return fmt.Errorf("unknown hook type: %q", hc.Type)
	}
	if hc.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	return nil
}

// HookMatcher groups hooks under a string matcher pattern.
type HookMatcher struct {
	Matcher string        `json:"matcher,omitempty" yaml:"matcher,omitempty"`
	Hooks   []HookCommand `json:"hooks" yaml:"hooks"`
}

// HooksSettings is the full hook configuration, keyed by event type.
// Each event maps to a list of matchers, each with their own hooks.
// Matches Claude Code's HooksSettings = Partial<Record<HookEvent, HookMatcher[]>>.
type HooksSettings map[HookEvent][]HookMatcher

// Validate checks all hooks in the settings.
func (hs HooksSettings) Validate() error {
	for event, matchers := range hs {
		if !IsValidHookEvent(string(event)) {
			return fmt.Errorf("unknown hook event: %q", event)
		}
		for i, m := range matchers {
			for j, hook := range m.Hooks {
				if err := hook.Validate(); err != nil {
					return fmt.Errorf("event %s, matcher[%d], hook[%d]: %w", event, i, j, err)
				}
			}
		}
	}
	return nil
}

// Merge merges another HooksSettings into this one. Matchers from other are
// appended to existing events. This implements Claude Code's multi-source
// config merging (project, user, skill, policy).
func (hs HooksSettings) Merge(other HooksSettings) HooksSettings {
	if hs == nil {
		hs = make(HooksSettings)
	}
	for event, matchers := range other {
		hs[event] = append(hs[event], matchers...)
	}
	return hs
}

// Clone returns a deep copy of the HooksSettings.
func (hs HooksSettings) Clone() HooksSettings {
	if hs == nil {
		return nil
	}
	data, _ := json.Marshal(hs)
	var clone HooksSettings
	json.Unmarshal(data, &clone) //nolint:errcheck
	return clone
}

// IndividualHookConfig represents a single hook with its full context.
type IndividualHookConfig struct {
	Event      HookEvent
	Config     HookCommand
	Matcher    string
	Source     HookConfigSource
	PluginName string
	SkillRoot  string
}

// HookConfigSource indicates where a hook was defined.
type HookConfigSource string

const (
	SourceUserSettings    HookConfigSource = "userSettings"
	SourceProjectSettings HookConfigSource = "projectSettings"
	SourceLocalSettings   HookConfigSource = "localSettings"
	SourcePolicySettings  HookConfigSource = "policySettings"
	SourcePluginHook      HookConfigSource = "pluginHook"
	SourceBuiltinHook     HookConfigSource = "builtinHook"
	SourceSessionHook     HookConfigSource = "sessionHook"
	SourceFrontmatter     HookConfigSource = "frontmatter"
)

// HookConfigSnapshot holds an immutable snapshot of hook configuration
// captured once per query. This prevents config changes during hook execution
// from causing inconsistent behavior.
type HookConfigSnapshot struct {
	mu          sync.RWMutex
	config      HooksSettings
	managedOnly bool
	allDisabled bool
	policyHooks HooksSettings
	logger      *slog.Logger
}

// NewHookConfigSnapshot creates a new config snapshot.
func NewHookConfigSnapshot(logger *slog.Logger) *HookConfigSnapshot {
	return &HookConfigSnapshot{
		logger: logger,
	}
}

// Capture takes a snapshot of the current hooks configuration from all sources.
// Respects policy settings: if disableAllHooks is set in policy, no hooks run;
// if allowManagedHooksOnly is set, only policy hooks run.
func (s *HookConfigSnapshot) Capture(
	userHooks HooksSettings,
	projectHooks HooksSettings,
	localHooks HooksSettings,
	policyHooks HooksSettings,
	policyDisableAll bool,
	policyManagedOnly bool,
	mergedDisableAll bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.policyHooks = policyHooks
	s.allDisabled = policyDisableAll
	s.managedOnly = policyManagedOnly

	if policyDisableAll {
		// Managed settings disables all hooks: return empty
		s.config = make(HooksSettings)
		return
	}

	if policyManagedOnly {
		// Only use managed/policy hooks
		s.config = policyHooks.Clone()
		return
	}

	if mergedDisableAll && !policyDisableAll {
		// Non-managed settings disable their hooks, but managed hooks still run
		s.managedOnly = true
		s.config = policyHooks.Clone()
		return
	}

	// Merge all sources: policy overrides everything, then project, then user, then local
	merged := make(HooksSettings)
	merged = merged.Merge(userHooks)
	merged = merged.Merge(projectHooks)
	merged = merged.Merge(localHooks)
	merged = merged.Merge(policyHooks)
	s.config = merged
}

// CaptureSimple captures a snapshot from a single merged HooksSettings.
func (s *HookConfigSnapshot) CaptureSimple(hooks HooksSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = hooks.Clone()
	s.allDisabled = false
	s.managedOnly = false
}

// Get returns the captured hooks configuration. Returns nil if no snapshot
// has been captured yet.
func (s *HookConfigSnapshot) Get() HooksSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// IsManagedOnly returns true if only managed/policy hooks should run.
func (s *HookConfigSnapshot) IsManagedOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.managedOnly
}

// IsAllDisabled returns true if all hooks (including managed) are disabled.
func (s *HookConfigSnapshot) IsAllDisabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allDisabled
}

// Update refreshes the snapshot with new settings.
func (s *HookConfigSnapshot) Update(hooks HooksSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = hooks.Clone()
}

// Reset clears the snapshot.
func (s *HookConfigSnapshot) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = nil
	s.managedOnly = false
	s.allDisabled = false
	s.policyHooks = nil
}

// GroupByEventAndMatcher organizes hooks by event and matcher for efficient lookup.
func GroupByEventAndMatcher(hooks HooksSettings) map[HookEvent]map[string][]IndividualHookConfig {
	grouped := make(map[HookEvent]map[string][]IndividualHookConfig)
	for _, event := range AllHookEvents {
		grouped[event] = make(map[string][]IndividualHookConfig)
	}

	for event, matchers := range hooks {
		eventGroup := grouped[event]
		if eventGroup == nil {
			eventGroup = make(map[string][]IndividualHookConfig)
			grouped[event] = eventGroup
		}
		for _, m := range matchers {
			matcherKey := m.Matcher
			for _, hook := range m.Hooks {
				eventGroup[matcherKey] = append(eventGroup[matcherKey], IndividualHookConfig{
					Event:   event,
					Config:  hook,
					Matcher: matcherKey,
				})
			}
		}
	}
	return grouped
}

// SortMatchersByPriority returns matchers in execution priority order.
// Empty matcher (catch-all) comes last; specific matchers come first.
func SortMatchersByPriority(matchers []string) []string {
	// Partition: specific matchers first, then empty/catch-all
	var specific, catchAll []string
	for _, m := range matchers {
		if m == "" {
			catchAll = append(catchAll, m)
		} else {
			specific = append(specific, m)
		}
	}
	return append(specific, catchAll...)
}

// HookTimeouts defines default timeouts for different hook types.
var HookTimeouts = struct {
	ShellDefault  int // seconds
	HTTPDefault   int // seconds
	PromptDefault int // seconds
	AgentDefault  int // seconds
	AsyncDefault  int // milliseconds
}{
	ShellDefault:  600, // 10 minutes (matches TOOL_HOOK_EXECUTION_TIMEOUT_MS)
	HTTPDefault:   600, // 10 minutes
	PromptDefault: 30,
	AgentDefault:  60,
	AsyncDefault:  15000, // 15 seconds
}
