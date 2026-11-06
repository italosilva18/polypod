package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// --- Shell Hook Executor ---

// ShellHookInput holds environment variables and input for a shell hook.
type ShellHookInput struct {
	// Standard env vars passed to all hooks
	HookEventName string
	HookMatcher   string
	ToolName      string
	ToolInput     string // JSON-encoded tool input
	ToolUseID     string
	SessionID     string
	WorkingDir    string
	PluginRoot    string // CLAUDE_PLUGIN_ROOT for skill hooks

	// The JSON payload piped to stdin
	JSONInput string

	// Custom env vars from the hook config
	ExtraEnv map[string]string
}

// ShellHookOutput holds the captured output from a shell hook.
type ShellHookOutput struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecShellHook runs a shell command hook with environment variables and JSON stdin.
// It respects the context for cancellation/timeout.
//
// Exit code semantics (matching Claude Code):
//
//	0 - success; stdout may contain JSON response or plain text
//	2 - blocking error; stderr shown to model, tool call blocked
//	other - non-blocking error; stderr shown to user only
func ExecShellHook(
	ctx context.Context,
	hook HookCommand,
	input ShellHookInput,
	logger *slog.Logger,
) (*ShellHookOutput, error) {
	// Determine shell and args
	shell := "bash"
	shellArgs := []string{"-c", hook.Command}
	if hook.Shell == ShellPowerShell {
		shell = "pwsh"
		shellArgs = []string{"-Command", hook.Command}
	}

	// Determine timeout
	timeout := time.Duration(HookTimeouts.ShellDefault) * time.Second
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, shell, shellArgs...)

	// Set up stdin with JSON input
	if input.JSONInput != "" {
		cmd.Stdin = strings.NewReader(input.JSONInput)
	}

	// Build environment
	cmd.Env = buildShellEnv(input)

	// Set working directory
	if input.WorkingDir != "" {
		cmd.Dir = input.WorkingDir
	}

	// Capture stdout and stderr separately
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	logger.Debug("executing shell hook",
		"command", hook.Command,
		"shell", shell,
		"timeout", timeout,
	)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() != nil {
			// Context cancelled or timed out
			return &ShellHookOutput{
				Stdout:   stdoutBuf.String(),
				Stderr:   stderrBuf.String(),
				ExitCode: -1,
			}, execCtx.Err()
		} else {
			return nil, fmt.Errorf("shell hook execution failed: %w", err)
		}
	}

	return &ShellHookOutput{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
	}, nil
}

// buildShellEnv constructs the environment variables for a shell hook.
func buildShellEnv(input ShellHookInput) []string {
	env := os.Environ()
	add := func(key, value string) {
		if value != "" {
			env = append(env, key+"="+value)
		}
	}

	add("HOOK_EVENT_NAME", input.HookEventName)
	add("HOOK_MATCHER", input.HookMatcher)
	add("TOOL_NAME", input.ToolName)
	add("TOOL_INPUT", input.ToolInput)
	add("TOOL_USE_ID", input.ToolUseID)
	add("SESSION_ID", input.SessionID)
	add("CLAUDE_PLUGIN_ROOT", input.PluginRoot)

	for k, v := range input.ExtraEnv {
		add(k, v)
	}

	return env
}

// InterpretShellResult converts a ShellHookOutput into a HookResult.
// Applies Claude Code's exit code semantics.
func InterpretShellResult(
	hook *HookCommand,
	hookName string,
	hookEvent HookEvent,
	output *ShellHookOutput,
	toolUseID string,
) *HookResult {
	result := &HookResult{
		Hook:     hook,
		Stdout:   output.Stdout,
		Stderr:   output.Stderr,
		ExitCode: &output.ExitCode,
	}

	switch output.ExitCode {
	case 0:
		// Success — try to parse JSON from stdout
		result.Outcome = HookOutcomeSuccess
		parseJSONResponse(output.Stdout, result)

	case 2:
		// Blocking error — stderr shown to model, blocks the action
		result.Outcome = HookOutcomeBlocking
		result.BlockingError = &HookBlockingError{
			BlockingError: strings.TrimSpace(output.Stderr),
			Command:       hook.Command,
		}

	default:
		// Non-blocking error — stderr shown to user only
		result.Outcome = HookOutcomeNonBlockingError
	}

	return result
}

// parseJSONResponse attempts to parse structured JSON from hook stdout.
func parseJSONResponse(stdout string, result *HookResult) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return
	}

	// Try to find and parse the first JSON line
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var output SyncHookJSONOutput
		if err := json.Unmarshal([]byte(line), &output); err != nil {
			continue
		}

		// Apply structured output fields
		if output.Continue != nil && !*output.Continue {
			result.Outcome = HookOutcomeBlocking
			result.StopReason = output.StopReason
			result.PreventContinuation = true
		}
		if output.Decision == "block" || output.Decision == "deny" {
			result.Outcome = HookOutcomeBlocking
			result.BlockingError = &HookBlockingError{
				BlockingError: output.Reason,
			}
		}
		if output.SystemMessage != "" {
			result.Message = output.SystemMessage
		}
		if output.HookSpecificOutput != nil {
			applyHookSpecificOutput(output.HookSpecificOutput, result)
		}
		return
	}
}

// applyHookSpecificOutput applies event-specific output fields.
func applyHookSpecificOutput(specific map[string]interface{}, result *HookResult) {
	if ac, ok := specific["additionalContext"].(string); ok {
		result.AdditionalContext = ac
	}
	if ium, ok := specific["initialUserMessage"].(string); ok {
		result.InitialUserMessage = ium
	}
	if pd, ok := specific["permissionDecision"].(string); ok {
		result.PermissionBehavior = pd
	}
	if pdr, ok := specific["permissionDecisionReason"].(string); ok {
		result.HookPermissionDecisionReason = pdr
	}
	if ui, ok := specific["updatedInput"].(map[string]interface{}); ok {
		result.UpdatedInput = ui
	}
	if retry, ok := specific["retry"].(bool); ok {
		result.Retry = retry
	}

	// PermissionRequest decision
	if decision, ok := specific["decision"].(map[string]interface{}); ok {
		prr := &PermissionRequestResult{}
		if behavior, ok := decision["behavior"].(string); ok {
			prr.Behavior = behavior
		}
		if msg, ok := decision["message"].(string); ok {
			prr.Message = msg
		}
		if interrupt, ok := decision["interrupt"].(bool); ok {
			prr.Interrupt = interrupt
		}
		if ui, ok := decision["updatedInput"].(map[string]interface{}); ok {
			prr.UpdatedInput = ui
		}
		result.PermissionRequestResult = prr
	}

	// WorktreeCreate
	if wp, ok := specific["worktreePath"].(string); ok {
		// Store in AdditionalContext for the caller to extract
		result.AdditionalContext = wp
	}

	// Watch paths
	if wps, ok := specific["watchPaths"].([]interface{}); ok {
		paths := make([]string, 0, len(wps))
		for _, p := range wps {
			if s, ok := p.(string); ok {
				paths = append(paths, s)
			}
		}
		if len(paths) > 0 {
			result.AdditionalContext = strings.Join(paths, "\n")
		}
	}
}

// --- HTTP Hook Executor ---

// HTTPHookResult holds the response from an HTTP hook.
type HTTPHookResult struct {
	OK         bool
	StatusCode int
	Body       string
	Error      string
	Aborted    bool
}

// ExecHTTPHook executes an HTTP hook by POSTing the hook input JSON to the
// configured URL. Applies SSRF protection, header env var interpolation,
// and URL allowlist enforcement.
func ExecHTTPHook(
	ctx context.Context,
	hook HookCommand,
	hookEvent HookEvent,
	jsonInput string,
	allowlist *DomainAllowlist,
	env map[string]string,
	logger *slog.Logger,
) *HTTPHookResult {
	// Enforce URL allowlist before any I/O
	if allowlist != nil && !allowlist.IsAllowed(hook.URL) {
		msg := fmt.Sprintf("HTTP hook blocked: %s does not match any pattern in allowedHttpHookUrls", hook.URL)
		logger.Warn(msg)
		return &HTTPHookResult{OK: false, Error: msg}
	}

	// Determine timeout
	timeout := time.Duration(HookTimeouts.HTTPDefault) * time.Second
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build headers with env var interpolation
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if hook.Headers != nil {
		allowedVars := make(map[string]bool)
		for _, v := range hook.AllowedEnvVars {
			allowedVars[v] = true
		}
		for name, value := range hook.Headers {
			headers[name] = InterpolateEnvVars(value, env, allowedVars)
		}
	}

	logger.Debug("executing HTTP hook",
		"url", hook.URL,
		"timeout", timeout,
	)

	// SSRF-guarded HTTP client
	client := buildSSRFGuardedClient(timeout)

	req, err := http.NewRequestWithContext(execCtx, "POST", hook.URL, strings.NewReader(jsonInput))
	if err != nil {
		return &HTTPHookResult{OK: false, Error: fmt.Sprintf("failed to create request: %v", err)}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		if execCtx.Err() != nil {
			return &HTTPHookResult{OK: false, Aborted: true}
		}
		return &HTTPHookResult{OK: false, Error: fmt.Sprintf("HTTP hook error: %v", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &HTTPHookResult{
			OK:         resp.StatusCode >= 200 && resp.StatusCode < 300,
			StatusCode: resp.StatusCode,
			Error:      fmt.Sprintf("failed to read response body: %v", err),
		}
	}

	logger.Debug("HTTP hook response",
		"status", resp.StatusCode,
		"body_length", len(body),
	)

	return &HTTPHookResult{
		OK:         resp.StatusCode >= 200 && resp.StatusCode < 300,
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

// buildSSRFGuardedClient creates an HTTP client with SSRF protection.
func buildSSRFGuardedClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	transport := &http.Transport{
		DialContext:            SSRFGuardedDialer(dialer),
		MaxIdleConns:           10,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 1 << 20, // 1MB
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Block redirects (matching Claude Code's maxRedirects: 0)
			return http.ErrUseLastResponse
		},
	}
}

// InterpretHTTPResult converts an HTTPHookResult into a HookResult.
func InterpretHTTPResult(
	hook *HookCommand,
	hookName string,
	hookEvent HookEvent,
	httpResult *HTTPHookResult,
	toolUseID string,
) *HookResult {
	result := &HookResult{
		Hook: hook,
	}

	if httpResult.Aborted {
		result.Outcome = HookOutcomeCancelled
		return result
	}

	if !httpResult.OK {
		result.Outcome = HookOutcomeNonBlockingError
		result.Stderr = httpResult.Error
		return result
	}

	result.Outcome = HookOutcomeSuccess
	result.Stdout = httpResult.Body

	// Try to parse JSON response
	if httpResult.Body != "" {
		parseJSONResponse(httpResult.Body, result)
	}

	return result
}

// --- Prompt Hook Executor ---

// PromptHookInput holds the context for a prompt hook execution.
type PromptHookInput struct {
	Prompt    string
	JSONInput string
	Model     string
	Timeout   time.Duration
}

// PromptHookExecutor is the interface for LLM-based prompt hook execution.
// The actual implementation depends on the AI client, so this is an interface
// that gets injected.
type PromptHookExecutor interface {
	ExecPromptHook(ctx context.Context, input PromptHookInput) (*PromptHookResponse, error)
}

// PromptHookResponse is the LLM's evaluation of a prompt hook.
type PromptHookResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// ExecPromptHook executes a prompt-based hook using an LLM.
// The prompt is evaluated against the hook input, and the LLM returns
// a structured {ok: bool, reason?: string} response.
func ExecPromptHook(
	ctx context.Context,
	hook HookCommand,
	hookName string,
	hookEvent HookEvent,
	jsonInput string,
	executor PromptHookExecutor,
	toolUseID string,
	logger *slog.Logger,
) *HookResult {
	result := &HookResult{Hook: &hook}

	// Replace $ARGUMENTS with the JSON input
	processedPrompt := SubstituteArguments(hook.Prompt, jsonInput)

	timeout := time.Duration(HookTimeouts.PromptDefault) * time.Second
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.Debug("executing prompt hook",
		"prompt", processedPrompt,
		"model", hook.Model,
	)

	resp, err := executor.ExecPromptHook(execCtx, PromptHookInput{
		Prompt:    processedPrompt,
		JSONInput: jsonInput,
		Model:     hook.Model,
		Timeout:   timeout,
	})

	if err != nil {
		if execCtx.Err() != nil {
			result.Outcome = HookOutcomeCancelled
			return result
		}
		logger.Error("prompt hook error", "error", err)
		result.Outcome = HookOutcomeNonBlockingError
		result.Stderr = fmt.Sprintf("Error executing prompt hook: %v", err)
		return result
	}

	if !resp.OK {
		logger.Debug("prompt hook condition not met", "reason", resp.Reason)
		result.Outcome = HookOutcomeBlocking
		result.BlockingError = &HookBlockingError{
			BlockingError: fmt.Sprintf("Prompt hook condition was not met: %s", resp.Reason),
			Command:       hook.Prompt,
		}
		result.PreventContinuation = true
		result.StopReason = resp.Reason
		return result
	}

	logger.Debug("prompt hook condition met")
	result.Outcome = HookOutcomeSuccess
	return result
}

// --- Agent Hook Executor ---

// AgentHookExecutor is the interface for agent-based hook execution.
// The actual implementation depends on the query system, so this is injected.
type AgentHookExecutor interface {
	ExecAgentHook(ctx context.Context, input AgentHookInput) (*AgentHookResponse, error)
}

// AgentHookInput holds the context for an agent hook execution.
type AgentHookInput struct {
	Prompt         string
	JSONInput      string
	Model          string
	Timeout        time.Duration
	TranscriptPath string
	MaxTurns       int
}

// AgentHookResponse is the result of an agent hook execution.
type AgentHookResponse struct {
	OK        bool
	Reason    string
	TurnCount int
	Cancelled bool
}

// ExecAgentHook executes an agent-based hook using a multi-turn LLM query.
// The agent can use tools to inspect the codebase and verify conditions.
func ExecAgentHook(
	ctx context.Context,
	hook HookCommand,
	hookName string,
	hookEvent HookEvent,
	jsonInput string,
	executor AgentHookExecutor,
	toolUseID string,
	transcriptPath string,
	logger *slog.Logger,
) *HookResult {
	result := &HookResult{Hook: &hook}

	// Replace $ARGUMENTS with the JSON input
	processedPrompt := SubstituteArguments(hook.Prompt, jsonInput)

	timeout := time.Duration(HookTimeouts.AgentDefault) * time.Second
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.Debug("executing agent hook",
		"prompt", processedPrompt,
		"model", hook.Model,
		"max_turns", 50,
	)

	resp, err := executor.ExecAgentHook(execCtx, AgentHookInput{
		Prompt:         processedPrompt,
		JSONInput:      jsonInput,
		Model:          hook.Model,
		Timeout:        timeout,
		TranscriptPath: transcriptPath,
		MaxTurns:       50,
	})

	if err != nil {
		if execCtx.Err() != nil {
			result.Outcome = HookOutcomeCancelled
			return result
		}
		logger.Error("agent hook error", "error", err)
		result.Outcome = HookOutcomeNonBlockingError
		result.Stderr = fmt.Sprintf("Error executing agent hook: %v", err)
		return result
	}

	if resp.Cancelled {
		result.Outcome = HookOutcomeCancelled
		return result
	}

	if !resp.OK {
		logger.Debug("agent hook condition not met", "reason", resp.Reason)
		result.Outcome = HookOutcomeBlocking
		result.BlockingError = &HookBlockingError{
			BlockingError: fmt.Sprintf("Agent hook condition was not met: %s", resp.Reason),
			Command:       hook.Prompt,
		}
		return result
	}

	logger.Debug("agent hook condition met", "turns", resp.TurnCount)
	result.Outcome = HookOutcomeSuccess
	return result
}

// --- Argument Substitution ---

// SubstituteArguments replaces $ARGUMENTS placeholder in a prompt with the
// JSON input. Also supports indexed arguments like $ARGUMENTS[0], $ARGUMENTS[1],
// or shorthand $0, $1, etc.
func SubstituteArguments(prompt, jsonInput string) string {
	if jsonInput == "" {
		return prompt
	}

	// First, try indexed substitution ($ARGUMENTS[0], $0, etc.)
	// Parse jsonInput as JSON array for indexed access
	var args []interface{}
	if err := json.Unmarshal([]byte(jsonInput), &args); err == nil && len(args) > 0 {
		// Replace $ARGUMENTS[N] patterns
		result := prompt
		for i, arg := range args {
			argStr := fmt.Sprintf("%v", arg)
			result = strings.ReplaceAll(result, fmt.Sprintf("$ARGUMENTS[%d]", i), argStr)
			result = strings.ReplaceAll(result, fmt.Sprintf("$%d", i), argStr)
		}
		// Also replace bare $ARGUMENTS with the full JSON
		result = strings.ReplaceAll(result, "$ARGUMENTS", jsonInput)
		return result
	}

	// Simple replacement: $ARGUMENTS → full JSON input
	return strings.ReplaceAll(prompt, "$ARGUMENTS", jsonInput)
}

// --- Unified Hook Dispatcher ---

// DispatchHook routes a hook to the appropriate executor based on its type.
func DispatchHook(
	ctx context.Context,
	hook HookCommand,
	hookName string,
	hookEvent HookEvent,
	jsonInput string,
	input ShellHookInput,
	toolUseID string,
	promptExec PromptHookExecutor,
	agentExec AgentHookExecutor,
	allowlist *DomainAllowlist,
	env map[string]string,
	transcriptPath string,
	logger *slog.Logger,
) *HookResult {
	switch hook.Type {
	case HookTypeCommand:
		output, err := ExecShellHook(ctx, hook, input, logger)
		if err != nil {
			return &HookResult{
				Hook:    &hook,
				Outcome: HookOutcomeCancelled,
				Stderr:  err.Error(),
			}
		}
		return InterpretShellResult(&hook, hookName, hookEvent, output, toolUseID)

	case HookTypeHTTP:
		httpResult := ExecHTTPHook(ctx, hook, hookEvent, jsonInput, allowlist, env, logger)
		return InterpretHTTPResult(&hook, hookName, hookEvent, httpResult, toolUseID)

	case HookTypePrompt:
		if promptExec == nil {
			return &HookResult{
				Hook:    &hook,
				Outcome: HookOutcomeNonBlockingError,
				Stderr:  "prompt hook executor not available",
			}
		}
		return ExecPromptHook(ctx, hook, hookName, hookEvent, jsonInput, promptExec, toolUseID, logger)

	case HookTypeAgent:
		if agentExec == nil {
			return &HookResult{
				Hook:    &hook,
				Outcome: HookOutcomeNonBlockingError,
				Stderr:  "agent hook executor not available",
			}
		}
		return ExecAgentHook(ctx, hook, hookName, hookEvent, jsonInput, agentExec, toolUseID, transcriptPath, logger)

	default:
		return &HookResult{
			Hook:    &hook,
			Outcome: HookOutcomeNonBlockingError,
			Stderr:  fmt.Sprintf("unknown hook type: %s", hook.Type),
		}
	}
}
