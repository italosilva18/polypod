package bash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const rtkRewriteTimeout = 2 * time.Second

// RTKEnabled reports whether the optional output compressor was requested.
// The default remains the normal shell command and its unfiltered output.
func RTKEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPOD_RTK_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// rewriteWithRTK runs only after the original command has passed Polypod's
// validation, permission checks and audit start. A failed or inapplicable
// rewrite leaves the command unchanged; the command itself is never retried.
func (bt *BashTool) rewriteWithRTK(ctx context.Context, original, workDir string, env map[string]string) (string, bool) {
	originalNode, ok := singleSimpleCommand(original)
	if !ok || !rtkCandidate(original, originalNode) {
		return original, false
	}

	bin, err := resolveRTKBinary()
	if err != nil {
		slog.Debug("RTK unavailable; running original command", "error", err)
		return original, false
	}

	rewriteCtx, cancel := context.WithTimeout(ctx, rtkRewriteTimeout)
	defer cancel()

	rewriteEnv := make(map[string]string, len(env)+1)
	for key, value := range env {
		rewriteEnv[key] = value
	}
	rewriteEnv["RTK_RECALL"] = "0"

	rewriter := exec.CommandContext(rewriteCtx, bin, "rewrite", original)
	rewriter.Dir = workDir
	rewriter.Env = bt.buildEnv(rewriteEnv)
	rewriteOutput := &limitedBuffer{maxSize: 64 * 1024}
	rewriter.Stdout = rewriteOutput
	err = rewriter.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			slog.Debug("RTK rewrite failed before command execution", "error", err)
			return original, false
		}
		code = exitErr.ExitCode()
	}

	// RTK's codes 0 and 3 both carry a rewrite. Its Claude permission
	// classification has no effect on Polypod's earlier authorization.
	if code != 0 && code != 3 {
		return original, false
	}
	if rewriteOutput.truncated {
		return original, false
	}
	output := rewriteOutput.String()
	if output == "" || !utf8.ValidString(output) {
		return original, false
	}
	rewritten := output
	if !strings.HasPrefix(rewritten, "rtk ") || strings.ContainsAny(rewritten, "\r\n\x00") {
		return original, false
	}
	rewrittenNode, ok := singleSimpleCommand(rewritten)
	if !ok || rewrittenNode.Name != "rtk" || len(rewrittenNode.Args) < 2 || rewrittenNode.Args[1] != originalNode.Name {
		return original, false
	}

	// Pin the invocation to the executable we verified. In particular, a
	// privileged service must not resolve bare "rtk" via a user-writable PATH.
	return shellQuoteRTKPath(bin) + strings.TrimPrefix(rewritten, "rtk"), true
}

func rtkCandidate(original string, node *Node) bool {
	// Build and test output can be large, and RTK has dedicated filters for
	// these commands. Authorization still uses the original command.
	if len(node.Args) >= 2 {
		switch node.Name {
		case "go":
			return node.Args[1] == "build" || node.Args[1] == "test"
		case "cargo":
			return node.Args[1] == "build" || node.Args[1] == "test"
		}
	}
	if !IsReadOnlyCommand(original) && !(node.Name == "tree" && strings.TrimSpace(original) == "tree") {
		return false
	}
	switch node.Name {
	case "ls", "tree", "rg", "grep":
		return true
	case "git":
		if len(node.Args) < 2 {
			return false
		}
		switch node.Args[1] {
		case "status", "diff", "log", "show", "blame", "reflog":
			return true
		}
	}
	return false
}

func singleSimpleCommand(command string) (*Node, bool) {
	root := Parse(command)
	if root == nil || root.Type != NodeProgram || len(root.Children) != 1 {
		return nil, false
	}
	node := root.Children[0]
	if node == nil || node.Type != NodeCommand || len(node.EnvVars) != 0 || len(node.Redirects) != 0 ||
		strings.TrimSpace(node.Text) != strings.TrimSpace(command) {
		return nil, false
	}
	return node, true
}

func resolveRTKBinary() (string, error) {
	candidate := strings.TrimSpace(os.Getenv("POLYPOD_RTK_BIN"))
	privileged := os.Geteuid() == 0 || os.Geteuid() != os.Getuid() || os.Getegid() != os.Getgid()
	if candidate == "" {
		if privileged {
			return "", fmt.Errorf("privileged process requires absolute POLYPOD_RTK_BIN")
		}
		candidate = "rtk"
	}
	if privileged && !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("privileged process requires absolute POLYPOD_RTK_BIN")
	}
	bin, err := exec.LookPath(candidate)
	if err != nil {
		return "", fmt.Errorf("locating RTK: %w", err)
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return "", fmt.Errorf("resolving RTK path: %w", err)
	}
	if privileged {
		if err := verifyPrivilegedRTKPath(bin); err != nil {
			return "", err
		}
	}
	return bin, nil
}

func verifyPrivilegedRTKPath(bin string) error {
	for path := filepath.Clean(bin); ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat RTK path %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("RTK path %q contains a symlink", path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("RTK path %q must be root-owned and not writable by others", path)
		}
		if path == bin {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("RTK binary %q is not an executable regular file", path)
			}
		} else if !info.IsDir() {
			return fmt.Errorf("RTK parent %q is not a directory", path)
		}
		if path == string(filepath.Separator) {
			return nil
		}
	}
}

func shellQuoteRTKPath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}
