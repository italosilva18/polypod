package permissions

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// ============================================================================
// Denial Tracking
// ============================================================================

// DenialLimits defines when to fall back to user prompting.
var DenialLimits = struct {
	MaxConsecutive int
	MaxTotal       int
}{
	MaxConsecutive: 3,
	MaxTotal:       20,
}

// DenialTrackingState tracks consecutive and total denials for classifiers.
type DenialTrackingState struct {
	ConsecutiveDenials int
	TotalDenials       int
}

// NewDenialTrackingState creates a fresh denial tracking state.
func NewDenialTrackingState() DenialTrackingState {
	return DenialTrackingState{}
}

// RecordDenial increments both consecutive and total denial counters.
func (s DenialTrackingState) RecordDenial() DenialTrackingState {
	return DenialTrackingState{
		ConsecutiveDenials: s.ConsecutiveDenials + 1,
		TotalDenials:       s.TotalDenials + 1,
	}
}

// RecordSuccess resets the consecutive denial counter.
// Returns the same state if already at zero (no allocation).
func (s DenialTrackingState) RecordSuccess() DenialTrackingState {
	if s.ConsecutiveDenials == 0 {
		return s
	}
	return DenialTrackingState{
		ConsecutiveDenials: 0,
		TotalDenials:       s.TotalDenials,
	}
}

// ShouldFallbackToPrompting returns true if denial limits have been exceeded.
func (s DenialTrackingState) ShouldFallbackToPrompting() bool {
	return s.ConsecutiveDenials >= DenialLimits.MaxConsecutive ||
		s.TotalDenials >= DenialLimits.MaxTotal
}

// HitTotalLimit returns true if the total denial limit was hit.
func (s DenialTrackingState) HitTotalLimit() bool {
	return s.TotalDenials >= DenialLimits.MaxTotal
}

// DenialTracker manages denial tracking with thread safety.
type DenialTracker struct {
	mu    sync.RWMutex
	state DenialTrackingState
}

// NewDenialTracker creates a new denial tracker.
func NewDenialTracker() *DenialTracker {
	return &DenialTracker{
		state: NewDenialTrackingState(),
	}
}

// RecordDenial records a denial.
func (dt *DenialTracker) RecordDenial() DenialTrackingState {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.state = dt.state.RecordDenial()
	return dt.state
}

// RecordSuccess records a successful approval.
func (dt *DenialTracker) RecordSuccess() DenialTrackingState {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.state = dt.state.RecordSuccess()
	return dt.state
}

// State returns the current denial tracking state.
func (dt *DenialTracker) State() DenialTrackingState {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.state
}

// Reset resets the denial tracking state.
func (dt *DenialTracker) Reset() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.state = NewDenialTrackingState()
}

// ResetTotal resets the total counter (keeping consecutive at zero).
func (dt *DenialTracker) ResetTotal() {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.state = DenialTrackingState{
		ConsecutiveDenials: 0,
		TotalDenials:       0,
	}
}

// ============================================================================
// Auto Mode State
// ============================================================================

// AutoModeState tracks the state of auto mode (YOLO mode).
type AutoModeState struct {
	mu             sync.RWMutex
	active         bool
	flagCli        bool
	circuitBroken  bool
	totalApprovals int
	totalDenials   int
	activatedAt    time.Time
}

// NewAutoModeState creates a new auto mode state.
func NewAutoModeState() *AutoModeState {
	return &AutoModeState{}
}

// SetActive sets whether auto mode is active.
func (s *AutoModeState) SetActive(active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = active
	if active {
		s.activatedAt = time.Now()
	}
}

// IsActive returns whether auto mode is currently active.
func (s *AutoModeState) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// SetFlagCli records that auto mode was passed as a CLI flag.
func (s *AutoModeState) SetFlagCli(passed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flagCli = passed
}

// FlagCli returns whether auto mode was passed as a CLI flag.
func (s *AutoModeState) FlagCli() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.flagCli
}

// SetCircuitBroken marks auto mode as circuit-broken (disabled by server).
func (s *AutoModeState) SetCircuitBroken(broken bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.circuitBroken = broken
}

// IsCircuitBroken returns whether auto mode is circuit-broken.
func (s *AutoModeState) IsCircuitBroken() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.circuitBroken
}

// RecordApproval increments the approval counter.
func (s *AutoModeState) RecordApproval() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalApprovals++
}

// RecordAutoDenial increments the denial counter.
func (s *AutoModeState) RecordAutoDenial() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalDenials++
}

// Stats returns the current auto mode statistics.
func (s *AutoModeState) Stats() (approvals, denials int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalApprovals, s.totalDenials
}

// Reset resets the auto mode state.
func (s *AutoModeState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.flagCli = false
	s.circuitBroken = false
	s.totalApprovals = 0
	s.totalDenials = 0
}

// ============================================================================
// Permission Explainer
// ============================================================================

// RiskLevel represents the risk level of a permission decision.
type RiskLevel string

const (
	RiskLow    RiskLevel = "LOW"
	RiskMedium RiskLevel = "MEDIUM"
	RiskHigh   RiskLevel = "HIGH"
)

// PermissionExplanation is a human-readable explanation of a permission decision.
type PermissionExplanation struct {
	RiskLevel   RiskLevel
	Explanation string
	Reasoning   string
	Risk        string
}

// PermissionExplainer generates human-readable explanations for permission decisions.
type PermissionExplainer struct {
	logger  *slog.Logger
	enabled bool
}

// NewPermissionExplainer creates a new permission explainer.
func NewPermissionExplainer(logger *slog.Logger) *PermissionExplainer {
	return &PermissionExplainer{
		logger:  logger,
		enabled: true,
	}
}

// SetEnabled enables or disables the permission explainer.
func (pe *PermissionExplainer) SetEnabled(enabled bool) {
	pe.enabled = enabled
}

// IsEnabled returns whether the permission explainer is enabled.
func (pe *PermissionExplainer) IsEnabled() bool {
	return pe.enabled
}

// ExplainBashCommand generates an explanation for a bash command permission.
func (pe *PermissionExplainer) ExplainBashCommand(command string) PermissionExplanation {
	cls := ClassifyBashCommand(command)
	dangerMatches := DetectDangerousPatterns(command)
	maxSeverity := MaxDangerSeverity(dangerMatches)

	var riskLevel RiskLevel
	var risk string

	switch {
	case maxSeverity >= SeverityCritical:
		riskLevel = RiskHigh
		risk = "CRITICAL: " + dangerMatches[0].Description
	case maxSeverity >= SeverityHigh:
		riskLevel = RiskHigh
		risk = dangerMatches[0].Description
	case maxSeverity >= SeverityMedium:
		riskLevel = RiskMedium
		risk = dangerMatches[0].Description
	case cls.Destructive:
		riskLevel = RiskMedium
		risk = "Command may cause irreversible changes"
	case cls.ReadOnly:
		riskLevel = RiskLow
		risk = "Read-only operation"
	case cls.Category == CategoryFileWrite:
		riskLevel = RiskLow
		risk = "File modification within project"
	default:
		riskLevel = RiskLow
		risk = "Standard development operation"
	}

	return PermissionExplanation{
		RiskLevel:   riskLevel,
		Explanation: cls.Reason,
		Reasoning:   fmt.Sprintf("Command classified as %s (%s)", cls.Category.String(), cls.Confidence),
		Risk:        risk,
	}
}

// ExplainToolUse generates an explanation for a tool use permission.
func (pe *PermissionExplainer) ExplainToolUse(toolName string, input map[string]interface{}) PermissionExplanation {
	switch toolName {
	case "Bash", "bash":
		if cmd, ok := input["command"].(string); ok {
			return pe.ExplainBashCommand(cmd)
		}
	case "Write", "Edit", "MultiEdit", "FileEdit":
		path, _ := input["file_path"].(string)
		if path == "" {
			path, _ = input["path"].(string)
		}
		if isDangerousFile(path) {
			return PermissionExplanation{
				RiskLevel:   RiskHigh,
				Explanation: "Editing a sensitive file: " + path,
				Reasoning:   "This file is on the list of dangerous/sensitive files",
				Risk:        "Modifying this file could affect system behavior or security",
			}
		}
		return PermissionExplanation{
			RiskLevel:   RiskLow,
			Explanation: "File edit: " + path,
			Reasoning:   "Standard file edit operation",
			Risk:        "File will be modified",
		}
	}

	return PermissionExplanation{
		RiskLevel:   RiskMedium,
		Explanation: fmt.Sprintf("Tool use: %s", toolName),
		Reasoning:   "Tool requires permission",
		Risk:        "Unknown risk for this tool",
	}
}

// ExplainDecision generates an explanation for a permission decision.
func (pe *PermissionExplainer) ExplainDecision(decision types.PermissionDecision) string {
	if decision.DecisionReason == nil {
		return decision.Message
	}

	reason := decision.DecisionReason
	switch reason.Type {
	case "rule":
		if reason.Rule != nil {
			return fmt.Sprintf("Permission rule '%s' from %s %s this action",
				PermissionRuleValueToString(*reason.Rule),
				reason.Rule.Source,
				behaviorVerb(string(decision.Behavior)))
		}
	case "mode":
		return fmt.Sprintf("Current permission mode %s this action", behaviorVerb(string(decision.Behavior)))
	case "classifier":
		return fmt.Sprintf("Classifier %s: %s", behaviorVerb(string(decision.Behavior)), reason.Detail)
	case "hook":
		return fmt.Sprintf("Hook %s this action: %s", behaviorVerb(string(decision.Behavior)), reason.Detail)
	case "safetyCheck":
		return fmt.Sprintf("Safety check: %s", reason.Detail)
	}

	return decision.Message
}

func behaviorVerb(behavior string) string {
	switch behavior {
	case "allow":
		return "allowed"
	case "deny":
		return "denied"
	case "ask":
		return "requires approval for"
	default:
		return behavior
	}
}

// ============================================================================
// Shadowed Rule Detection
// ============================================================================

// ShadowType indicates the type of shadowing.
type ShadowType string

const (
	ShadowTypeDeny ShadowType = "deny"
	ShadowTypeAsk  ShadowType = "ask"
)

// UnreachableRule represents a permission rule that can never fire.
type UnreachableRule struct {
	Rule       types.PermissionRule
	Reason     string
	ShadowedBy types.PermissionRule
	ShadowType ShadowType
	Fix        string
}

// DetectUnreachableRulesOptions configures unreachable rule detection.
type DetectUnreachableRulesOptions struct {
	SandboxAutoAllowEnabled bool
}

// IsSharedSettingSource returns true if a source is shared (visible to team).
func IsSharedSettingSource(source types.PermissionRuleSource) bool {
	return source == types.SourceProjectSettings ||
		source == types.SourcePolicySettings ||
		source == types.SourceCommand
}

// DetectUnreachableRules finds permission rules that are shadowed by broader rules.
func DetectUnreachableRules(ruleSet *PermissionRuleSet, opts DetectUnreachableRulesOptions) []UnreachableRule {
	var unreachable []UnreachableRule

	allowRules := ruleSet.GetAllRules("allow")
	askRules := ruleSet.GetAllRules("ask")
	denyRules := ruleSet.GetAllRules("deny")

	for _, allowRule := range allowRules {
		// Only check content-specific allow rules
		if allowRule.RuleContent == "" {
			continue
		}

		// Check deny-shadowing first (more severe)
		denyShadow := findToolWideShadow(allowRule, denyRules)
		if denyShadow != nil {
			unreachable = append(unreachable, UnreachableRule{
				Rule:       allowRule,
				ShadowedBy: *denyShadow,
				ShadowType: ShadowTypeDeny,
				Reason:     fmt.Sprintf("blocked by \"%s\" deny rule (from %s)", denyShadow.ToolName, denyShadow.Source),
				Fix:        fmt.Sprintf("remove the \"%s\" deny rule from %s, or remove the specific allow rule from %s", denyShadow.ToolName, denyShadow.Source, allowRule.Source),
			})
			continue
		}

		// Check ask-shadowing
		askShadow := findToolWideShadow(allowRule, askRules)
		if askShadow != nil {
			// Special case: Bash with sandbox auto-allow from personal settings
			if allowRule.ToolName == "Bash" && opts.SandboxAutoAllowEnabled {
				if !IsSharedSettingSource(askShadow.Source) {
					continue // sandbox will auto-allow
				}
			}

			unreachable = append(unreachable, UnreachableRule{
				Rule:       allowRule,
				ShadowedBy: *askShadow,
				ShadowType: ShadowTypeAsk,
				Reason:     fmt.Sprintf("shadowed by \"%s\" ask rule (from %s)", askShadow.ToolName, askShadow.Source),
				Fix:        fmt.Sprintf("remove the \"%s\" ask rule from %s, or remove the specific allow rule from %s", askShadow.ToolName, askShadow.Source, allowRule.Source),
			})
		}
	}

	return unreachable
}

// findToolWideShadow finds a tool-wide rule that shadows a content-specific rule.
func findToolWideShadow(rule types.PermissionRule, candidates []types.PermissionRule) *types.PermissionRule {
	for i := range candidates {
		if candidates[i].ToolName == rule.ToolName && candidates[i].RuleContent == "" {
			return &candidates[i]
		}
	}
	return nil
}

// ============================================================================
// Permission Decision Helpers
// ============================================================================

// BuildPermissionRequestMessage creates a message explaining a permission request.
func BuildPermissionRequestMessage(toolName string, reason *types.PermissionDecisionReason) string {
	if reason == nil {
		return fmt.Sprintf("Polypod requested permissions to use %s, but you haven't granted it yet.", toolName)
	}

	switch reason.Type {
	case "classifier":
		return fmt.Sprintf("Classifier '%s' requires approval for this %s command: %s",
			reason.Type, toolName, reason.Detail)
	case "hook":
		if reason.Detail != "" {
			return fmt.Sprintf("Hook blocked this action: %s", reason.Detail)
		}
		return fmt.Sprintf("Hook requires approval for this %s command", toolName)
	case "rule":
		if reason.Rule != nil {
			ruleStr := PermissionRuleValueToString(*reason.Rule)
			return fmt.Sprintf("Permission rule '%s' from %s requires approval for this %s command",
				ruleStr, reason.Rule.Source, toolName)
		}
	case "mode":
		return fmt.Sprintf("Current permission mode requires approval for this %s command", toolName)
	case "safetyCheck":
		return reason.Detail
	case "workingDir":
		return reason.Detail
	case "asyncAgent":
		return reason.Detail
	}

	return fmt.Sprintf("Polypod requested permissions to use %s, but you haven't granted it yet.", toolName)
}

// BuildYoloRejectionMessage creates a message for when the auto mode classifier blocks an action.
func BuildYoloRejectionMessage(reason string) string {
	return fmt.Sprintf("The auto-mode classifier blocked this action: %s\n\nPlease modify your approach to avoid this action, or ask the user to approve it manually.", reason)
}

// BuildClassifierUnavailableMessage creates a message for when the classifier is unavailable.
func BuildClassifierUnavailableMessage(toolName, model string) string {
	msg := fmt.Sprintf("The auto-mode classifier could not evaluate this %s action", toolName)
	if model != "" {
		msg += fmt.Sprintf(" (model: %s)", model)
	}
	msg += ". Please try a different approach."
	return msg
}
