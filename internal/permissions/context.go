package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// PermissionDecisionSource describes where a permission decision came from.
type PermissionDecisionSource struct {
	Type      string // "hook", "user", "classifier", "rule", "mode"
	Permanent bool   // whether this was a persistent (always allow) decision
	HookName  string // for hook-type sources
}

// PermissionApproval is the result of approving a permission request.
type PermissionApproval struct {
	UpdatedInput map[string]interface{}
	UserModified bool
	Reason       *types.PermissionDecisionReason
	Feedback     string
	PermUpdates  []PermissionUpdate
}

// PermissionUpdate represents a change to the permission configuration.
type PermissionUpdate struct {
	Type        string // "addRules", "replaceRules", "removeRules", "setMode", "addDirectories", "removeDirectories"
	Destination string // "userSettings", "projectSettings", "localSettings", "session", "cliArg"
	Rules       []types.PermissionRule
	Behavior    string   // "allow", "deny", "ask"
	Mode        string   // for setMode
	Directories []string // for addDirectories/removeDirectories
}

// PermissionRequestHandler is a callback for handling interactive permission requests.
// Returns true for allow, false for deny. The handler may modify updatedInput.
type PermissionRequestHandler func(ctx context.Context, req PermissionRequest) (PermissionResponse, error)

// PermissionRequest describes a permission request to the user.
type PermissionRequest struct {
	ToolName    string
	ToolInput   map[string]interface{}
	ToolUseID   string
	Message     string
	Reason      *types.PermissionDecisionReason
	Suggestions []PermissionUpdate
	Explanation *PermissionExplanation
}

// PermissionResponse is the user's response to a permission request.
type PermissionResponse struct {
	Allowed      bool
	UpdatedInput map[string]interface{}
	Feedback     string
	Permanent    bool // "always allow"
	PermUpdates  []PermissionUpdate
	Abort        bool // user wants to abort entirely
}

// PermissionContext manages the full permission decision flow for tool use.
// It coordinates between rules, classifiers, hooks, and user interaction.
type PermissionContext struct {
	mu             sync.RWMutex
	logger         *slog.Logger
	manager        *Manager
	loader         *PermissionLoader
	yoloClassifier *YoloClassifier
	denialTracker  *DenialTracker
	autoModeState  *AutoModeState
	explainer      *PermissionExplainer

	// Handler for interactive permission prompts
	requestHandler PermissionRequestHandler

	// Mode and configuration
	mode          types.PermissionMode
	isHeadless    bool   // true for background agents that can't show prompts
	isCoordinator bool   // true for coordinator agents
	isSwarmWorker bool   // true for swarm worker agents
	agentID       string // ID of the agent this context belongs to
	projectDir    string

	// Hooks
	preToolHooks  []PreToolHook
	postToolHooks []PostToolHook
}

// PreToolHook is called before a tool use permission decision.
type PreToolHook func(ctx context.Context, toolName string, input map[string]interface{}) (*types.PermissionDecision, error)

// PostToolHook is called after a tool use permission decision.
type PostToolHook func(ctx context.Context, toolName string, decision types.PermissionDecision)

// PermissionContextConfig holds configuration for creating a PermissionContext.
type PermissionContextConfig struct {
	Logger         *slog.Logger
	Manager        *Manager
	Loader         *PermissionLoader
	ProjectDir     string
	IsHeadless     bool
	IsCoordinator  bool
	IsSwarmWorker  bool
	AgentID        string
	RequestHandler PermissionRequestHandler
}

// NewPermissionContext creates a new PermissionContext with the given configuration.
func NewPermissionContext(cfg PermissionContextConfig) *PermissionContext {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	manager := cfg.Manager
	if manager == nil {
		manager = NewManager(logger)
	}

	loader := cfg.Loader
	if loader == nil {
		loader = NewPermissionLoader(logger, cfg.ProjectDir)
	}

	return &PermissionContext{
		logger:         logger,
		manager:        manager,
		loader:         loader,
		yoloClassifier: NewYoloClassifier(logger, cfg.ProjectDir),
		denialTracker:  NewDenialTracker(),
		autoModeState:  NewAutoModeState(),
		explainer:      NewPermissionExplainer(logger),
		requestHandler: cfg.RequestHandler,
		mode:           types.ModeDefault,
		isHeadless:     cfg.IsHeadless,
		isCoordinator:  cfg.IsCoordinator,
		isSwarmWorker:  cfg.IsSwarmWorker,
		agentID:        cfg.AgentID,
		projectDir:     cfg.ProjectDir,
	}
}

// HandlePermissionDecision is the main entry point for permission decisions.
// It evaluates rules, classifiers, hooks, and user interaction to produce a final decision.
func (pc *PermissionContext) HandlePermissionDecision(
	ctx context.Context,
	toolName string,
	input map[string]interface{},
	toolUseID string,
) (types.PermissionDecision, error) {
	start := time.Now()

	// Step 1: Run pre-tool hooks
	for _, hook := range pc.preToolHooks {
		hookDecision, err := hook(ctx, toolName, input)
		if err != nil {
			pc.logger.Warn("pre-tool hook error", "error", err, "tool", toolName)
			continue
		}
		if hookDecision != nil {
			pc.logDecision(toolName, *hookDecision, time.Since(start))
			return *hookDecision, nil
		}
	}

	// Step 2: Check deny rules (highest priority)
	decision := pc.checkDenyRules(toolName, input)
	if decision != nil {
		pc.logDecision(toolName, *decision, time.Since(start))
		return *decision, nil
	}

	// Step 3: Check ask rules (tool-wide)
	askDecision := pc.checkAskRules(toolName, input)

	// Step 4: Check bypass mode
	pc.mu.RLock()
	currentMode := pc.mode
	pc.mu.RUnlock()

	if currentMode == types.ModeBypass {
		result := types.PermissionDecision{
			Behavior: "allow",
			Message:  "bypass mode active",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "mode",
				Detail: "bypass mode — all permissions granted",
			},
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	// Step 5: Check tool-wide allow rules
	allowDecision := pc.checkAllowRules(toolName, input)
	if allowDecision != nil && askDecision == nil {
		pc.logDecision(toolName, *allowDecision, time.Since(start))
		return *allowDecision, nil
	}

	// Step 6: Auto mode / YOLO classifier
	if currentMode == types.PermissionMode("auto") || pc.autoModeState.IsActive() {
		autoDecision, err := pc.handleAutoMode(ctx, toolName, input, toolUseID)
		if err != nil {
			pc.logger.Warn("auto mode error", "error", err, "tool", toolName)
		}
		if autoDecision != nil {
			pc.logDecision(toolName, *autoDecision, time.Since(start))
			return *autoDecision, nil
		}
	}

	// Step 7: Don't ask mode
	if currentMode == types.ModeDontAsk {
		if askDecision != nil {
			result := types.PermissionDecision{
				Behavior: "deny",
				Message:  fmt.Sprintf("Tool %s requires permission, but don't-ask mode auto-denies all permission requests.", toolName),
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "mode",
					Detail: "don't-ask mode — permission requests are auto-denied",
				},
			}
			pc.logDecision(toolName, result, time.Since(start))
			return result, nil
		}
	}

	// Step 8: If we have an ask decision (from rule or default), handle it
	if askDecision != nil {
		return pc.handleAskDecision(ctx, toolName, input, toolUseID, askDecision, start)
	}

	// Step 9: Default behavior — allow (no rules matched)
	result := types.PermissionDecision{
		Behavior: "allow",
		Message:  "allowed by default",
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "default",
			Detail: "no rules matched — allowed by default",
		},
	}
	pc.logDecision(toolName, result, time.Since(start))
	return result, nil
}

// handleAutoMode processes a tool call through the auto mode classifier.
func (pc *PermissionContext) handleAutoMode(
	ctx context.Context,
	toolName string,
	input map[string]interface{},
	toolUseID string,
) (*types.PermissionDecision, error) {
	// Check if the tool is on the safe allowlist
	if IsAutoModeAllowlistedTool(toolName) {
		pc.denialTracker.RecordSuccess()
		pc.autoModeState.RecordApproval()
		return &types.PermissionDecision{
			Behavior: "allow",
			Message:  "safe tool — auto-approved",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "classifier",
				Detail: "tool is on the safe allowlist",
			},
		}, nil
	}

	// Run the YOLO classifier
	classification := pc.yoloClassifier.ClassifyForAutoApprove(toolName, input)

	if !classification.ShouldBlock {
		pc.denialTracker.RecordSuccess()
		pc.autoModeState.RecordApproval()
		return &types.PermissionDecision{
			Behavior: "allow",
			Message:  classification.Reason,
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "classifier",
				Detail: classification.Reason,
			},
		}, nil
	}

	// Classifier blocked the action
	denialState := pc.denialTracker.RecordDenial()
	pc.autoModeState.RecordAutoDenial()

	pc.logger.Debug("auto mode classifier blocked action",
		"tool", toolName,
		"reason", classification.Reason,
		"consecutive_denials", denialState.ConsecutiveDenials,
		"total_denials", denialState.TotalDenials,
	)

	// Check denial limits
	if denialState.ShouldFallbackToPrompting() {
		// Headless mode: abort
		if pc.isHeadless {
			return nil, fmt.Errorf("agent aborted: too many classifier denials in headless mode")
		}

		// Interactive mode: fall through to prompting
		pc.logger.Warn("classifier denial limit exceeded, falling back to prompting",
			"consecutive", denialState.ConsecutiveDenials,
			"total", denialState.TotalDenials,
		)

		if denialState.HitTotalLimit() {
			pc.denialTracker.ResetTotal()
		}

		return nil, nil // Fall through to interactive prompting
	}

	// Return denial
	return &types.PermissionDecision{
		Behavior: "deny",
		Message:  BuildYoloRejectionMessage(classification.Reason),
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "classifier",
			Detail: classification.Reason,
		},
	}, nil
}

// handleAskDecision handles the interactive permission prompt flow.
func (pc *PermissionContext) handleAskDecision(
	ctx context.Context,
	toolName string,
	input map[string]interface{},
	toolUseID string,
	askDecision *types.PermissionDecision,
	start time.Time,
) (types.PermissionDecision, error) {
	// Headless mode: auto-deny
	if pc.isHeadless {
		result := types.PermissionDecision{
			Behavior: "deny",
			Message:  fmt.Sprintf("Tool %s requires permission, but permission prompts are not available in this context.", toolName),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "asyncAgent",
				Detail: "Permission prompts are not available in this context",
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	// Swarm worker mode: bubble up to coordinator
	if pc.isSwarmWorker {
		result := types.PermissionDecision{
			Behavior: "ask",
			Message:  askDecision.Message,
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "asyncAgent",
				Detail: "Swarm worker — permission request bubbled to coordinator",
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	// Interactive mode: ask the user
	if pc.requestHandler == nil {
		// No handler configured — deny
		result := types.PermissionDecision{
			Behavior: "deny",
			Message:  "No permission request handler configured",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "No permission request handler available",
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	// Generate explanation if enabled
	var explanation *PermissionExplanation
	if pc.explainer.IsEnabled() {
		expl := pc.explainer.ExplainToolUse(toolName, input)
		explanation = &expl
	}

	// Build the permission request
	req := PermissionRequest{
		ToolName:    toolName,
		ToolInput:   input,
		ToolUseID:   toolUseID,
		Message:     askDecision.Message,
		Reason:      askDecision.DecisionReason,
		Explanation: explanation,
	}

	// Ask the user
	resp, err := pc.requestHandler(ctx, req)
	if err != nil {
		pc.logger.Warn("permission request handler error", "error", err)
		result := types.PermissionDecision{
			Behavior: "deny",
			Message:  "Permission request failed: " + err.Error(),
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: err.Error(),
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	if resp.Abort {
		result := types.PermissionDecision{
			Behavior: "deny",
			Message:  "User aborted the operation",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "User abort",
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	if resp.Allowed {
		// Apply permission updates if any
		if len(resp.PermUpdates) > 0 {
			pc.applyPermissionUpdates(resp.PermUpdates)
		}

		result := types.PermissionDecision{
			Behavior: "allow",
			Message:  "approved by user",
			DecisionReason: &types.PermissionDecisionReason{
				Type:   "other",
				Detail: "User approved",
			},
			ToolUseID: toolUseID,
		}
		pc.logDecision(toolName, result, time.Since(start))
		return result, nil
	}

	// User denied
	message := "Permission denied by user"
	if resp.Feedback != "" {
		message = "Permission denied: " + resp.Feedback
	}

	result := types.PermissionDecision{
		Behavior: "deny",
		Message:  message,
		DecisionReason: &types.PermissionDecisionReason{
			Type:   "other",
			Detail: "User denied",
		},
		ToolUseID: toolUseID,
	}
	pc.logDecision(toolName, result, time.Since(start))
	return result, nil
}

// checkDenyRules checks for deny rules matching the tool.
func (pc *PermissionContext) checkDenyRules(toolName string, input map[string]interface{}) *types.PermissionDecision {
	ruleSet := pc.loader.GetRuleSet()
	denyRules := ruleSet.GetAllRules("deny")

	for _, rule := range denyRules {
		if MatchToolRule(rule, toolName) {
			return &types.PermissionDecision{
				Behavior: "deny",
				Message:  fmt.Sprintf("Permission to use %s has been denied.", toolName),
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "rule",
					Detail: fmt.Sprintf("deny rule from %s", rule.Source),
					Rule:   &rule,
				},
			}
		}
	}
	return nil
}

// checkAskRules checks for ask rules matching the tool.
func (pc *PermissionContext) checkAskRules(toolName string, input map[string]interface{}) *types.PermissionDecision {
	ruleSet := pc.loader.GetRuleSet()
	askRules := ruleSet.GetAllRules("ask")

	for _, rule := range askRules {
		if MatchToolRule(rule, toolName) {
			return &types.PermissionDecision{
				Behavior: "ask",
				Message:  BuildPermissionRequestMessage(toolName, nil),
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "rule",
					Detail: fmt.Sprintf("ask rule from %s", rule.Source),
					Rule:   &rule,
				},
			}
		}
	}
	return nil
}

// checkAllowRules checks for allow rules matching the tool.
func (pc *PermissionContext) checkAllowRules(toolName string, input map[string]interface{}) *types.PermissionDecision {
	ruleSet := pc.loader.GetRuleSet()
	allowRules := ruleSet.GetAllRules("allow")

	for _, rule := range allowRules {
		if MatchToolRule(rule, toolName) {
			return &types.PermissionDecision{
				Behavior: "allow",
				Message:  "allowed by rule",
				DecisionReason: &types.PermissionDecisionReason{
					Type:   "rule",
					Detail: fmt.Sprintf("allow rule from %s", rule.Source),
					Rule:   &rule,
				},
			}
		}
	}
	return nil
}

// applyPermissionUpdates applies permission updates from user responses.
func (pc *PermissionContext) applyPermissionUpdates(updates []PermissionUpdate) {
	for _, update := range updates {
		switch update.Type {
		case "addRules":
			for _, rule := range update.Rules {
				rule.Source = types.PermissionRuleSource(update.Destination)
				rule.Behavior = types.PermissionBehavior(update.Behavior)
				pc.loader.MergePermissions([]types.PermissionRule{rule})
			}
		case "setMode":
			if update.Mode != "" {
				pc.SetMode(types.PermissionMode(update.Mode))
			}
		case "addDirectories":
			for _, dir := range update.Directories {
				pc.yoloClassifier.AddAllowedDir(dir)
			}
		}
	}
}

// SetMode sets the permission mode for this context.
func (pc *PermissionContext) SetMode(mode types.PermissionMode) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.mode = mode
	pc.manager.SetMode(mode)
	pc.logger.Info("permission mode changed", "mode", string(mode))
}

// Mode returns the current permission mode.
func (pc *PermissionContext) Mode() types.PermissionMode {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.mode
}

// SetRequestHandler sets the callback for interactive permission requests.
func (pc *PermissionContext) SetRequestHandler(handler PermissionRequestHandler) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.requestHandler = handler
}

// AddPreToolHook adds a hook that runs before permission decisions.
func (pc *PermissionContext) AddPreToolHook(hook PreToolHook) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.preToolHooks = append(pc.preToolHooks, hook)
}

// AddPostToolHook adds a hook that runs after permission decisions.
func (pc *PermissionContext) AddPostToolHook(hook PostToolHook) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.postToolHooks = append(pc.postToolHooks, hook)
}

// DenialTracker returns the denial tracker.
func (pc *PermissionContext) DenialTracker() *DenialTracker {
	return pc.denialTracker
}

// AutoModeState returns the auto mode state.
func (pc *PermissionContext) AutoModeState() *AutoModeState {
	return pc.autoModeState
}

// Explainer returns the permission explainer.
func (pc *PermissionContext) Explainer() *PermissionExplainer {
	return pc.explainer
}

// Manager returns the underlying permission manager.
func (pc *PermissionContext) Manager() *Manager {
	return pc.manager
}

// Loader returns the permission loader.
func (pc *PermissionContext) Loader() *PermissionLoader {
	return pc.loader
}

// logDecision logs a permission decision for telemetry/debugging.
func (pc *PermissionContext) logDecision(toolName string, decision types.PermissionDecision, duration time.Duration) {
	reasonType := "unknown"
	reasonDetail := ""
	if decision.DecisionReason != nil {
		reasonType = decision.DecisionReason.Type
		reasonDetail = decision.DecisionReason.Detail
	}

	pc.logger.Debug("permission decision",
		"tool", toolName,
		"behavior", string(decision.Behavior),
		"reason_type", reasonType,
		"reason", reasonDetail,
		"duration_ms", duration.Milliseconds(),
	)

	// Run post-tool hooks
	pc.mu.RLock()
	hooks := pc.postToolHooks
	pc.mu.RUnlock()

	for _, hook := range hooks {
		hook(context.Background(), toolName, decision)
	}
}
