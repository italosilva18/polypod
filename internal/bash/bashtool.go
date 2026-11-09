// Package bash provides the BashTool implementation for executing shell commands
// with full security, timeout management, output capture, and process lifecycle.
// Migrated from Claude Code's BashTool.tsx.
package bash

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// DefaultTimeoutMs is the default command timeout in milliseconds.
const DefaultTimeoutMs = 120_000 // 2 minutes

// MaxTimeoutMs is the maximum allowed command timeout.
const MaxTimeoutMs = 600_000 // 10 minutes

// MaxOutputLines is the threshold above which output is truncated.
const MaxOutputLines = 2000

// MaxOutputBytes is the threshold above which output is truncated.
const MaxOutputBytes = 512_000 // 500KB

// ProgressThresholdMs is the delay before showing progress updates.
const ProgressThresholdMs = 2000

// BashToolName is the canonical name for the bash tool.
const BashToolName = "bash"

// ExecInput represents the input parameters for a bash command execution.
type ExecInput struct {
	Command     string
	Timeout     int    // milliseconds; 0 means DefaultTimeoutMs
	Description string // human-readable description of the command
	WorkingDir  string // override working directory
	Background  bool   // run in background
	UseRTK      bool   // request optional RTK compression after permission checks

	// Environment overrides (merged with current env).
	Env map[string]string
}

// ExecOutput represents the result of a bash command execution.
type ExecOutput struct {
	Stdout      string
	Stderr      string
	ExitCode    int
	Interrupted bool
	TimedOut    bool
	Duration    time.Duration

	// OutputTruncated is true if the output exceeded MaxOutputBytes.
	OutputTruncated bool
	// OutputID lets bash_output retrieve stdout and stderr when the preview was truncated.
	OutputID           string
	OutputStorageError string
	RTKApplied         bool

	// BackgroundTaskID is set when the command was backgrounded.
	BackgroundTaskID string

	// NoOutputExpected is true for silent commands (mv, cp, rm, etc.).
	NoOutputExpected bool

	// ReturnCodeInterpretation provides semantic meaning for the exit code.
	ReturnCodeInterpretation string
}

// BashTool executes shell commands with security validation, timeout management,
// and output capture. It is the most critical tool in the system.
type BashTool struct {
	workingDir string
	projectDir string // project root for path validation
	mu         sync.Mutex
	shell      string // "bash" or "sh"

	// AllowedDirs are additional directories the tool may operate in.
	AllowedDirs []string

	// SecurityValidator runs security checks before execution.
	SecurityValidator func(command string) SecurityCheck

	// PermissionChecker evaluates permission rules.
	PermissionChecker func(command string) types.PermissionResult

	// OnProgress is called periodically for long-running commands.
	OnProgress func(output string, elapsed time.Duration, totalLines int)
}

// NewBashTool creates a new BashTool with the given working directory.
func NewBashTool(workingDir string) *BashTool {
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shell = "sh"
	}

	return &BashTool{
		workingDir:        workingDir,
		projectDir:        workingDir,
		shell:             shell,
		SecurityValidator: ValidateCommandSecurity,
	}
}

// SetProjectDir sets the project root directory for path validation.
func (bt *BashTool) SetProjectDir(dir string) {
	bt.projectDir = dir
}

// ToolDef returns a types.ToolDef for the BashTool, suitable for registration
// in the tool registry.
func (bt *BashTool) ToolDef() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return constants.ToolBash },
		DescriptionFn: func() string { return "Execute a bash command in the shell" },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": fmt.Sprintf("Optional timeout in milliseconds (max %d)", MaxTimeoutMs),
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Clear, concise description of what this command does",
					},
				},
				"required": []string{"command"},
			}
		},
		IsDestructiveFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			return !IsReadOnlyCommand(cmd)
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			return IsReadOnlyCommand(cmd)
		},
		IsConcurrencyFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			return IsReadOnlyCommand(cmd)
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			cmd, _ := args["command"].(string)
			if strings.TrimSpace(cmd) == "" {
				return types.ValidationResult{Valid: false, Message: "command is required"}
			}
			return types.ValidationResult{Valid: true}
		},
		CheckPermFn: func(args map[string]any, ctx types.ToolContext) types.PermissionResult {
			cmd, _ := args["command"].(string)
			return bt.CheckPermissions(cmd)
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			cmd, _ := args["command"].(string)
			timeoutMs := 0
			if t, ok := args["timeout"].(float64); ok {
				timeoutMs = int(t)
			}
			desc, _ := args["description"].(string)

			input := ExecInput{
				Command:     cmd,
				Timeout:     timeoutMs,
				Description: desc,
				WorkingDir:  ctx.WorkingDir,
			}

			result, err := bt.Execute(ctx.Context, input)
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error: %v", err),
					IsError: true,
				}, nil
			}

			return bt.formatResult(result), nil
		},
	}
}

// CheckPermissions evaluates whether a command is allowed to execute.
func (bt *BashTool) CheckPermissions(command string) types.PermissionResult {
	command = strings.TrimSpace(command)

	// 1. Run security validation
	if bt.SecurityValidator != nil {
		check := bt.SecurityValidator(command)
		if !check.Safe {
			return types.PermissionResult{
				Behavior: types.PermissionAsk,
				Message:  check.Message,
			}
		}
	}

	// 2. Check destructive patterns
	warnings := CheckDestructiveCommand(command)
	for _, w := range warnings {
		if w.Severity == "high" {
			return types.PermissionResult{
				Behavior: types.PermissionAsk,
				Message:  w.Warning,
			}
		}
	}

	// 3. Check custom permission checker
	if bt.PermissionChecker != nil {
		return bt.PermissionChecker(command)
	}

	// 4. Read-only commands are auto-allowed
	if IsReadOnlyCommand(command) {
		return types.PermissionResult{Behavior: types.PermissionAllow}
	}

	// 5. Default: ask
	return types.PermissionResult{
		Behavior: types.PermissionAsk,
		Message:  "Command requires approval",
	}
}

// Execute runs a shell command with full lifecycle management.
func (bt *BashTool) Execute(ctx context.Context, input ExecInput) (*ExecOutput, error) {
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return &ExecOutput{Stdout: "", ExitCode: 0}, nil
	}

	// Determine timeout
	timeoutMs := input.Timeout
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}
	if timeoutMs > MaxTimeoutMs {
		timeoutMs = MaxTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	// Determine working directory
	workDir := input.WorkingDir
	if workDir == "" {
		workDir = bt.workingDir
	}

	// Validate working directory exists
	if err := validateWorkingDir(workDir); err != nil {
		return nil, fmt.Errorf("invalid working directory %q: %w", workDir, err)
	}

	// Create command with timeout context
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	commandToRun := command
	execEnv := input.Env
	rtkApplied := false
	if input.UseRTK {
		if rewritten, ok := bt.rewriteWithRTK(execCtx, command, workDir, input.Env); ok {
			commandToRun = rewritten
			rtkApplied = true
			execEnv = make(map[string]string, len(input.Env)+1)
			for key, value := range input.Env {
				execEnv[key] = value
			}
			execEnv["RTK_RECALL"] = "0"
		}
	}

	cmd := exec.CommandContext(execCtx, bt.shell, "-c", commandToRun)
	cmd.Dir = workDir

	// Set up environment
	cmd.Env = bt.buildEnv(execEnv)

	// Set process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Keep a short preview in memory and spool longer output to private files.
	outputID, stdoutCapture, stderrCapture, storeErr := openOutputCaptures()
	if storeErr != nil {
		stdoutCapture = newOutputCapture(nil)
		stderrCapture = newOutputCapture(nil)
	}
	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	startTime := time.Now()

	// Start the command
	if err := cmd.Start(); err != nil {
		stdoutCapture.close()
		stderrCapture.close()
		if stdoutCapture.file != nil {
			os.Remove(stdoutCapture.file.Name())
			os.Remove(stderrCapture.file.Name())
		}
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// Start progress reporting goroutine
	var progressDone chan struct{}
	if bt.OnProgress != nil {
		progressDone = make(chan struct{})
		go bt.reportProgress(execCtx, &stdoutCapture.preview, startTime, progressDone)
	}

	// Wait for completion
	err := cmd.Wait()
	duration := time.Since(startTime)
	stdoutCloseErr := stdoutCapture.close()
	stderrCloseErr := stderrCapture.close()

	// Stop progress reporting
	if progressDone != nil {
		close(progressDone)
	}

	// Build result
	output := &ExecOutput{
		Stdout:     stdoutCapture.preview.String(),
		Stderr:     stderrCapture.preview.String(),
		Duration:   duration,
		RTKApplied: rtkApplied,
	}

	// Check if output was truncated
	output.OutputTruncated = stdoutCapture.preview.truncated || stderrCapture.preview.truncated ||
		strings.Count(output.Stdout, "\n")+1 > MaxOutputLines
	if storeErr == nil {
		if stdoutCapture.writeErr != nil {
			storeErr = stdoutCapture.writeErr
		} else if stderrCapture.writeErr != nil {
			storeErr = stderrCapture.writeErr
		} else if stdoutCloseErr != nil {
			storeErr = stdoutCloseErr
		} else if stderrCloseErr != nil {
			storeErr = stderrCloseErr
		}
	}
	if output.OutputTruncated && storeErr == nil {
		output.OutputID = outputID
	} else if output.OutputTruncated && storeErr != nil {
		output.OutputStorageError = storeErr.Error()
	}
	if !output.OutputTruncated || storeErr != nil {
		if stdoutCapture.file != nil {
			os.Remove(stdoutCapture.file.Name())
			os.Remove(stderrCapture.file.Name())
		}
	}

	// Determine exit status
	if execCtx.Err() == context.DeadlineExceeded {
		output.TimedOut = true
		output.Interrupted = true
		output.ExitCode = -1
		// Kill the entire process group
		bt.killProcessGroup(cmd)
	} else if ctx.Err() != nil {
		// Parent context cancelled (user interrupt)
		output.Interrupted = true
		output.ExitCode = -1
		bt.killProcessGroup(cmd)
	} else if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			output.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("command execution error: %w", err)
		}
	}

	// Add semantic interpretation
	meaning, desc := CommandExitSemantics(command, output.ExitCode)
	if meaning != ExitSuccess && meaning != ExitFailure {
		output.ReturnCodeInterpretation = desc
	}

	// Detect if command should produce no output
	output.NoOutputExpected = isSilentCommand(command)

	// Post-process output
	output.Stdout = postProcessOutput(output.Stdout)
	output.Stderr = strings.TrimSpace(output.Stderr)

	return output, nil
}

// formatResult converts ExecOutput to a ToolResult.
func (bt *BashTool) formatResult(output *ExecOutput) types.ToolResult {
	var parts []string

	stdout := strings.TrimRight(output.Stdout, "\n\r ")
	if stdout != "" {
		parts = append(parts, stdout)
	}

	stderr := strings.TrimSpace(output.Stderr)
	if stderr != "" {
		parts = append(parts, stderr)
	}

	if output.Interrupted {
		if output.TimedOut {
			parts = append(parts, fmt.Sprintf("[timeout after %s]", output.Duration.Round(time.Second)))
		} else {
			parts = append(parts, "[command was interrupted]")
		}
	} else if output.ExitCode != 0 {
		// Only add exit code if it's an actual error (not semantic non-error)
		if output.ReturnCodeInterpretation == "" {
			parts = append(parts, fmt.Sprintf("Exit code %d", output.ExitCode))
		} else {
			parts = append(parts, output.ReturnCodeInterpretation)
		}
	}

	if output.OutputTruncated {
		if output.OutputID != "" {
			parts = append(parts, fmt.Sprintf("[saida completa: bash_output id=%s stream=stdout|stderr]", output.OutputID))
		} else {
			parts = append(parts, fmt.Sprintf("[saida truncada; falha no armazenamento: %s]", output.OutputStorageError))
		}
	}

	content := strings.Join(parts, "\n")
	if content == "" && output.NoOutputExpected {
		content = "(no output)"
	}

	isError := output.ExitCode != 0 && output.ReturnCodeInterpretation == ""

	return types.ToolResult{
		Content: content,
		IsError: isError,
	}
}

// buildEnv builds the environment for command execution.
func (bt *BashTool) buildEnv(overrides map[string]string) []string {
	env := os.Environ()

	// Apply overrides
	for k, v := range overrides {
		found := false
		prefix := k + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = prefix + v
				found = true
				break
			}
		}
		if !found {
			env = append(env, prefix+v)
		}
	}

	return env
}

// killProcessGroup sends SIGKILL to the entire process group.
func (bt *BashTool) killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		// Kill entire process group
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		// Fallback: kill just the process
		_ = cmd.Process.Kill()
	}
}

// reportProgress periodically reports command output.
func (bt *BashTool) reportProgress(ctx context.Context, buf *limitedBuffer, startTime time.Time, done chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Wait for initial threshold before first report
	initialDelay := time.NewTimer(time.Duration(ProgressThresholdMs) * time.Millisecond)
	select {
	case <-initialDelay.C:
	case <-done:
		initialDelay.Stop()
		return
	case <-ctx.Done():
		initialDelay.Stop()
		return
	}

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(startTime)
			content := buf.String()
			lines := strings.Count(content, "\n")
			if bt.OnProgress != nil {
				bt.OnProgress(lastNLines(content, 20), elapsed, lines)
			}
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// validateWorkingDir checks that a working directory exists and is a directory.
func validateWorkingDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	return nil
}

// postProcessOutput cleans and truncates command output.
func postProcessOutput(output string) string {
	// Strip leading empty lines
	output = strings.TrimLeft(output, "\n")

	// Truncate by line count if needed
	lines := strings.Split(output, "\n")
	if len(lines) > MaxOutputLines {
		output = strings.Join(lines[:MaxOutputLines], "\n")
	}

	return output
}

// isSilentCommand returns true for commands that produce no stdout on success.
func isSilentCommand(command string) bool {
	silentCmds := map[string]bool{
		"mv": true, "cp": true, "rm": true, "mkdir": true, "rmdir": true,
		"chmod": true, "chown": true, "chgrp": true, "touch": true,
		"ln": true, "cd": true, "export": true, "unset": true, "wait": true,
	}

	base := strings.Fields(strings.TrimSpace(command))
	if len(base) == 0 {
		return false
	}

	// Handle wrappers
	for _, w := range []string{"sudo", "time", "timeout", "nice", "nohup", "env"} {
		if base[0] == w && len(base) > 1 {
			base = base[1:]
		}
	}

	return silentCmds[base[0]]
}

// lastNLines returns the last n lines from content.
func lastNLines(content string, n int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= n {
		return content
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// SplitCommand splits a compound command into subcommands by &&, ||, and ;.
// This is a simplified version of Claude Code's splitCommand.
func SplitCommand(command string) []string {
	var result []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for i := 0; i < len(command); i++ {
		ch := command[i]

		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' && !inSingleQuote {
			current.WriteByte(ch)
			escaped = true
			continue
		}

		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteByte(ch)
			continue
		}

		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteByte(ch)
			continue
		}

		if !inSingleQuote && !inDoubleQuote {
			// Check for && or ||
			if i+1 < len(command) {
				pair := string([]byte{ch, command[i+1]})
				if pair == "&&" || pair == "||" {
					sub := strings.TrimSpace(current.String())
					if sub != "" {
						result = append(result, sub)
					}
					current.Reset()
					i++ // skip second char
					continue
				}
			}
			// Check for ;
			if ch == ';' {
				sub := strings.TrimSpace(current.String())
				if sub != "" {
					result = append(result, sub)
				}
				current.Reset()
				continue
			}
			// Check for | (not ||)
			if ch == '|' && (i+1 >= len(command) || command[i+1] != '|') {
				sub := strings.TrimSpace(current.String())
				if sub != "" {
					result = append(result, sub)
				}
				current.Reset()
				continue
			}
		}

		current.WriteByte(ch)
	}

	sub := strings.TrimSpace(current.String())
	if sub != "" {
		result = append(result, sub)
	}

	return result
}

// StripSafeWrappers removes safe command wrappers (env vars, timeout, time, nice, nohup)
// from a command string for permission matching. Migrated from bashPermissions.ts.
func StripSafeWrappers(command string) string {
	stripped := command

	// Phase 1: Strip safe environment variables
	for {
		prev := stripped
		stripped = stripCommentLines(stripped)
		stripped = stripSafeEnvVars(stripped)
		if stripped == prev {
			break
		}
	}

	// Phase 2: Strip wrapper commands
	for {
		prev := stripped
		stripped = stripCommentLines(stripped)
		for _, strip := range wrapperStrippers {
			stripped = strip(stripped)
		}
		if stripped == prev {
			break
		}
	}

	return strings.TrimSpace(stripped)
}

// stripCommentLines removes full-line comments from a command.
func stripCommentLines(command string) string {
	lines := strings.Split(command, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return command
	}
	return strings.Join(result, "\n")
}

// safeEnvVars is the set of environment variables safe to strip from commands.
var safeEnvVars = map[string]bool{
	"GOEXPERIMENT": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true,
	"GO111MODULE": true, "RUST_BACKTRACE": true, "RUST_LOG": true,
	"NODE_ENV": true, "PYTHONUNBUFFERED": true, "PYTHONDONTWRITEBYTECODE": true,
	"PYTEST_DISABLE_PLUGIN_AUTOLOAD": true, "PYTEST_DEBUG": true,
	"LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true,
	"LC_TIME": true, "CHARSET": true, "TERM": true, "COLORTERM": true,
	"NO_COLOR": true, "FORCE_COLOR": true, "TZ": true,
	"LS_COLORS": true, "LSCOLORS": true, "GREP_COLOR": true,
	"GREP_COLORS": true, "GCC_COLORS": true, "TIME_STYLE": true,
	"BLOCK_SIZE": true, "BLOCKSIZE": true,
}

// stripSafeEnvVars strips known-safe environment variable assignments from the beginning of a command.
func stripSafeEnvVars(command string) string {
	for {
		// Match VAR=value at the start followed by horizontal whitespace
		idx := strings.IndexByte(command, '=')
		if idx <= 0 {
			return command
		}

		// Check that everything before = is a valid variable name
		varName := command[:idx]
		if !isValidEnvVarName(varName) {
			return command
		}

		if !safeEnvVars[varName] {
			return command
		}

		// Find end of value (must be simple: alnum, _, ., /, :, -)
		rest := command[idx+1:]
		valueEnd := 0
		for valueEnd < len(rest) {
			ch := rest[valueEnd]
			if ch == ' ' || ch == '\t' {
				break
			}
			if !isSimpleValueChar(ch) {
				return command // value contains complex chars, bail
			}
			valueEnd++
		}

		// Must have whitespace after value
		if valueEnd >= len(rest) || (rest[valueEnd] != ' ' && rest[valueEnd] != '\t') {
			return command
		}

		// Skip value and whitespace
		afterValue := rest[valueEnd:]
		afterValue = strings.TrimLeft(afterValue, " \t")
		command = afterValue
	}
}

func isValidEnvVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, ch := range s {
		if i == 0 {
			if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_') {
				return false
			}
		} else {
			if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
				return false
			}
		}
	}
	return true
}

func isSimpleValueChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '.' ||
		ch == '/' || ch == ':' || ch == '-'
}

// wrapperStrippers remove safe wrappers from commands.
var wrapperStrippers = []func(string) string{
	stripTimeout,
	stripTime,
	stripNice,
	stripNohup,
	stripStdbuf,
}

func stripTimeout(cmd string) string {
	if !strings.HasPrefix(cmd, "timeout ") && !strings.HasPrefix(cmd, "timeout\t") {
		return cmd
	}
	parts := strings.Fields(cmd)
	if len(parts) < 3 { // timeout <duration> <command>
		return cmd
	}
	i := 1
	for i < len(parts) {
		arg := parts[i]
		if arg == "--" {
			i++
			break
		}
		if arg == "--foreground" || arg == "--preserve-status" || arg == "--verbose" || arg == "-v" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--kill-after=") || strings.HasPrefix(arg, "--signal=") {
			i++
			continue
		}
		if (arg == "--kill-after" || arg == "--signal" || arg == "-k" || arg == "-s") && i+1 < len(parts) {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return cmd // unknown flag, bail
		}
		break // found duration
	}
	if i >= len(parts) {
		return cmd
	}
	// parts[i] should be the duration
	i++ // skip duration
	if i >= len(parts) {
		return cmd
	}
	return strings.Join(parts[i:], " ")
}

func stripTime(cmd string) string {
	if strings.HasPrefix(cmd, "time ") || strings.HasPrefix(cmd, "time\t") {
		rest := strings.TrimSpace(cmd[4:])
		if strings.HasPrefix(rest, "-- ") {
			rest = strings.TrimSpace(rest[2:])
		}
		return rest
	}
	return cmd
}

func stripNice(cmd string) string {
	if !strings.HasPrefix(cmd, "nice ") && !strings.HasPrefix(cmd, "nice\t") {
		return cmd
	}
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return cmd
	}
	i := 1
	if i < len(parts) && parts[i] == "-n" && i+1 < len(parts) {
		i += 2 // skip -n <value>
	} else if i < len(parts) && len(parts[i]) > 1 && parts[i][0] == '-' && parts[i][1] >= '0' && parts[i][1] <= '9' {
		i++ // skip -N
	}
	if i < len(parts) && parts[i] == "--" {
		i++
	}
	if i >= len(parts) {
		return cmd
	}
	return strings.Join(parts[i:], " ")
}

func stripNohup(cmd string) string {
	if strings.HasPrefix(cmd, "nohup ") || strings.HasPrefix(cmd, "nohup\t") {
		rest := strings.TrimSpace(cmd[5:])
		if strings.HasPrefix(rest, "-- ") {
			rest = strings.TrimSpace(rest[2:])
		}
		return rest
	}
	return cmd
}

func stripStdbuf(cmd string) string {
	if !strings.HasPrefix(cmd, "stdbuf ") && !strings.HasPrefix(cmd, "stdbuf\t") {
		return cmd
	}
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return cmd
	}
	i := 1
	for i < len(parts) {
		arg := parts[i]
		if len(arg) >= 3 && arg[0] == '-' && (arg[1] == 'i' || arg[1] == 'o' || arg[1] == 'e') {
			i++
			continue
		}
		if arg == "--" {
			i++
			break
		}
		break
	}
	if i >= len(parts) {
		return cmd
	}
	return strings.Join(parts[i:], " ")
}

// IsSearchOrReadCommand checks if a command is a search or read operation.
func IsSearchOrReadCommand(command string) (isSearch, isRead, isList bool) {
	searchCmds := map[string]bool{
		"find": true, "grep": true, "rg": true, "ag": true,
		"ack": true, "locate": true, "which": true, "whereis": true,
	}
	readCmds := map[string]bool{
		"cat": true, "head": true, "tail": true, "less": true, "more": true,
		"wc": true, "stat": true, "file": true, "strings": true,
		"jq": true, "awk": true, "cut": true, "sort": true, "uniq": true, "tr": true,
	}
	listCmds := map[string]bool{
		"ls": true, "tree": true, "du": true,
	}
	neutralCmds := map[string]bool{
		"echo": true, "printf": true, "true": true, "false": true, ":": true,
	}

	subs := SplitCommand(command)
	hasNonNeutral := false
	for _, sub := range subs {
		base := strings.Fields(strings.TrimSpace(sub))
		if len(base) == 0 {
			continue
		}
		cmd := base[0]
		if neutralCmds[cmd] {
			continue
		}
		hasNonNeutral = true
		if searchCmds[cmd] {
			isSearch = true
		} else if readCmds[cmd] {
			isRead = true
		} else if listCmds[cmd] {
			isList = true
		} else {
			return false, false, false
		}
	}
	if !hasNonNeutral {
		return false, false, false
	}
	return
}

// GetBaseCommand extracts the base command name, stripping wrappers and env vars.
func GetBaseCommand(command string) string {
	stripped := StripSafeWrappers(command)
	fields := strings.Fields(stripped)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// limitedBuffer is a bytes.Buffer with a max size that tracks truncation.
type limitedBuffer struct {
	buf       bytes.Buffer
	maxSize   int
	truncated bool
	mu        sync.Mutex
}

func (lb *limitedBuffer) Write(p []byte) (n int, err error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if lb.truncated {
		return len(p), nil // accept but discard
	}

	remaining := lb.maxSize - lb.buf.Len()
	if remaining <= 0 {
		lb.truncated = true
		return len(p), nil
	}

	if len(p) > remaining {
		lb.buf.Write(p[:remaining])
		lb.truncated = true
		return len(p), nil
	}

	return lb.buf.Write(p)
}

func (lb *limitedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.String()
}

// Ensure limitedBuffer satisfies io.Writer.
var _ io.Writer = (*limitedBuffer)(nil)
