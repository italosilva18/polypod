package tool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/constants"
	"github.com/costa/polypod/internal/types"
)

// powershellCandidates lists the PowerShell executables tried in order.
// `pwsh` (PowerShell 7+ / Core) is preferred because it is cross-platform
// and actively supported; `powershell` (Windows PowerShell 5.x) is used
// as a fallback on Windows hosts that lack pwsh.
var powershellCandidates = []string{"pwsh", "powershell"}

// defaultPowerShellTimeoutMs is the timeout applied when no explicit
// timeout_ms is supplied by the caller.
const defaultPowerShellTimeoutMs = 120_000

// maxPowerShellTimeoutMs caps the maximum timeout to avoid runaway jobs.
const maxPowerShellTimeoutMs = 600_000

// findPowerShell returns the first PowerShell executable found on PATH
// along with its resolved absolute path. It returns an error if neither
// `pwsh` nor `powershell` is available.
func findPowerShell() (name, path string, err error) {
	for _, candidate := range powershellCandidates {
		if p, lookErr := exec.LookPath(candidate); lookErr == nil {
			return candidate, p, nil
		}
	}
	return "", "", errors.New("no PowerShell interpreter found (tried pwsh, powershell)")
}

// NewPowerShellTool builds a Tool that executes PowerShell commands. The
// tool first tries `pwsh` (PowerShell 7+) and falls back to `powershell`
// (Windows PowerShell) when pwsh is not installed.
func NewPowerShellTool(workingDir string) types.Tool {
	return types.BuildTool(types.ToolDef{
		NameFn: func() string { return constants.ToolPowerShell },
		DescriptionFn: func() string {
			return "Execute a PowerShell command. Tries pwsh (PowerShell 7+) " +
				"first and falls back to Windows PowerShell. Supports an optional " +
				"timeout_ms parameter."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The PowerShell command or script to execute",
					},
					"timeout_ms": map[string]any{
						"type": "integer",
						"description": fmt.Sprintf(
							"Optional timeout in milliseconds (default %d, max %d)",
							defaultPowerShellTimeoutMs, maxPowerShellTimeoutMs,
						),
					},
				},
				"required": []string{"command"},
			}
		},

		// Classification: PowerShell is treated as destructive by default
		// because it can invoke any system command, file mutation, network
		// request, or process. We cannot meaningfully classify arbitrary
		// scripts, so we assume the worst to force permission prompts.
		IsReadOnlyFn:    func(map[string]any) bool { return false },
		IsDestructiveFn: func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return false },

		IsEnabledFn: func() bool {
			_, _, err := findPowerShell()
			return err == nil
		},

		ValidateFn: func(args map[string]any) types.ValidationResult {
			cmd, _ := args["command"].(string)
			if strings.TrimSpace(cmd) == "" {
				return types.ValidationResult{
					Valid:   false,
					Message: "command is required",
				}
			}
			if t, ok := args["timeout_ms"].(float64); ok {
				if t < 0 {
					return types.ValidationResult{
						Valid:   false,
						Message: "timeout_ms must be non-negative",
					}
				}
				if int(t) > maxPowerShellTimeoutMs {
					return types.ValidationResult{
						Valid: false,
						Message: fmt.Sprintf(
							"timeout_ms exceeds maximum of %d",
							maxPowerShellTimeoutMs,
						),
					}
				}
			}
			return types.ValidationResult{Valid: true}
		},

		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			cmd, _ := args["command"].(string)
			cmd = strings.TrimSpace(cmd)
			if cmd == "" {
				return types.ToolResult{
					Content: "Error: command is required",
					IsError: true,
				}, nil
			}

			// Resolve timeout
			timeoutMs := defaultPowerShellTimeoutMs
			if t, ok := args["timeout_ms"].(float64); ok && t > 0 {
				timeoutMs = int(t)
			} else if t, ok := args["timeout_ms"].(int); ok && t > 0 {
				timeoutMs = t
			}
			if timeoutMs > maxPowerShellTimeoutMs {
				timeoutMs = maxPowerShellTimeoutMs
			}

			// Resolve working directory
			wd := workingDir
			if ctx.WorkingDir != "" {
				wd = ctx.WorkingDir
			}

			// Locate the interpreter
			shellName, shellPath, err := findPowerShell()
			if err != nil {
				return types.ToolResult{
					Content: fmt.Sprintf("Error: %v", err),
					IsError: true,
				}, nil
			}

			// Set up timeout context
			parent := ctx.Context
			if parent == nil {
				parent = context.Background()
			}
			execCtx, cancel := context.WithTimeout(
				parent,
				time.Duration(timeoutMs)*time.Millisecond,
			)
			defer cancel()

			// Build the command with safe, non-interactive flags.
			// -NoProfile: skip user profiles for reproducibility
			// -NonInteractive: prevent hanging on prompts
			// -Command: run the supplied script
			psArgs := []string{"-NoProfile", "-NonInteractive", "-Command", cmd}
			execCmd := exec.CommandContext(execCtx, shellPath, psArgs...)
			if wd != "" {
				execCmd.Dir = wd
			}

			start := time.Now()
			output, runErr := execCmd.CombinedOutput()
			elapsed := time.Since(start)

			// Timeout handling
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				content := strings.TrimRight(string(output), "\n\r ")
				if content != "" {
					content += "\n"
				}
				content += fmt.Sprintf(
					"[timeout after %s via %s]",
					elapsed.Round(time.Millisecond), shellName,
				)
				return types.ToolResult{
					Content: content,
					IsError: true,
				}, nil
			}

			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if errors.As(runErr, &exitErr) {
					exitCode = exitErr.ExitCode()
				} else {
					return types.ToolResult{
						Content: fmt.Sprintf(
							"Error running %s: %v\n%s",
							shellName, runErr, string(output),
						),
						IsError: true,
					}, nil
				}
			}

			content := strings.TrimRight(string(output), "\n\r ")
			if exitCode != 0 {
				if content != "" {
					content += "\n"
				}
				content += fmt.Sprintf("Exit code %d", exitCode)
			}
			if content == "" {
				content = fmt.Sprintf("(no output, exit code %d)", exitCode)
			}

			return types.ToolResult{
				Content: content,
				IsError: exitCode != 0,
			}, nil
		},
	})
}

// RegisterPowerShellTool registers the PowerShell tool with the given
// registry. The tool is always registered even if PowerShell is not
// installed; IsEnabled() will then report false so callers can gate it.
func RegisterPowerShellTool(reg *Registry, workingDir string) {
	reg.Register(NewPowerShellTool(workingDir))
}
