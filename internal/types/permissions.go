package types

// PermissionMode controls how permissions are evaluated.
type PermissionMode string

const (
	// ModeDefault prompts the user for each tool call that requires permission.
	ModeDefault PermissionMode = "default"

	// ModeAcceptEdits auto-approves file edit operations but prompts for others.
	ModeAcceptEdits PermissionMode = "acceptEdits"

	// ModePlan allows all tools but simulates destructive operations.
	ModePlan PermissionMode = "plan"

	// ModeDontAsk auto-approves all non-destructive tool calls.
	ModeDontAsk PermissionMode = "dontAsk"

	// ModeBypass skips all permission checks (dangerous).
	ModeBypass PermissionMode = "bypassPermissions"
)

// PermissionRuleSource indicates where a permission rule came from.
type PermissionRuleSource string

const (
	SourceUserSettings    PermissionRuleSource = "userSettings"
	SourceProjectSettings PermissionRuleSource = "projectSettings"
	SourceLocalSettings   PermissionRuleSource = "localSettings"
	SourceFlagSettings    PermissionRuleSource = "flagSettings"
	SourcePolicySettings  PermissionRuleSource = "policySettings"
	SourceCLIArg          PermissionRuleSource = "cliArg"
	SourceCommand         PermissionRuleSource = "command"
	SourceSession         PermissionRuleSource = "session"
)

// PermissionRule defines a single permission rule.
type PermissionRule struct {
	Source      PermissionRuleSource `json:"source" yaml:"source"`
	Behavior    PermissionBehavior   `json:"behavior" yaml:"behavior"`
	ToolName    string               `json:"tool_name" yaml:"tool_name"`
	RuleContent string               `json:"rule_content,omitempty" yaml:"rule_content,omitempty"`
}

// PermissionDecisionReason explains why a permission decision was made.
type PermissionDecisionReason struct {
	Type   string // "rule", "mode", "hook", "classifier", "default"
	Detail string // Human-readable explanation
	Rule   *PermissionRule
}

// PermissionDecision is the full result of a permission check.
type PermissionDecision struct {
	Behavior       PermissionBehavior
	Message        string
	DecisionReason *PermissionDecisionReason
	ToolUseID      string
}

// PermissionContext holds the state needed for permission evaluation.
type PermissionContext struct {
	Mode                         PermissionMode
	AdditionalWorkingDirs        map[string]string
	AlwaysAllowRules             []PermissionRule
	AlwaysDenyRules              []PermissionRule
	AlwaysAskRules               []PermissionRule
	IsBypassPermissionsAvailable bool
	ShouldAvoidPermissionPrompts bool
}

// DefaultPermissionContext returns a context with safe defaults.
func DefaultPermissionContext() PermissionContext {
	return PermissionContext{
		Mode:                         ModeDefault,
		AdditionalWorkingDirs:        make(map[string]string),
		IsBypassPermissionsAvailable: false,
	}
}
