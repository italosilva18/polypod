package queryctx

import (
	"os"
	"strings"
	"sync"
	"time"
)

// FastModeState represents the current operational state of fast mode.
type FastModeState string

const (
	FastModeOff      FastModeState = "off"
	FastModeOn       FastModeState = "on"
	FastModeCooldown FastModeState = "cooldown"
)

// CooldownReason identifies why fast mode entered cooldown.
type CooldownReason string

const (
	CooldownRateLimit  CooldownReason = "rate_limit"
	CooldownOverloaded CooldownReason = "overloaded"
)

// FastModeDisabledReason is the reason returned by the API for why fast mode is disabled.
type FastModeDisabledReason string

const (
	DisabledReasonFree         FastModeDisabledReason = "free"
	DisabledReasonPreference   FastModeDisabledReason = "preference"
	DisabledReasonExtraUsage   FastModeDisabledReason = "extra_usage_disabled"
	DisabledReasonNetworkError FastModeDisabledReason = "network_error"
	DisabledReasonUnknown      FastModeDisabledReason = "unknown"
)

// FastModeConfig configures fast mode behavior: reduced output, fewer tools, cheaper model.
type FastModeConfig struct {
	// Enabled indicates the user has opted in to fast mode.
	Enabled bool

	// Model is the model to use in fast mode (e.g., "opus").
	Model string

	// ModelDisplay is the human-readable model name (e.g., "Opus 4.6").
	ModelDisplay string

	// PerSessionOptIn requires user to enable fast mode each session.
	PerSessionOptIn bool

	// mu protects runtime state.
	mu sync.RWMutex

	// runtimeStatus tracks the actual operational state.
	runtimeStatus fastModeRuntimeState

	// orgStatus tracks org-level fast mode enablement.
	orgStatus fastModeOrgStatus

	// cooldownListeners are called when cooldown is triggered.
	cooldownListeners []func(resetAt time.Time, reason CooldownReason)

	// cooldownExpiredListeners are called when cooldown expires.
	cooldownExpiredListeners []func()

	// hasLoggedCooldownExpiry prevents duplicate expiry logs.
	hasLoggedCooldownExpiry bool
}

type fastModeRuntimeState struct {
	status  FastModeState
	resetAt time.Time
	reason  CooldownReason
}

type fastModeOrgStatus struct {
	status         string // "pending", "enabled", "disabled"
	disabledReason FastModeDisabledReason
}

// NewFastModeConfig creates a FastModeConfig with defaults.
func NewFastModeConfig() *FastModeConfig {
	return &FastModeConfig{
		Model:        "opus",
		ModelDisplay: "Opus 4.6",
		runtimeStatus: fastModeRuntimeState{
			status: FastModeOn,
		},
		orgStatus: fastModeOrgStatus{
			status: "pending",
		},
	}
}

// IsFastModeEnabled returns true if fast mode is not globally disabled.
func IsFastModeEnabled() bool {
	return !isEnvTruthy(os.Getenv("CLAUDE_CODE_DISABLE_FAST_MODE"))
}

// IsFastModeAvailable checks if fast mode is available considering all constraints.
func (fc *FastModeConfig) IsFastModeAvailable() bool {
	if !IsFastModeEnabled() {
		return false
	}
	return fc.GetUnavailableReason() == ""
}

// GetUnavailableReason returns a human-readable reason why fast mode is unavailable,
// or empty string if it is available.
func (fc *FastModeConfig) GetUnavailableReason() string {
	if !IsFastModeEnabled() {
		return "Fast mode is not available"
	}

	fc.mu.RLock()
	orgSt := fc.orgStatus
	fc.mu.RUnlock()

	if orgSt.status == "disabled" {
		return getDisabledReasonMessage(orgSt.disabledReason)
	}

	return ""
}

func getDisabledReasonMessage(reason FastModeDisabledReason) string {
	switch reason {
	case DisabledReasonFree:
		return "Fast mode requires a paid subscription"
	case DisabledReasonPreference:
		return "Fast mode has been disabled by your organization"
	case DisabledReasonExtraUsage:
		return "Fast mode requires extra usage billing"
	case DisabledReasonNetworkError:
		return "Fast mode unavailable due to network connectivity issues"
	default:
		return "Fast mode is currently unavailable"
	}
}

// IsFastMode returns true if fast mode should be active for the current request.
// Checks enabled + available + supported model + not in cooldown.
func (fc *FastModeConfig) IsFastMode(model string) bool {
	if !fc.Enabled {
		return false
	}
	if !fc.IsFastModeAvailable() {
		return false
	}
	if !IsFastModeSupportedByModel(model) {
		return false
	}
	return fc.GetRuntimeState() != FastModeCooldown
}

// GetFastModeState returns the current fast mode state as a combined check.
func (fc *FastModeConfig) GetFastModeState(model string) FastModeState {
	if !fc.Enabled || !fc.IsFastModeAvailable() || !IsFastModeSupportedByModel(model) {
		return FastModeOff
	}
	rs := fc.GetRuntimeState()
	if rs == FastModeCooldown {
		return FastModeCooldown
	}
	return FastModeOn
}

// GetRuntimeState returns the current runtime state, automatically expiring cooldowns.
func (fc *FastModeConfig) GetRuntimeState() FastModeState {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.runtimeStatus.status == FastModeCooldown {
		if time.Now().After(fc.runtimeStatus.resetAt) {
			if !fc.hasLoggedCooldownExpiry {
				fc.hasLoggedCooldownExpiry = true
				// Notify listeners.
				for _, fn := range fc.cooldownExpiredListeners {
					fn()
				}
			}
			fc.runtimeStatus.status = FastModeOn
		}
	}
	return fc.runtimeStatus.status
}

// TriggerCooldown puts fast mode into cooldown until the given reset time.
func (fc *FastModeConfig) TriggerCooldown(resetAt time.Time, reason CooldownReason) {
	if !IsFastModeEnabled() {
		return
	}

	fc.mu.Lock()
	fc.runtimeStatus = fastModeRuntimeState{
		status:  FastModeCooldown,
		resetAt: resetAt,
		reason:  reason,
	}
	fc.hasLoggedCooldownExpiry = false
	listeners := make([]func(time.Time, CooldownReason), len(fc.cooldownListeners))
	copy(listeners, fc.cooldownListeners)
	fc.mu.Unlock()

	for _, fn := range listeners {
		fn(resetAt, reason)
	}
}

// ClearCooldown resets the runtime state to active.
func (fc *FastModeConfig) ClearCooldown() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.runtimeStatus.status = FastModeOn
}

// OnCooldownTriggered registers a listener for cooldown trigger events.
func (fc *FastModeConfig) OnCooldownTriggered(fn func(resetAt time.Time, reason CooldownReason)) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.cooldownListeners = append(fc.cooldownListeners, fn)
}

// OnCooldownExpired registers a listener for cooldown expiry events.
func (fc *FastModeConfig) OnCooldownExpired(fn func()) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.cooldownExpiredListeners = append(fc.cooldownExpiredListeners, fn)
}

// SetOrgEnabled updates the org-level fast mode status.
func (fc *FastModeConfig) SetOrgEnabled(enabled bool, reason FastModeDisabledReason) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if enabled {
		fc.orgStatus = fastModeOrgStatus{status: "enabled"}
	} else {
		fc.orgStatus = fastModeOrgStatus{
			status:         "disabled",
			disabledReason: reason,
		}
	}
}

// IsFastModeSupportedByModel checks if the given model supports fast mode.
func IsFastModeSupportedByModel(model string) bool {
	if !IsFastModeEnabled() {
		return false
	}
	return strings.Contains(strings.ToLower(model), "opus-4-6")
}

// ApplyFastMode modifies query parameters for fast mode execution.
// Returns modified model and max tokens. If fast mode is not active,
// returns the original values unchanged.
func (fc *FastModeConfig) ApplyFastMode(model string, maxTokens int) (string, int) {
	if !fc.IsFastMode(model) {
		return model, maxTokens
	}
	return fc.Model, maxTokens
}

// FastModeToolFilter returns a function that filters tools for fast mode.
// In fast mode, only essential tools are included to reduce latency.
func FastModeToolFilter(essentialTools []string) func(toolName string) bool {
	essential := make(map[string]bool, len(essentialTools))
	for _, name := range essentialTools {
		essential[name] = true
	}
	return func(toolName string) bool {
		return essential[toolName]
	}
}

// isEnvTruthy checks if an env var value is truthy.
func isEnvTruthy(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == "1" || v == "true" || v == "yes"
}
