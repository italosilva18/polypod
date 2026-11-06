package command

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ShellInterpolator processes !`command` substitutions inside slash-command
// templates. Compared to the package-level helper ExecuteShellInterpolations,
// it offers per-instance configuration (timeout, working directory, env,
// allowlist) and an explicit safety validator.
//
// Syntax:
//
//	The current branch is !`git branch --show-current`
//	Today is !`date +%Y-%m-%d`
//	Files changed: !`git diff --name-only`
//
// Each match is replaced by the command's stdout (with trailing newlines
// trimmed). Failures become a `[error: ...]` marker so the surrounding
// template still parses cleanly downstream.
type ShellInterpolator struct {
	// Timeout bounds each individual command. Zero defaults to 10s.
	Timeout time.Duration

	// WorkingDir is the cwd of every spawned command. Empty means inherit.
	WorkingDir string

	// Env is the explicit environment for spawned commands. nil means
	// inherit the current process environment.
	Env []string

	// AllowList, when non-empty, restricts execution to commands whose
	// first token is one of the listed binaries. Validation always runs
	// the destructive-pattern check regardless of this field.
	AllowList []string
}

// NewShellInterpolator returns an interpolator with the standard 10s
// timeout and the given working directory.
func NewShellInterpolator(workingDir string) *ShellInterpolator {
	return &ShellInterpolator{
		Timeout:    10 * time.Second,
		WorkingDir: workingDir,
	}
}

// shellMatchRe is the same pattern used by ExecuteShellInterpolations and
// matches !`...` spans without nested backticks.
var shellMatchRe = regexp.MustCompile("!`([^`]*)`")

// Process expands every !`command` span in template. The first execution
// error is returned alongside the (possibly partially substituted) text.
// Each individual failure also leaves an inline `[error: ...]` marker so
// downstream consumers always see syntactically valid output.
func (i *ShellInterpolator) Process(ctx context.Context, template string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := i.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	matches := shellMatchRe.FindAllStringSubmatchIndex(template, -1)
	if len(matches) == 0 {
		return template, nil
	}

	var out strings.Builder
	out.Grow(len(template))
	last := 0
	var firstErr error

	for _, m := range matches {
		out.WriteString(template[last:m[0]])
		cmdStr := strings.TrimSpace(template[m[2]:m[3]])
		last = m[1]

		if cmdStr == "" {
			out.WriteString("[error: empty command]")
			if firstErr == nil {
				firstErr = fmt.Errorf("empty shell interpolation")
			}
			continue
		}

		if err := i.ValidateCommand(cmdStr); err != nil {
			out.WriteString(fmt.Sprintf("[error: %v]", err))
			if firstErr == nil {
				firstErr = fmt.Errorf("validate %q: %w", cmdStr, err)
			}
			continue
		}

		stdout, err := i.run(ctx, cmdStr, timeout)
		if err != nil {
			out.WriteString(fmt.Sprintf("[error: %v]", err))
			if firstErr == nil {
				firstErr = fmt.Errorf("shell interpolation %q failed: %w", cmdStr, err)
			}
			continue
		}
		out.WriteString(strings.TrimRight(stdout, "\n"))
	}
	out.WriteString(template[last:])
	return out.String(), firstErr
}

// ExtractCommands returns every !`command` body found in template, in
// document order. Empty bodies are skipped.
func ExtractCommands(template string) []string {
	matches := shellMatchRe.FindAllStringSubmatch(template, -1)
	if len(matches) == 0 {
		return nil
	}
	cmds := make([]string, 0, len(matches))
	for _, m := range matches {
		c := strings.TrimSpace(m[1])
		if c == "" {
			continue
		}
		cmds = append(cmds, c)
	}
	return cmds
}

// ---------------------------------------------------------------------------
// Safety
// ---------------------------------------------------------------------------

// destructivePatterns are command fragments that ShellInterpolator refuses
// to execute regardless of allowlist configuration. The list is intended to
// catch the most common foot-guns; it is not a substitute for sandboxing.
var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f[a-z]*\b`), // rm -rf, rm -fr
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*f[a-z]*r[a-z]*\b`),
	regexp.MustCompile(`(?i)\bdd\b`),
	regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`),
	regexp.MustCompile(`(?i)\bshred\b`),
	regexp.MustCompile(`(?i)\bmkswap\b`),
	regexp.MustCompile(`(?i):\s*\(\s*\)\s*{`), // fork bomb prefix
	regexp.MustCompile(`>\s*/dev/sd[a-z]`),
}

// ValidateCommand checks the command against the chaining and destructive
// rules and (when set) the allowlist. It does NOT execute anything.
func (i *ShellInterpolator) ValidateCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("empty command")
	}

	// Reject command chaining / piping. We deliberately keep this strict:
	// the interpolation feature is for single, side-effect-free reads.
	if idx := indexOfChaining(cmd); idx >= 0 {
		return fmt.Errorf("command chaining is not allowed")
	}

	for _, re := range destructivePatterns {
		if re.MatchString(cmd) {
			return fmt.Errorf("destructive command rejected")
		}
	}

	if len(i.AllowList) > 0 {
		head := firstToken(cmd)
		ok := false
		for _, allowed := range i.AllowList {
			if head == allowed {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("command %q not in allowlist", head)
		}
	}
	return nil
}

// indexOfChaining returns the position of the first chaining/piping
// operator outside of single- or double-quoted regions, or -1 if none.
// Recognised operators: ; && || |
func indexOfChaining(s string) int {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			// skip next char
			i++
			continue
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		}
		if inSingle || inDouble {
			continue
		}
		switch c {
		case ';':
			return i
		case '|':
			return i // catches both | and ||
		case '&':
			if i+1 < len(s) && s[i+1] == '&' {
				return i
			}
		}
	}
	return -1
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

func (i *ShellInterpolator) run(ctx context.Context, cmdStr string, timeout time.Duration) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-c", cmdStr)
	}
	if i.WorkingDir != "" {
		cmd.Dir = i.WorkingDir
	}
	if i.Env != nil {
		cmd.Env = i.Env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return stdout.String(), fmt.Errorf("timed out after %s", timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.String(), err
		}
		return stdout.String(), fmt.Errorf("%s: %w", msg, err)
	}
	return stdout.String(), nil
}
