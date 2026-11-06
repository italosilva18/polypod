package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/costa/polypod/internal/bash"
	"github.com/costa/polypod/internal/modes"
	"github.com/costa/polypod/internal/types"
)

// AskApprovalFn is a callback for asking the user to approve a tool call.
// Returns true if approved, false if denied.
type AskApprovalFn func(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error)

// ApprovalRequest describes a permission prompt sent to the user.
type ApprovalRequest struct {
	ToolName    string
	Args        map[string]any
	Risk        ToolRisk
	Reason      string
	Explanation *PermissionExplanation
}

// ApprovalResponse is the user's response to an approval prompt.
type ApprovalResponse struct {
	Allowed   bool
	Permanent bool   // "always allow this"
	Feedback  string // optional user feedback
	Abort     bool   // user wants to abort entirely
}

// sessionRule is a session-scoped allow/deny rule.
type sessionRule struct {
	ToolName string
	Pattern  string // glob pattern for args (e.g. path prefix, command prefix)
}

// EnforcerConfig holds configuration for creating an Enforcer.
type EnforcerConfig struct {
	Logger     *slog.Logger
	Manager    *Manager
	PermCtx    *PermissionContext
	Modes      *modes.State
	ProjectDir string
	AskFn      AskApprovalFn
	YOLO       bool
}

// Enforcer provides the actual permission enforcement that connects to the tool pipeline.
// It classifies tool call risk, checks mode restrictions, evaluates rules, analyzes
// bash commands for dangerous patterns, verifies file paths, and prompts for approval.
type Enforcer struct {
	mu         sync.RWMutex
	logger     *slog.Logger
	manager    *Manager
	permCtx    *PermissionContext
	modes      *modes.State
	projectDir string
	askFn      AskApprovalFn
	yolo       bool

	// Pattern-based allow/deny matching from config rules.
	matcher *CompiledMatcher

	// Session-scoped approval store — remembered across repeated identical calls.
	store *SessionStore

	// Session-scoped rules
	sessionAllows []sessionRule
	sessionDenies []sessionRule

	// Stats
	totalChecks    atomic.Int64
	totalAllowed   atomic.Int64
	totalDenied    atomic.Int64
	totalAsked     atomic.Int64
	totalYoloAllow atomic.Int64
}

// NewEnforcer creates a new permission enforcer.
func NewEnforcer(cfg EnforcerConfig) *Enforcer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Enforcer{
		logger:     logger,
		manager:    cfg.Manager,
		permCtx:    cfg.PermCtx,
		modes:      cfg.Modes,
		projectDir: cfg.ProjectDir,
		askFn:      cfg.AskFn,
		yolo:       cfg.YOLO,
	}
}

// NewEnforcerWith creates a new permission enforcer with a CompiledMatcher for
// pattern-based rule matching and a SessionStore for remembering previously-approved
// tool invocations. Passing nil for either argument is safe — the Enforcer will
// simply skip that feature and fall through to the existing logic.
func NewEnforcerWith(cfg EnforcerConfig, matcher *CompiledMatcher, store *SessionStore) *Enforcer {
	e := NewEnforcer(cfg)
	e.matcher = matcher
	e.store = store
	return e
}

// CheckToolCall is the main entry point for permission enforcement.
// It runs the full pipeline: classify → mode check → rules → bash analysis →
// path validation → approval prompt → decision.
func (e *Enforcer) CheckToolCall(ctx context.Context, toolName string, args map[string]any) (types.PermissionDecision, error) {
	e.totalChecks.Add(1)
	start := time.Now()

	// Step 0: YOLO mode — auto-approve everything
	if e.isYOLO() {
		e.totalYoloAllow.Add(1)
		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "YOLO mode", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "YOLO mode — auto-approved",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "mode",
				Detail: "YOLO mode active — all operations auto-approved",
			},
		}, nil
	}

	// Step 1: Classify tool call risk
	risk := ClassifyToolRisk(toolName, args)

	// Step 1b: Auto mode — safe/low-risk tools auto-approved
	if e.modes != nil && e.modes.Current == modes.ModeAuto {
		if risk.Level <= ToolRiskLow {
			e.totalAllowed.Add(1)
			e.logCheck(toolName, "allow", "auto mode (low risk)", time.Since(start))
			return types.PermissionDecision{
				Behavior: types.PermissionAllow,
				Message:  "auto mode — low risk auto-approved",
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "classifier",
					Detail: risk.Reason,
				},
			}, nil
		}
	}

	// Step 2: Check mode restrictions (plan mode blocks writes)
	if decision, blocked := e.checkModeRestrictions(toolName, risk); blocked {
		e.totalDenied.Add(1)
		e.logCheck(toolName, "deny", "mode restriction", time.Since(start))
		return decision, nil
	}

	// Step 3: Check session-scoped deny rules
	if decision, denied := e.checkSessionDenies(toolName, args); denied {
		e.totalDenied.Add(1)
		e.logCheck(toolName, "deny", "session deny rule", time.Since(start))
		return decision, nil
	}

	// Step 4: Check session-scoped allow rules
	if decision, allowed := e.checkSessionAllows(toolName, args); allowed {
		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "session allow rule", time.Since(start))
		return decision, nil
	}

	// Step 4a: Check SessionStore — if this exact call was previously approved, auto-approve.
	if decision, hit := e.checkStoreApproval(toolName, args); hit {
		if decision.Behavior == types.PermissionAllow {
			e.totalAllowed.Add(1)
			e.logCheck(toolName, "allow", "session store (prior approval)", time.Since(start))
		} else {
			e.totalDenied.Add(1)
			e.logCheck(toolName, string(decision.Behavior), "session store (prior denial)", time.Since(start))
		}
		return decision, nil
	}

	// Step 4b: Check CompiledMatcher — pattern-based allow rules from config.
	if decision, matched := e.checkMatcherAllow(toolName, args); matched {
		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "compiled matcher rule", time.Since(start))
		return decision, nil
	}

	// Step 5: If we have a PermissionContext, delegate to it for the full rule evaluation
	if e.permCtx != nil {
		decision, err := e.permCtx.HandlePermissionDecision(ctx, toolName, args, "")
		if err != nil {
			return decision, err
		}
		// If the PermissionContext made a definitive decision, use it
		if decision.Behavior == types.PermissionAllow {
			e.totalAllowed.Add(1)
			e.logCheck(toolName, "allow", "permission context", time.Since(start))
			return decision, nil
		}
		if decision.Behavior == types.PermissionDeny {
			e.totalDenied.Add(1)
			e.logCheck(toolName, "deny", "permission context", time.Since(start))
			return decision, nil
		}
		// "ask" falls through to our enforcement below
	}

	// Step 6: For bash commands — deep analysis
	if isBashTool(toolName) {
		if decision, handled := e.analyzeBashCommand(ctx, toolName, args, risk); handled {
			if decision.Behavior == types.PermissionAllow {
				e.totalAllowed.Add(1)
			} else {
				e.totalDenied.Add(1)
			}
			e.logCheck(toolName, string(decision.Behavior), "bash analysis", time.Since(start))
			return decision, nil
		}
	}

	// Step 7: For file writes/edits — path validation
	if isFileWriteTool(toolName) {
		if decision, handled := e.validateFilePath(ctx, toolName, args, risk); handled {
			if decision.Behavior == types.PermissionAllow {
				e.totalAllowed.Add(1)
			} else {
				e.totalDenied.Add(1)
			}
			e.logCheck(toolName, string(decision.Behavior), "path validation", time.Since(start))
			return decision, nil
		}
	}

	// Step 8: Risk-based decision
	switch risk.Level {
	case ToolRiskSafe:
		// Safe tools are always allowed
		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "safe risk level", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "safe operation — auto-approved",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "classifier",
				Detail: risk.Reason,
			},
		}, nil

	case ToolRiskLow:
		// Low risk — auto-approve
		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "low risk", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "low risk operation — auto-approved",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "classifier",
				Detail: risk.Reason,
			},
		}, nil

	case ToolRiskMedium, ToolRiskHigh, ToolRiskCritical:
		// Need approval
		return e.requestApproval(ctx, toolName, args, risk, start)
	}

	// Default: allow
	e.totalAllowed.Add(1)
	e.logCheck(toolName, "allow", "default", time.Since(start))
	return types.PermissionDecision{
		Behavior: types.PermissionAllow,
		Message:  "allowed by default",
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "default",
			Detail: "no rules matched — allowed by default",
		},
	}, nil
}

// checkModeRestrictions checks if the current mode blocks this tool call.
func (e *Enforcer) checkModeRestrictions(toolName string, risk ToolRisk) (types.PermissionDecision, bool) {
	if e.modes == nil {
		return types.PermissionDecision{}, false
	}

	// Plan mode blocks write/destructive tools
	if e.modes.Current == modes.ModePlan {
		if risk.Level >= ToolRiskMedium || risk.IsWrite || risk.IsDestructive {
			return types.PermissionDecision{
				Behavior: types.PermissionDeny,
				Message:  fmt.Sprintf("plan mode blocks %s — operation is %s", toolName, risk.Category),
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "mode",
					Detail: fmt.Sprintf("plan mode does not allow %s operations", risk.Category),
				},
			}, true
		}
	}

	// Ask mode blocks all tools
	if e.modes.Current == modes.ModeAsk {
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("ask mode blocks all tool use including %s", toolName),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "mode",
				Detail: "ask mode does not allow any tool use",
			},
		}, true
	}

	// Check the modes blocked tools list
	for _, blocked := range e.modes.BlockedTools() {
		if blocked == "*" || matchGlob(blocked, toolName) {
			return types.PermissionDecision{
				Behavior: types.PermissionDeny,
				Message:  fmt.Sprintf("%s is blocked by current mode (%s)", toolName, e.modes.Current),
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "mode",
					Detail: fmt.Sprintf("tool %s blocked in %s mode", toolName, e.modes.Current),
				},
			}, true
		}
	}

	return types.PermissionDecision{}, false
}

// checkSessionDenies checks session-scoped deny rules.
func (e *Enforcer) checkSessionDenies(toolName string, args map[string]any) (types.PermissionDecision, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, rule := range e.sessionDenies {
		if matchGlob(rule.ToolName, toolName) {
			if rule.Pattern == "" || matchesArgs(rule.Pattern, args) {
				return types.PermissionDecision{
					Behavior: types.PermissionDeny,
					Message:  fmt.Sprintf("denied by session rule: %s(%s)", rule.ToolName, rule.Pattern),
					DecisionReason: &types.PermissionDecisionReason{
						Type:   "rule",
						Detail: fmt.Sprintf("session deny: %s(%s)", rule.ToolName, rule.Pattern),
					},
				}, true
			}
		}
	}
	return types.PermissionDecision{}, false
}

// checkSessionAllows checks session-scoped allow rules.
func (e *Enforcer) checkSessionAllows(toolName string, args map[string]any) (types.PermissionDecision, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, rule := range e.sessionAllows {
		if matchGlob(rule.ToolName, toolName) {
			if rule.Pattern == "" || matchesArgs(rule.Pattern, args) {
				return types.PermissionDecision{
					Behavior: types.PermissionAllow,
					Message:  fmt.Sprintf("allowed by session rule: %s(%s)", rule.ToolName, rule.Pattern),
					DecisionReason: &types.PermissionDecisionReason{
						Type:   "rule",
						Detail: fmt.Sprintf("session allow: %s(%s)", rule.ToolName, rule.Pattern),
					},
				}, true
			}
		}
	}
	return types.PermissionDecision{}, false
}

// analyzeBashCommand performs deep analysis on bash commands.
func (e *Enforcer) analyzeBashCommand(ctx context.Context, toolName string, args map[string]any, risk ToolRisk) (types.PermissionDecision, bool) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return types.PermissionDecision{}, false
	}

	// Security validation (injection, obfuscation, etc.)
	secCheck := bash.ValidateCommandSecurity(command)
	if !secCheck.Safe {
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("command blocked by security check: %s", secCheck.Message),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "safetyCheck",
				Detail: secCheck.Message,
			},
		}, true
	}

	// Dangerous pattern detection
	dangerMatches := DetectDangerousPatterns(command)
	maxSeverity := MaxDangerSeverity(dangerMatches)

	if maxSeverity >= SeverityCritical {
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("command blocked — critical danger: %s", dangerMatches[0].Description),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "safetyCheck",
				Detail: fmt.Sprintf("critical danger pattern: %s", dangerMatches[0].Description),
			},
		}, true
	}

	// AST-based analysis
	root := bash.Parse(command)
	if root != nil {
		analysis := bash.AnalyzeCommand(root)
		if len(analysis.Issues) > 0 {
			// Collect all issue messages
			var issueDescs []string
			for _, issue := range analysis.Issues {
				issueDescs = append(issueDescs, fmt.Sprintf("[%s] %s", issue.Type, issue.Message))
			}
			issuesSummary := strings.Join(issueDescs, "; ")

			// High-severity issues block immediately
			for _, issue := range analysis.Issues {
				if issue.Type == "injection" || issue.Type == "privilege" || issue.Type == "exfiltration" {
					return types.PermissionDecision{
						Behavior: types.PermissionDeny,
						Message:  fmt.Sprintf("command blocked by AST analysis: %s", issuesSummary),
						DecisionReason: &types.PermissionDecisionReason{
							Type:   "safetyCheck",
							Detail: issuesSummary,
						},
					}, true
				}
			}
		}

		// Read-only AST commands with no danger patterns are safe
		if analysis.IsReadOnly && maxSeverity == 0 {
			return types.PermissionDecision{
				Behavior: types.PermissionAllow,
				Message:  "read-only command — auto-approved",
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "classifier",
					Detail: "AST analysis confirms read-only operation",
				},
			}, true
		}
	}

	// Command classification
	cls := ClassifyBashCommand(command)

	// Read-only commands are always safe
	if cls.ReadOnly && maxSeverity == 0 {
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  fmt.Sprintf("read-only command (%s) — auto-approved", cls.Category.String()),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "classifier",
				Detail: cls.Reason,
			},
		}, true
	}

	// High danger patterns need approval
	if maxSeverity >= SeverityHigh {
		decision, err := e.requestApproval(ctx, toolName, args, ToolRisk{
			Level:         ToolRiskHigh,
			Category:      "dangerous-command",
			Reason:        fmt.Sprintf("dangerous pattern: %s", dangerMatches[0].Description),
			IsWrite:       !cls.ReadOnly,
			IsDestructive: cls.Destructive,
		}, time.Now())
		if err != nil {
			return types.PermissionDecision{
				Behavior: types.PermissionDeny,
				Message:  "approval failed: " + err.Error(),
			}, true
		}
		return decision, true
	}

	// Not handled — fall through to outer logic
	return types.PermissionDecision{}, false
}

// validateFilePath checks if a file write/edit targets an allowed directory.
func (e *Enforcer) validateFilePath(_ context.Context, toolName string, args map[string]any, _ ToolRisk) (types.PermissionDecision, bool) {
	filePath := extractFilePath(args)
	if filePath == "" {
		return types.PermissionDecision{}, false
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("cannot resolve file path: %s", filePath),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "safetyCheck",
				Detail: "unresolvable file path",
			},
		}, true
	}

	// Check for dangerous files
	if isDangerousFile(absPath) {
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("editing dangerous file blocked: %s", filepath.Base(absPath)),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "safetyCheck",
				Detail: fmt.Sprintf("file %s is on the dangerous files list", filepath.Base(absPath)),
			},
		}, true
	}

	// Check if within project directory
	if e.projectDir != "" {
		absProject, err := filepath.Abs(e.projectDir)
		if err == nil && isPathWithin(absPath, absProject) {
			return types.PermissionDecision{
				Behavior: types.PermissionAllow,
				Message:  "file within project directory — auto-approved",
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "classifier",
					Detail: "file path is within the project directory",
				},
			}, true
		}
	}

	// Outside project — needs review (not hard deny, fall through)
	return types.PermissionDecision{}, false
}

// requestApproval prompts the user for approval if an AskApprovalFn is set.
func (e *Enforcer) requestApproval(ctx context.Context, toolName string, args map[string]any, risk ToolRisk, start time.Time) (types.PermissionDecision, error) {
	e.totalAsked.Add(1)

	if e.askFn == nil {
		// No approval handler — deny by default for medium+ risk
		e.totalDenied.Add(1)
		e.logCheck(toolName, "deny", "no approval handler", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  fmt.Sprintf("no approval handler configured — %s requires approval (risk: %s)", toolName, risk.Level),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "no approval handler available",
			},
		}, nil
	}

	// Build explanation
	var explanation *PermissionExplanation
	if isBashTool(toolName) {
		if cmd, ok := args["command"].(string); ok {
			explainer := NewPermissionExplainer(e.logger)
			expl := explainer.ExplainBashCommand(cmd)
			explanation = &expl
		}
	} else {
		explainer := NewPermissionExplainer(e.logger)
		expl := explainer.ExplainToolUse(toolName, args)
		explanation = &expl
	}

	req := ApprovalRequest{
		ToolName:    toolName,
		Args:        args,
		Risk:        risk,
		Reason:      risk.Reason,
		Explanation: explanation,
	}

	resp, err := e.askFn(ctx, req)
	if err != nil {
		e.totalDenied.Add(1)
		e.logCheck(toolName, "deny", "approval error", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  "approval failed: " + err.Error(),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: err.Error(),
			},
		}, nil
	}

	if resp.Abort {
		e.totalDenied.Add(1)
		e.logCheck(toolName, "deny", "user abort", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  "user aborted the operation",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "user abort",
			},
		}, nil
	}

	if resp.Allowed {
		// If user chose "always allow", add a session rule
		if resp.Permanent {
			e.AddSessionAllow(toolName, "")
		}

		// Remember this specific approval in the SessionStore so the same
		// (tool, args) combination is not re-prompted during this session.
		if e.store != nil {
			e.store.Add(SessionRule{
				ToolName: toolName,
				ArgsHash: HashArgs(args),
				Decision: "allow",
				Reason:   "user approved",
			})
		}

		e.totalAllowed.Add(1)
		e.logCheck(toolName, "allow", "user approved", time.Since(start))
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "approved by user",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "user approved",
			},
		}, nil
	}

	// Denied
	e.totalDenied.Add(1)
	message := "permission denied by user"
	if resp.Feedback != "" {
		message = "permission denied: " + resp.Feedback
	}
	e.logCheck(toolName, "deny", "user denied", time.Since(start))
	return types.PermissionDecision{
		Behavior: types.PermissionDeny,
		Message:  message,
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "other",
			Detail: "user denied",
		},
	}, nil
}

// AddSessionAllow adds a session-scoped allow rule.
func (e *Enforcer) AddSessionAllow(toolName string, pattern string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionAllows = append(e.sessionAllows, sessionRule{
		ToolName: toolName,
		Pattern:  pattern,
	})
	e.logger.Debug("session allow added", "tool", toolName, "pattern", pattern)
}

// AddSessionDeny adds a session-scoped deny rule.
func (e *Enforcer) AddSessionDeny(toolName string, pattern string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionDenies = append(e.sessionDenies, sessionRule{
		ToolName: toolName,
		Pattern:  pattern,
	})
	e.logger.Debug("session deny added", "tool", toolName, "pattern", pattern)
}

// SetYOLO enables or disables YOLO mode (auto-approve everything).
func (e *Enforcer) SetYOLO(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.yolo = enabled
	e.logger.Info("YOLO mode changed", "enabled", enabled)
}

// isYOLO returns whether YOLO mode is active.
func (e *Enforcer) isYOLO() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.yolo
}

// SetAskFn sets the approval callback function.
func (e *Enforcer) SetAskFn(fn AskApprovalFn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.askFn = fn
}

// Stats returns permission check statistics.
func (e *Enforcer) Stats() map[string]any {
	e.mu.RLock()
	sessionAllows := len(e.sessionAllows)
	sessionDenies := len(e.sessionDenies)
	yolo := e.yolo
	e.mu.RUnlock()

	return map[string]any{
		"total_checks":   e.totalChecks.Load(),
		"total_allowed":  e.totalAllowed.Load(),
		"total_denied":   e.totalDenied.Load(),
		"total_asked":    e.totalAsked.Load(),
		"total_yolo":     e.totalYoloAllow.Load(),
		"session_allows": sessionAllows,
		"session_denies": sessionDenies,
		"yolo_mode":      yolo,
	}
}

// logCheck logs a permission check result.
func (e *Enforcer) logCheck(toolName, decision, reason string, duration time.Duration) {
	e.logger.Debug("enforcer check",
		"tool", toolName,
		"decision", decision,
		"reason", reason,
		"duration_ms", duration.Milliseconds(),
	)
}

// checkStoreApproval consults the SessionStore for a prior decision on the
// exact same (toolName, argsHash) pair. Returns the cached decision and true
// when a stored rule is found; returns an empty decision and false otherwise.
func (e *Enforcer) checkStoreApproval(toolName string, args map[string]any) (types.PermissionDecision, bool) {
	if e.store == nil {
		return types.PermissionDecision{}, false
	}
	hash := HashArgs(args)
	rule, found := e.store.Lookup(toolName, hash)
	if !found {
		return types.PermissionDecision{}, false
	}
	switch rule.Decision {
	case "allow":
		return types.PermissionDecision{
			Behavior: types.PermissionAllow,
			Message:  "auto-approved by session store (prior approval)",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "rule",
				Detail: fmt.Sprintf("session store: prior approval for %s", toolName),
			},
		}, true
	case "deny":
		return types.PermissionDecision{
			Behavior: types.PermissionDeny,
			Message:  "denied by session store (prior denial)",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "rule",
				Detail: fmt.Sprintf("session store: prior denial for %s", toolName),
			},
		}, true
	}
	// "ask" or unknown — fall through so the user is prompted again.
	return types.PermissionDecision{}, false
}

// checkMatcherAllow checks the CompiledMatcher for a pattern-based allow rule
// matching the given tool call. Returns an allow decision and true on a match;
// returns an empty decision and false when there is no matcher or no match.
func (e *Enforcer) checkMatcherAllow(toolName string, args map[string]any) (types.PermissionDecision, bool) {
	if e.matcher == nil || e.matcher.Len() == 0 {
		return types.PermissionDecision{}, false
	}
	if !e.matcher.Match(toolName, args) {
		return types.PermissionDecision{}, false
	}
	return types.PermissionDecision{
		Behavior: types.PermissionAllow,
		Message:  fmt.Sprintf("allowed by compiled matcher rule for %s", toolName),
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "rule",
			Detail: fmt.Sprintf("compiled matcher: pattern matched %s", toolName),
		},
	}, true
}

// --- Helpers ---

// isBashTool returns true if the tool name is a bash/command execution tool.
func isBashTool(toolName string) bool {
	switch toolName {
	case "Bash", "bash", "run_command", "RunCommand", "Shell":
		return true
	}
	return false
}

// isFileWriteTool returns true if the tool name is a file write/edit tool.
func isFileWriteTool(toolName string) bool {
	switch toolName {
	case "Write", "Edit", "MultiEdit", "FileEdit",
		"write_file", "edit_file", "create_file", "patch_file":
		return true
	}
	return false
}

// extractFilePath gets the file path from tool arguments.
func extractFilePath(args map[string]any) string {
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["filePath"].(string); ok && p != "" {
		return p
	}
	return ""
}

// matchesArgs checks if a pattern matches the tool arguments.
// For bash tools, matches against the command; for file tools, matches against the path.
func matchesArgs(pattern string, args map[string]any) bool {
	// Try command match
	if cmd, ok := args["command"].(string); ok {
		rule := ParseShellPermissionRule(pattern)
		if MatchRule(rule, cmd) {
			return true
		}
	}

	// Try path match
	filePath := extractFilePath(args)
	if filePath != "" {
		if matchGlob(pattern, filePath) {
			return true
		}
		// Also try prefix matching for directory patterns
		if strings.HasSuffix(pattern, "/") && strings.HasPrefix(filePath, pattern) {
			return true
		}
	}

	return false
}
