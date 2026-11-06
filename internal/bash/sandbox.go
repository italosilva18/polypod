// Package bash provides sandbox integration for executing commands
// in isolated Docker containers with resource limits, network isolation,
// and controlled filesystem access. Migrated from Claude Code's sandbox concepts.
package bash

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SandboxConfig defines the configuration for a sandboxed command execution.
type SandboxConfig struct {
	// Enabled controls whether sandboxing is active.
	Enabled bool

	// Image is the Docker image to use for the sandbox container.
	// Defaults to "ubuntu:22.04" if empty.
	Image string

	// CPULimit is the CPU limit (e.g., "1.0" for 1 CPU core).
	CPULimit string

	// MemoryLimit is the memory limit (e.g., "512m", "1g").
	MemoryLimit string

	// DiskLimit is the maximum disk space for the container (e.g., "1g").
	DiskLimit string

	// NetworkMode controls network access.
	// "none" = no network, "bridge" = default Docker networking, "host" = host networking.
	NetworkMode string

	// ReadOnlyRootfs makes the root filesystem read-only.
	ReadOnlyRootfs bool

	// AllowedWritePaths are paths inside the container that may be written to.
	// Each entry is bind-mounted read-write from the host.
	AllowedWritePaths []string

	// ReadOnlyPaths are paths inside the container that are bind-mounted read-only.
	ReadOnlyPaths []string

	// DenyWritePaths are paths that must not be written to even within allowed paths.
	DenyWritePaths []string

	// AllowedHosts are hostnames that network access is permitted to.
	// Only effective when NetworkMode != "none".
	AllowedHosts []string

	// DeniedHosts are hostnames that network access is blocked for.
	DeniedHosts []string

	// Env are additional environment variables to set in the container.
	Env map[string]string

	// User specifies the user to run commands as inside the container (e.g., "1000:1000").
	User string

	// TmpDir is the writable temp directory inside the container.
	TmpDir string

	// AutoAllow automatically allows commands in sandbox mode without prompting.
	AutoAllow bool

	// IgnoreViolations are specific violation types to ignore.
	IgnoreViolations []string
}

// DefaultSandboxConfig returns a SandboxConfig with safe defaults.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:        false,
		Image:          "ubuntu:22.04",
		CPULimit:       "2.0",
		MemoryLimit:    "1g",
		DiskLimit:      "2g",
		NetworkMode:    "none",
		ReadOnlyRootfs: true,
		TmpDir:         "/tmp/sandbox",
	}
}

// SandboxExecutor executes commands inside a Docker container sandbox.
type SandboxExecutor struct {
	Config     SandboxConfig
	ProjectDir string
}

// NewSandboxExecutor creates a new sandbox executor for the given project directory.
func NewSandboxExecutor(projectDir string, config SandboxConfig) *SandboxExecutor {
	if config.Image == "" {
		config.Image = "ubuntu:22.04"
	}
	if config.TmpDir == "" {
		config.TmpDir = "/tmp/sandbox"
	}
	return &SandboxExecutor{
		Config:     config,
		ProjectDir: projectDir,
	}
}

// IsAvailable checks if Docker is available for sandboxing.
func (se *SandboxExecutor) IsAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// Execute runs a command inside the sandbox container.
func (se *SandboxExecutor) Execute(ctx context.Context, command string, workDir string, timeoutMs int) (*ExecOutput, error) {
	if !se.Config.Enabled {
		return nil, fmt.Errorf("sandbox is not enabled")
	}

	if !se.IsAvailable() {
		return nil, fmt.Errorf("docker is not available for sandboxing")
	}

	args := se.buildDockerArgs(command, workDir)

	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Duration(DefaultTimeoutMs) * time.Millisecond
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "docker", args...)

	var stdoutBuf, stderrBuf limitedBuffer
	stdoutBuf.maxSize = MaxOutputBytes
	stderrBuf.maxSize = MaxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	output := &ExecOutput{
		Stdout:          stdoutBuf.String(),
		Stderr:          stderrBuf.String(),
		Duration:        duration,
		OutputTruncated: stdoutBuf.truncated || stderrBuf.truncated,
	}

	if execCtx.Err() == context.DeadlineExceeded {
		output.TimedOut = true
		output.Interrupted = true
		output.ExitCode = -1
	} else if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			output.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("sandbox execution error: %w", err)
		}
	}

	// Annotate output with sandbox violation information
	output.Stderr = annotateSandboxViolations(output.Stderr, se.Config)

	return output, nil
}

// buildDockerArgs constructs the docker run command arguments.
func (se *SandboxExecutor) buildDockerArgs(command string, workDir string) []string {
	args := []string{
		"run",
		"--rm",          // Remove container after execution
		"--interactive", // Allow stdin
	}

	// Resource limits
	if se.Config.CPULimit != "" {
		args = append(args, "--cpus="+se.Config.CPULimit)
	}
	if se.Config.MemoryLimit != "" {
		args = append(args, "--memory="+se.Config.MemoryLimit)
	}

	// Network mode
	switch se.Config.NetworkMode {
	case "none":
		args = append(args, "--network=none")
	case "host":
		args = append(args, "--network=host")
	default:
		// Default Docker bridge networking
	}

	// Read-only root filesystem
	if se.Config.ReadOnlyRootfs {
		args = append(args, "--read-only")
		// Always mount a writable tmpfs for /tmp
		args = append(args, "--tmpfs", se.Config.TmpDir+":rw,exec,size=256m")
		args = append(args, "--tmpfs", "/var/tmp:rw,exec,size=128m")
	}

	// Security options - drop all capabilities by default
	args = append(args, "--cap-drop=ALL")

	// No new privileges
	args = append(args, "--security-opt=no-new-privileges")

	// User mapping
	if se.Config.User != "" {
		args = append(args, "--user="+se.Config.User)
	}

	// Mount project directory
	absProject, _ := filepath.Abs(se.ProjectDir)
	containerWorkDir := "/workspace"
	args = append(args, "-v", absProject+":"+containerWorkDir+":rw")
	args = append(args, "-w", containerWorkDir)

	// If workDir is a subdirectory of projectDir, adjust the container working dir
	if workDir != "" {
		absWorkDir, _ := filepath.Abs(workDir)
		if strings.HasPrefix(absWorkDir, absProject) {
			relPath, err := filepath.Rel(absProject, absWorkDir)
			if err == nil && relPath != "." {
				args = append(args[:len(args)-2], "-w", filepath.Join(containerWorkDir, relPath))
			}
		}
	}

	// Mount allowed write paths
	for _, p := range se.Config.AllowedWritePaths {
		absPath, _ := filepath.Abs(p)
		args = append(args, "-v", absPath+":"+absPath+":rw")
	}

	// Mount read-only paths
	for _, p := range se.Config.ReadOnlyPaths {
		absPath, _ := filepath.Abs(p)
		args = append(args, "-v", absPath+":"+absPath+":ro")
	}

	// Environment variables
	for k, v := range se.Config.Env {
		args = append(args, "-e", k+"="+v)
	}

	// Always set TMPDIR
	args = append(args, "-e", "TMPDIR="+se.Config.TmpDir)

	// Image and command
	args = append(args, se.Config.Image)
	args = append(args, "bash", "-c", command)

	return args
}

// ShouldUseSandbox determines if a command should be run in sandbox mode.
func ShouldUseSandbox(config SandboxConfig, input ExecInput) bool {
	if !config.Enabled {
		return false
	}

	// Check if command is in the excluded list
	base := GetBaseCommand(input.Command)
	for _, excluded := range sandboxExcludedCommands {
		if base == excluded {
			return false
		}
	}

	return true
}

// sandboxExcludedCommands are commands that should not run in sandbox
// (they need host access or are Docker-related).
var sandboxExcludedCommands = []string{
	"docker",
	"podman",
	"systemctl",
	"service",
	"journalctl",
	"mount",
	"umount",
}

// SandboxViolation represents a detected sandbox policy violation.
type SandboxViolation struct {
	Type    string // "filesystem", "network", "capability"
	Path    string // affected path or host
	Message string
}

// DetectSandboxViolations examines command output for sandbox violation indicators.
func DetectSandboxViolations(stdout, stderr string) []SandboxViolation {
	var violations []SandboxViolation
	combined := stdout + "\n" + stderr

	// Filesystem violations
	fsPatterns := []struct {
		pattern string
		vType   string
	}{
		{"Operation not permitted", "capability"},
		{"Read-only file system", "filesystem"},
		{"Permission denied", "filesystem"},
		{"cannot create directory", "filesystem"},
	}

	for _, p := range fsPatterns {
		if strings.Contains(combined, p.pattern) {
			violations = append(violations, SandboxViolation{
				Type:    p.vType,
				Message: p.pattern,
			})
		}
	}

	// Network violations
	netPatterns := []string{
		"Could not resolve host",
		"Connection refused",
		"Network is unreachable",
		"Name or service not known",
	}
	for _, p := range netPatterns {
		if strings.Contains(combined, p) {
			violations = append(violations, SandboxViolation{
				Type:    "network",
				Message: p,
			})
		}
	}

	return violations
}

// annotateSandboxViolations adds helpful messages to stderr when sandbox violations are detected.
func annotateSandboxViolations(stderr string, config SandboxConfig) string {
	violations := DetectSandboxViolations("", stderr)
	if len(violations) == 0 {
		return stderr
	}

	var annotations []string
	for _, v := range violations {
		switch v.Type {
		case "filesystem":
			annotations = append(annotations, fmt.Sprintf(
				"[sandbox] Filesystem access denied. Allowed write paths: %s",
				strings.Join(config.AllowedWritePaths, ", "),
			))
		case "network":
			annotations = append(annotations, fmt.Sprintf(
				"[sandbox] Network access denied. Network mode: %s",
				config.NetworkMode,
			))
		case "capability":
			annotations = append(annotations,
				"[sandbox] Operation requires elevated privileges not available in sandbox",
			)
		}
	}

	if len(annotations) > 0 {
		return stderr + "\n" + strings.Join(uniqueStrings(annotations), "\n")
	}
	return stderr
}

// SandboxFsConfig represents the filesystem access configuration for the sandbox.
type SandboxFsConfig struct {
	// Read controls which paths can be read.
	Read SandboxFsReadConfig

	// Write controls which paths can be written.
	Write SandboxFsWriteConfig
}

// SandboxFsReadConfig configures read access.
type SandboxFsReadConfig struct {
	// DenyOnly lists paths that cannot be read. Everything else is allowed.
	DenyOnly []string

	// AllowWithinDeny lists paths that are allowed even within denied paths.
	AllowWithinDeny []string
}

// SandboxFsWriteConfig configures write access.
type SandboxFsWriteConfig struct {
	// AllowOnly lists paths that can be written. Everything else is denied.
	AllowOnly []string

	// DenyWithinAllow lists paths that cannot be written even within allowed paths.
	DenyWithinAllow []string
}

// SandboxNetworkConfig represents the network access configuration.
type SandboxNetworkConfig struct {
	// AllowedHosts are hostnames that may be accessed.
	AllowedHosts []string

	// DeniedHosts are hostnames that are blocked.
	DeniedHosts []string
}

// IsPathWritable checks if a path is writable under the sandbox filesystem config.
func (fs *SandboxFsConfig) IsPathWritable(path string) bool {
	absPath, _ := filepath.Abs(path)

	// Check if in any allowed write path
	allowed := false
	for _, allow := range fs.Write.AllowOnly {
		absAllow, _ := filepath.Abs(allow)
		if isSubpath(absPath, absAllow) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}

	// Check if in a denied-within-allow path
	for _, deny := range fs.Write.DenyWithinAllow {
		absDeny, _ := filepath.Abs(deny)
		if isSubpath(absPath, absDeny) {
			return false
		}
	}

	return true
}

// IsPathReadable checks if a path is readable under the sandbox filesystem config.
func (fs *SandboxFsConfig) IsPathReadable(path string) bool {
	absPath, _ := filepath.Abs(path)

	// Check if in a denied read path
	for _, deny := range fs.Read.DenyOnly {
		absDeny, _ := filepath.Abs(deny)
		if isSubpath(absPath, absDeny) {
			// Check if in an allowed-within-deny path
			for _, allow := range fs.Read.AllowWithinDeny {
				absAllow, _ := filepath.Abs(allow)
				if isSubpath(absPath, absAllow) {
					return true
				}
			}
			return false
		}
	}

	return true // everything else is readable
}
