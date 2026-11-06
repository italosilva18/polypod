package tool

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/bash"
	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/sandbox"
	"github.com/costa/polypod/internal/types"
)

// bashToolState holds mutable state for the bash tool, including optional
// sandbox executor that can be wired after registration.
type bashToolState struct {
	sandbox *sandbox.SandboxExecutor
}

// globalBashState is the shared state for the registered bash tool.
// It is set during RegisterBashTool and can be modified via SetBashSandbox.
var globalBashState = &bashToolState{}

// SetBashSandbox configures the sandbox executor for the bash tool.
// When set, destructive or dangerous commands are routed through the sandbox
// instead of direct execution. Pass nil to disable sandboxing.
func SetBashSandbox(executor *sandbox.SandboxExecutor) {
	globalBashState.sandbox = executor
}

// GetBashSandbox returns the current sandbox executor, or nil if not set.
func GetBashSandbox() *sandbox.SandboxExecutor {
	return globalBashState.sandbox
}

// sensitiveEnvPrefixes are environment variable prefixes that should be
// stripped from the child process environment to prevent accidental leakage.
var sensitiveEnvPrefixes = []string{
	"AWS_SECRET", "AWS_SESSION_TOKEN",
	"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY",
	"AZURE_", "GCP_",
	"GITHUB_TOKEN", "GITLAB_TOKEN", "BITBUCKET_TOKEN",
	"NPM_TOKEN", "PYPI_TOKEN",
	"DATABASE_URL", "DB_PASSWORD",
	"SECRET_KEY", "PRIVATE_KEY",
	"STRIPE_SECRET", "TWILIO_AUTH",
	"SENDGRID_API_KEY", "MAILGUN_API_KEY",
	"SLACK_TOKEN", "SLACK_BOT_TOKEN",
	"DISCORD_TOKEN",
	"JWT_SECRET",
	"ENCRYPTION_KEY",
	"SSH_PRIVATE",
}

// interactiveCommands are commands that require a TTY and should be rejected.
var interactiveCommands = map[string]bool{
	"vim": true, "nvim": true, "vi": true, "nano": true, "emacs": true,
	"pico": true, "micro": true, "ed": true,
	"top": true, "htop": true, "btop": true,
	"less": true, "more": true,
	"ftp": true, "sftp": true,
	"mysql": true, "psql": true, "sqlite3": true, "mongosh": true,
	"python": true, "python3": true, "ipython": true, "node": true, "irb": true,
	"ssh": true,
}

// RegisterBashTool creates and registers a production-quality bash tool that
// replaces the simple run_command. It leverages the bash/ security analysis
// package for command validation, dangerous pattern detection, and AST-based
// read-only classification.
func RegisterBashTool(reg *Registry, workingDir string) {
	bt := bash.NewBashTool(workingDir)
	bt.SetProjectDir(workingDir)

	logger := slog.Default().With("tool", constants.ToolBash)

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return constants.ToolBash },
		DescriptionFn: func() string {
			return "Execute a bash command in the shell with security analysis, " +
				"timeout management, and output capture"
		},
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
						"description": fmt.Sprintf("Optional timeout in milliseconds (default %d, max %d)", bash.DefaultTimeoutMs, bash.MaxTimeoutMs),
					},
					"working_dir": map[string]any{
						"type":        "string",
						"description": "Override working directory for this command",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Clear, concise description of what this command does",
					},
				},
				"required": []string{"command"},
			}
		},

		// --- Classification ---

		IsReadOnlyFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			return bash.IsReadOnlyCommand(cmd)
		},

		IsDestructiveFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			cat := bash.ClassifyCommand(cmd)
			return cat == bash.CategoryDestructive || cat == bash.CategoryPrivileged
		},

		IsConcurrencyFn: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			return bash.IsReadOnlyCommand(cmd)
		},

		// --- Validation ---

		ValidateFn: func(args map[string]any) types.ValidationResult {
			cmd, _ := args["command"].(string)
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				return types.ValidationResult{Valid: false, Message: "command is required"}
			}

			// Reject interactive/TUI commands that need a PTY
			base := bash.GetBaseCommand(cmd)
			if interactiveCommands[base] {
				// Allow if arguments suggest non-interactive usage (e.g. python -c, node -e)
				if !looksNonInteractive(cmd, base) {
					return types.ValidationResult{
						Valid:   false,
						Message: fmt.Sprintf("%q requires an interactive terminal and cannot run here", base),
					}
				}
			}

			return types.ValidationResult{Valid: true}
		},

		// --- Permission check ---

		CheckPermFn: func(args map[string]any, ctx types.ToolContext) types.PermissionResult {
			cmd, _ := args["command"].(string)
			return bt.CheckPermissions(cmd)
		},

		// --- Execution ---

		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			cmd, _ := args["command"].(string)
			cmd = strings.TrimSpace(cmd)
			desc, _ := args["description"].(string)

			timeoutMs := 0
			if t, ok := args["timeout"].(float64); ok {
				timeoutMs = int(t)
			}

			wd := workingDir
			if override, ok := args["working_dir"].(string); ok && override != "" {
				wd = override
			}
			if ctx.WorkingDir != "" {
				wd = ctx.WorkingDir
			}
			// Explicit working_dir param takes priority over context
			if override, ok := args["working_dir"].(string); ok && override != "" {
				wd = override
			}

			// --- Pre-execution security analysis ---
			secIssues := preflightSecurityCheck(cmd)
			if len(secIssues) > 0 {
				logger.Warn("security issues detected",
					"command", cmd,
					"issues", len(secIssues),
				)
			}

			// --- Sandbox routing ---
			// If a sandbox executor is configured, route dangerous/destructive
			// commands through it instead of direct execution.
			if sbx := globalBashState.sandbox; sbx != nil && sbx.Policy() != sandbox.SandboxNone {
				cat := bash.ClassifyCommand(cmd)
				isDestructive := cat == bash.CategoryDestructive || cat == bash.CategoryPrivileged
				isReadOnly := bash.IsReadOnlyCommand(cmd)

				// Route through sandbox if: command is destructive, or policy is
				// full/read_only (sandbox everything), or command is not read-only
				// under workspace policy.
				shouldSandbox := false
				switch sbx.Policy() {
				case sandbox.SandboxFull:
					shouldSandbox = true
				case sandbox.SandboxReadOnly:
					shouldSandbox = !isReadOnly
				case sandbox.SandboxWorkspace:
					shouldSandbox = isDestructive || !isReadOnly
				}

				if shouldSandbox {
					// Check network policy
					if !sbx.Config().AllowNetwork && !sandbox.IsNetworkAllowed(cmd) {
						return types.ToolResult{
							Content: fmt.Sprintf("Network access denied by sandbox policy (command: %s)", bash.GetBaseCommand(cmd)),
							IsError: true,
						}, nil
					}

					logger.Info("routing command through sandbox",
						"command", cmd,
						"policy", string(sbx.Policy()),
						"category", cat.String(),
					)

					output, exitCode, err := sbx.Execute(ctx.Context, cmd)
					if err != nil {
						return types.ToolResult{
							Content: fmt.Sprintf("Sandbox error: %v", err),
							IsError: true,
						}, nil
					}

					content := output
					if exitCode != 0 {
						content += fmt.Sprintf("\nExit code %d", exitCode)
					}
					if content == "" {
						content = fmt.Sprintf("(no output, exit code %d)", exitCode)
					}

					return types.ToolResult{
						Content: fmt.Sprintf("[sandbox:%s] %s", sbx.Policy(), content),
						IsError: exitCode != 0,
					}, nil
				}
			}

			// --- Command audit log ---
			startTime := time.Now()
			logger.Info("executing command",
				"command", cmd,
				"working_dir", wd,
				"timeout_ms", timeoutMs,
				"description", desc,
				"read_only", bash.IsReadOnlyCommand(cmd),
				"category", bash.ClassifyCommand(cmd).String(),
			)

			// --- Build environment with sensitive vars stripped ---
			sanitizedEnv := sanitizeEnvironment()

			// --- Execute ---
			input := bash.ExecInput{
				Command:     cmd,
				Timeout:     timeoutMs,
				Description: desc,
				WorkingDir:  wd,
				Env:         sanitizedEnv,
			}

			result, err := bt.Execute(ctx.Context, input)

			// --- Post-execution audit ---
			elapsed := time.Since(startTime)
			if err != nil {
				logger.Error("command failed",
					"command", cmd,
					"error", err,
					"elapsed", elapsed,
				)
				return types.ToolResult{
					Content: fmt.Sprintf("Error: %v", err),
					IsError: true,
				}, nil
			}

			logger.Info("command completed",
				"command", cmd,
				"exit_code", result.ExitCode,
				"elapsed", elapsed,
				"truncated", result.OutputTruncated,
				"timed_out", result.TimedOut,
			)

			return formatBashResult(result), nil
		},
	})
}

// preflightSecurityCheck runs AST-based and pattern-based security analysis
// on the command before execution. Returns a slice of human-readable warnings.
func preflightSecurityCheck(command string) []string {
	var warnings []string

	// 1. Static security validation (shell metacharacters, obfuscation, etc.)
	check := bash.ValidateCommandSecurity(command)
	if !check.Safe {
		warnings = append(warnings, "security: "+check.Message)
	}

	// 2. AST-based deep analysis (injection, traversal, privilege escalation, exfiltration)
	root := bash.Parse(command)
	if root != nil {
		analysis := bash.AnalyzeCommand(root)
		for _, issue := range analysis.Issues {
			warnings = append(warnings, fmt.Sprintf("%s: %s", issue.Type, issue.Message))
		}
	}

	// 3. Dangerous pattern detection (rm -rf, curl|bash, etc.)
	dangers := bash.DetectDangerousCommand(command)
	for _, d := range dangers {
		warnings = append(warnings, fmt.Sprintf("[%s] %s", d.Severity, d.Message))
	}

	// 4. Destructive command detection (git push --force, etc.)
	destructive := bash.CheckDestructiveCommand(command)
	for _, w := range destructive {
		warnings = append(warnings, fmt.Sprintf("[%s] %s", w.Severity, w.Warning))
	}

	return warnings
}

// sanitizeEnvironment returns a map of env var overrides that blank out
// sensitive variables to prevent accidental credential leakage.
func sanitizeEnvironment() map[string]string {
	// We don't pass overrides that would blank sensitive vars because
	// BashTool.buildEnv starts from os.Environ(). Instead, we return
	// nil and rely on the tool-level filtering. The BashTool's buildEnv
	// uses os.Environ, so we handle sanitization by overriding sensitive
	// vars with empty strings.
	overrides := make(map[string]string)
	for _, prefix := range sensitiveEnvPrefixes {
		// We scan current env and override matches with empty string.
		// This is done lazily - the BashTool.buildEnv will merge these.
		for _, envVar := range currentEnvKeys() {
			if strings.HasPrefix(strings.ToUpper(envVar), prefix) {
				overrides[envVar] = ""
			}
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// currentEnvKeys returns all current environment variable names.
func currentEnvKeys() []string {
	environ := os.Environ()
	keys := make([]string, 0, len(environ))
	for _, entry := range environ {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			keys = append(keys, entry[:idx])
		}
	}
	return keys
}

// formatBashResult converts a bash.ExecOutput into a types.ToolResult,
// always including exit code information.
func formatBashResult(output *bash.ExecOutput) types.ToolResult {
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
	}

	// Always include exit code
	if output.ExitCode != 0 {
		if output.ReturnCodeInterpretation != "" {
			parts = append(parts, output.ReturnCodeInterpretation)
		} else {
			parts = append(parts, fmt.Sprintf("Exit code %d", output.ExitCode))
		}
	}

	if output.OutputTruncated {
		parts = append(parts, fmt.Sprintf("[output truncated to %dKB]", bash.MaxOutputBytes/1024))
	}

	content := strings.Join(parts, "\n")
	if content == "" {
		if output.NoOutputExpected {
			content = "(no output)"
		} else {
			content = fmt.Sprintf("(no output, exit code %d)", output.ExitCode)
		}
	}

	isError := output.ExitCode != 0 && output.ReturnCodeInterpretation == ""

	return types.ToolResult{
		Content: content,
		IsError: isError,
	}
}

// looksNonInteractive returns true if a command that is normally interactive
// appears to be invoked in a non-interactive way (e.g. python -c "...",
// node -e "...", mysql -e "...").
func looksNonInteractive(command, base string) bool {
	nonInteractiveFlags := map[string][]string{
		"python":  {"-c", "--version", "-m", "-h"},
		"python3": {"-c", "--version", "-m", "-h"},
		"node":    {"-e", "--eval", "--version", "-p", "--print", "-h"},
		"irb":     {"-e", "--version", "-h"},
		"mysql":   {"-e", "--execute", "--version", "-h"},
		"psql":    {"-c", "--command", "--version", "-h"},
		"sqlite3": {":memory:", "--version", "-h"},
		"mongosh": {"--eval", "--version", "-h"},
		"ssh":     {"-o", "-L", "-R", "-D", "-N", "-f"},
		"less":    {}, // never non-interactive
		"more":    {}, // never non-interactive
		"vim":     {}, "nvim": {}, "vi": {}, "nano": {}, "emacs": {},
		"pico": {}, "micro": {}, "ed": {},
		"top": {"-b", "-n"}, "htop": {}, "btop": {},
		"ftp": {}, "sftp": {},
	}

	flags, ok := nonInteractiveFlags[base]
	if !ok || len(flags) == 0 {
		return false
	}

	for _, flag := range flags {
		if strings.Contains(command, " "+flag+" ") || strings.Contains(command, " "+flag+"\n") ||
			strings.HasSuffix(command, " "+flag) {
			return true
		}
	}
	return false
}
