// Package powershell provides a PowerShell command execution tool with
// security validation, dangerous cmdlet detection, and output formatting.
//
// Mirrors the security analysis from Claude Code's PowerShellTool:
//   - Dangerous cmdlet detection
//   - Parameter validation (-Force, -Recurse, etc.)
//   - Pipeline analysis for dangerous pipe chains
//   - Encoded command detection
//   - Execution policy handling
//   - PowerShell Core (pwsh) vs Windows PowerShell (powershell.exe) detection
//
// Ported from Claude Code's TypeScript PowerShellTool/.
package powershell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ─── PowerShell detection ─────────────────────────────────────────────

// Edition identifies the PowerShell edition.
type Edition string

const (
	EditionCore    Edition = "core"    // PowerShell 7+ (pwsh)
	EditionDesktop Edition = "desktop" // Windows PowerShell 5.1
	EditionUnknown Edition = "unknown"
)

var (
	detectedPath    string
	detectedEdition Edition
	detectOnce      sync.Once
)

// DetectPowerShell finds the PowerShell executable and determines its edition.
func DetectPowerShell() (path string, edition Edition) {
	detectOnce.Do(func() {
		// Try pwsh (PowerShell Core) first.
		if p, err := exec.LookPath("pwsh"); err == nil {
			detectedPath = p
			detectedEdition = EditionCore
			return
		}
		// Fall back to powershell (Windows PowerShell 5.1).
		if p, err := exec.LookPath("powershell"); err == nil {
			detectedPath = p
			detectedEdition = EditionDesktop
			return
		}
		// On Windows, try powershell.exe explicitly.
		if runtime.GOOS == "windows" {
			if p, err := exec.LookPath("powershell.exe"); err == nil {
				detectedPath = p
				detectedEdition = EditionDesktop
				return
			}
		}
		detectedEdition = EditionUnknown
	})
	return detectedPath, detectedEdition
}

// IsAvailable returns whether PowerShell is installed.
func IsAvailable() bool {
	path, _ := DetectPowerShell()
	return path != ""
}

// ─── Execution result ─────────────────────────────────────────────────

// ExecResult holds the output of a PowerShell command execution.
type ExecResult struct {
	Stdout      string
	Stderr      string
	ExitCode    int
	Interrupted bool
	Duration    time.Duration
}

// ─── PowerShellTool ───────────────────────────────────────────────────

// PowerShellTool executes PowerShell commands with security validation.
type PowerShellTool struct {
	mu             sync.Mutex
	psPath         string
	edition        Edition
	defaultTimeout time.Duration
	maxTimeout     time.Duration
	cwd            string // working directory
}

// NewPowerShellTool creates a new PowerShell tool.
func NewPowerShellTool() (*PowerShellTool, error) {
	psPath, edition := DetectPowerShell()
	if psPath == "" {
		return nil, fmt.Errorf("PowerShell is not available on this system")
	}

	return &PowerShellTool{
		psPath:         psPath,
		edition:        edition,
		defaultTimeout: 120 * time.Second,
		maxTimeout:     600 * time.Second,
	}, nil
}

// SetWorkingDirectory sets the working directory for command execution.
func (t *PowerShellTool) SetWorkingDirectory(dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cwd = dir
}

// Edition returns the detected PowerShell edition.
func (t *PowerShellTool) Edition() Edition {
	return t.edition
}

// Execute runs a PowerShell command with security validation.
func (t *PowerShellTool) Execute(ctx context.Context, command string, timeout time.Duration) (*ExecResult, error) {
	if command == "" {
		return &ExecResult{ExitCode: 0}, nil
	}

	// Security validation.
	secResult := ValidateCommand(command)
	if secResult.Behavior == "deny" {
		return nil, fmt.Errorf("command blocked: %s", secResult.Message)
	}

	// Timeout capping.
	if timeout <= 0 {
		timeout = t.defaultTimeout
	}
	if timeout > t.maxTimeout {
		timeout = t.maxTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return t.executeRaw(ctx, command)
}

// ExecuteUnsafe runs a PowerShell command without security validation.
// Only use this for trusted, internally-generated commands.
func (t *PowerShellTool) ExecuteUnsafe(ctx context.Context, command string, timeout time.Duration) (*ExecResult, error) {
	if timeout <= 0 {
		timeout = t.defaultTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return t.executeRaw(ctx, command)
}

func (t *PowerShellTool) executeRaw(ctx context.Context, command string) (*ExecResult, error) {
	start := time.Now()

	args := []string{
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	}

	cmd := exec.CommandContext(ctx, t.psPath, args...)

	t.mu.Lock()
	if t.cwd != "" {
		cmd.Dir = t.cwd
	}
	t.mu.Unlock()

	// Capture stdout and stderr separately.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Inherit environment.
	cmd.Env = os.Environ()

	err := cmd.Run()
	duration := time.Since(start)

	result := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Interrupted = true
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("PowerShell execution failed: %w", err)
		}
	}

	return result, nil
}

// ─── Command semantics ────────────────────────────────────────────────

// CommandInterpretation holds the semantic meaning of an exit code.
type CommandInterpretation struct {
	IsError bool
	Message string
}

// InterpretCommandResult interprets exit codes using PowerShell semantics.
// PowerShell native cmdlets use $? not exit codes. External executables
// like grep, findstr, robocopy use non-zero codes for non-error states.
func InterpretCommandResult(command string, exitCode int, stdout, stderr string) CommandInterpretation {
	baseCmd := extractBaseCommand(command)

	switch baseCmd {
	case "grep", "rg":
		// grep/ripgrep: 0=matches, 1=no matches, 2+=error
		return CommandInterpretation{
			IsError: exitCode >= 2,
			Message: map[bool]string{true: "No matches found"}[exitCode == 1],
		}

	case "findstr":
		// findstr: 0=match, 1=no match, 2=error
		return CommandInterpretation{
			IsError: exitCode >= 2,
			Message: map[bool]string{true: "No matches found"}[exitCode == 1],
		}

	case "robocopy":
		// robocopy: 0-7=success, 8+=error
		if exitCode < 8 {
			msg := "Robocopy completed (no errors)"
			if exitCode&1 != 0 {
				msg = "Files copied successfully"
			} else if exitCode == 0 {
				msg = "No files copied (already in sync)"
			}
			return CommandInterpretation{IsError: false, Message: msg}
		}
		return CommandInterpretation{IsError: true}
	}

	// Default: 0=success, non-zero=error.
	return CommandInterpretation{IsError: exitCode != 0}
}

// extractBaseCommand gets the base command name from a PowerShell command line.
func extractBaseCommand(command string) string {
	// Take the last pipeline segment (determines exit code).
	segments := strings.FieldsFunc(command, func(r rune) bool {
		return r == ';' || r == '|'
	})
	if len(segments) == 0 {
		return ""
	}
	last := strings.TrimSpace(segments[len(segments)-1])

	// Strip PowerShell call operators.
	last = strings.TrimLeft(last, "&. ")

	// Get first token.
	firstToken := strings.Fields(last)
	if len(firstToken) == 0 {
		return ""
	}
	token := firstToken[0]

	// Strip quotes and path.
	token = strings.Trim(token, "'\"")
	parts := strings.FieldsFunc(token, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) > 0 {
		token = parts[len(parts)-1]
	}

	// Strip .exe suffix.
	token = strings.ToLower(token)
	token = strings.TrimSuffix(token, ".exe")

	return token
}

// ─── Sleep detection ──────────────────────────────────────────────────

// DetectBlockedSleepPattern catches Start-Sleep N as the first statement.
// Returns a description of the blocking pattern, or empty string if none.
func DetectBlockedSleepPattern(command string) string {
	trimmed := strings.TrimSpace(command)
	// First statement only.
	first := trimmed
	for _, sep := range []string{";", "|", "&", "\r", "\n"} {
		if idx := strings.Index(first, sep); idx >= 0 {
			first = first[:idx]
		}
	}
	first = strings.TrimSpace(first)

	// Match: Start-Sleep N, Start-Sleep -Seconds N, sleep N
	lower := strings.ToLower(first)
	if !strings.HasPrefix(lower, "start-sleep") && !strings.HasPrefix(lower, "sleep") {
		return ""
	}

	// Extract the number.
	fields := strings.Fields(first)
	if len(fields) < 2 {
		return ""
	}

	var secsStr string
	for i := 1; i < len(fields); i++ {
		f := strings.ToLower(fields[i])
		if strings.HasPrefix(f, "-s") { // -Seconds or -s
			continue
		}
		if f[0] >= '0' && f[0] <= '9' {
			secsStr = fields[i]
			break
		}
	}

	if secsStr == "" {
		return ""
	}

	var secs int
	if _, err := fmt.Sscanf(secsStr, "%d", &secs); err != nil {
		return ""
	}
	if secs < 2 {
		return "" // Sub-2s sleeps are fine.
	}

	rest := strings.TrimSpace(trimmed[len(first):])
	rest = strings.TrimLeft(rest, ";|& \t\r\n")
	if rest != "" {
		return fmt.Sprintf("Start-Sleep %d followed by: %s", secs, rest)
	}
	return fmt.Sprintf("standalone Start-Sleep %d", secs)
}
