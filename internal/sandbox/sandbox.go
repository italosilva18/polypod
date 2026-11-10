// Package sandbox provides execution sandboxing with multiple isolation levels:
// path validation, read-only mounts, workspace-restricted writes, and full
// Docker-based isolation. It also detects network-accessing commands and
// provides configurable policy enforcement.
package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// ─── Policy types ──────────────────────────────────────────────────────

// SandboxPolicy controls the isolation level for command execution.
type SandboxPolicy string

const (
	// SandboxNone disables all sandboxing — commands run unrestricted.
	SandboxNone SandboxPolicy = "none"

	// SandboxReadOnly allows the command to read the filesystem but not write.
	SandboxReadOnly SandboxPolicy = "read_only"

	// SandboxWorkspace allows writes only within the workspace directory.
	SandboxWorkspace SandboxPolicy = "workspace"

	// SandboxFull provides full Docker-based isolation.
	SandboxFull SandboxPolicy = "full"
)

// SandboxConfig holds all settings for sandbox execution.
type SandboxConfig struct {
	// Policy is the isolation level.
	Policy SandboxPolicy `yaml:"policy"`

	// WorkspaceDir is the root directory of the project workspace.
	WorkspaceDir string `yaml:"workspace_dir"`

	// AllowNetwork controls whether network access is permitted.
	AllowNetwork bool `yaml:"allow_network"`

	// AllowedPaths are absolute paths allowed for write access (in addition to workspace).
	AllowedPaths []string `yaml:"allowed_paths"`

	// DeniedPaths are absolute paths explicitly denied for all access.
	DeniedPaths []string `yaml:"denied_paths"`

	// MaxMemoryMB is the memory limit in megabytes for Docker containers (SandboxFull).
	MaxMemoryMB int `yaml:"max_memory_mb"`

	// MaxCPUPercent is the CPU limit as a percentage (100 = 1 core) for Docker (SandboxFull).
	MaxCPUPercent int `yaml:"max_cpu_percent"`

	// TimeoutSecs is the maximum execution time in seconds.
	TimeoutSecs int `yaml:"timeout_secs"`

	// Image is the Docker image for SandboxFull (default: ubuntu:22.04).
	Image string `yaml:"image"`
}

// DefaultSandboxConfig returns a SandboxConfig with safe defaults.
func DefaultSandboxConfig(workspaceDir string) SandboxConfig {
	return SandboxConfig{
		Policy:        SandboxWorkspace,
		WorkspaceDir:  workspaceDir,
		AllowNetwork:  true,
		AllowedPaths:  nil,
		DeniedPaths:   nil,
		MaxMemoryMB:   512,
		MaxCPUPercent: 200, // 2 cores
		TimeoutSecs:   120,
		Image:         "ubuntu:22.04",
	}
}

// ─── Sensitive paths ───────────────────────────────────────────────────

// sensitivePathPatterns are paths that are always denied for write access
// regardless of policy. Read access is also denied for the most sensitive ones.
var sensitiveWritePaths = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"/etc/ssh",
	"/root",
}

// sensitiveReadPaths are paths denied even for read access.
var sensitiveReadPaths = []string{
	"/etc/shadow",
	"/etc/gshadow",
}

// userSensitiveDirs are directories under $HOME that are always denied for write.
var userSensitiveDirs = []string{
	".ssh",
	".gnupg",
	".aws",
	".config/gcloud",
	".azure",
	".kube",
}

// systemDirs are system directories denied for write access.
var systemDirs = []string{
	"/bin",
	"/sbin",
	"/usr/bin",
	"/usr/sbin",
	"/usr/lib",
	"/lib",
	"/boot",
	"/sys",
	"/proc",
	"/dev",
}

// ─── SandboxExecutor ───────────────────────────────────────────────────

// SandboxExecutor runs commands under the configured sandbox policy.
type SandboxExecutor struct {
	config SandboxConfig
	logger *slog.Logger
}

// NewSandboxExecutor creates a SandboxExecutor with the given configuration.
func NewSandboxExecutor(cfg SandboxConfig) *SandboxExecutor {
	if cfg.Image == "" {
		cfg.Image = "ubuntu:22.04"
	}
	if cfg.TimeoutSecs <= 0 {
		cfg.TimeoutSecs = 120
	}
	if cfg.MaxMemoryMB <= 0 {
		cfg.MaxMemoryMB = 512
	}
	if cfg.MaxCPUPercent <= 0 {
		cfg.MaxCPUPercent = 200
	}
	if cfg.WorkspaceDir == "" {
		wd, _ := os.Getwd()
		cfg.WorkspaceDir = wd
	}
	return &SandboxExecutor{
		config: cfg,
		logger: slog.Default().With("component", "sandbox"),
	}
}

// Config returns the executor's configuration (read-only copy).
func (se *SandboxExecutor) Config() SandboxConfig {
	return se.config
}

// Policy returns the current sandbox policy.
func (se *SandboxExecutor) Policy() SandboxPolicy {
	return se.config.Policy
}

// Execute runs a command under the sandbox policy.
// Returns: stdout+stderr combined output, exit code, and error.
func (se *SandboxExecutor) Execute(ctx context.Context, command string) (string, int, error) {
	switch se.config.Policy {
	case SandboxNone:
		return se.executeUnsandboxed(ctx, command)
	case SandboxReadOnly:
		return se.executeReadOnly(ctx, command)
	case SandboxWorkspace:
		return se.executeWorkspace(ctx, command)
	case SandboxFull:
		return se.executeFull(ctx, command)
	default:
		return "", -1, fmt.Errorf("unknown sandbox policy: %s", se.config.Policy)
	}
}

// ExecuteAt runs one command with a conversation-specific workspace. Copying
// the executor keeps concurrent channels from changing each other's mount or
// local command directory.
func (se *SandboxExecutor) ExecuteAt(ctx context.Context, command, dir string) (string, int, error) {
	if dir == "" {
		return se.Execute(ctx, command)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", -1, fmt.Errorf("resolving sandbox workspace: %w", err)
	}
	scoped := *se
	scoped.config.WorkspaceDir = abs
	return scoped.Execute(ctx, command)
}

// ─── Execution strategies ──────────────────────────────────────────────

// executeUnsandboxed runs the command with no isolation.
func (se *SandboxExecutor) executeUnsandboxed(ctx context.Context, command string) (string, int, error) {
	return se.runLocal(ctx, command, se.config.WorkspaceDir)
}

// executeReadOnly runs the command with network restrictions and path pre-validation.
// On Linux with Docker available, mounts the filesystem read-only.
// Falls back to local execution with path pre-validation otherwise.
func (se *SandboxExecutor) executeReadOnly(ctx context.Context, command string) (string, int, error) {
	if !se.config.AllowNetwork && !IsNetworkAllowed(command) {
		return "", -1, fmt.Errorf("network access denied by sandbox policy (command appears to access network)")
	}

	if isDockerAvailable() {
		return se.runDocker(ctx, command, true)
	}

	// Fallback: local execution (read-only is advisory only without Docker)
	se.logger.Warn("Docker unavailable; read_only policy falls back to local execution with path validation")
	return se.runLocal(ctx, command, se.config.WorkspaceDir)
}

// executeWorkspace validates that the command doesn't write outside the workspace,
// checks for network access, and runs with PATH restrictions.
func (se *SandboxExecutor) executeWorkspace(ctx context.Context, command string) (string, int, error) {
	if !se.config.AllowNetwork && !IsNetworkAllowed(command) {
		return "", -1, fmt.Errorf("network access denied by sandbox policy")
	}

	return se.runLocal(ctx, command, se.config.WorkspaceDir)
}

// executeFull runs the command inside a Docker container with full isolation.
// Falls back to workspace-restricted local execution if Docker is unavailable.
func (se *SandboxExecutor) executeFull(ctx context.Context, command string) (string, int, error) {
	if isDockerAvailable() {
		return se.runDocker(ctx, command, false)
	}

	se.logger.Warn("Docker unavailable for full sandbox; falling back to workspace-restricted local execution")
	return se.runLocal(ctx, command, se.config.WorkspaceDir)
}

// ─── Local execution ───────────────────────────────────────────────────

func (se *SandboxExecutor) runLocal(ctx context.Context, command, workDir string) (string, int, error) {
	timeout := time.Duration(se.config.TimeoutSecs) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Dir = workDir

	// Build restricted environment
	cmd.Env = se.buildRestrictedEnv()

	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", -1, nil
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return result, exitErr.ExitCode(), nil
		}
		return result, -1, err
	}
	return result, 0, nil
}

// buildRestrictedEnv returns an environment that strips sensitive variables.
func (se *SandboxExecutor) buildRestrictedEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		key := e
		if idx := strings.IndexByte(e, '='); idx > 0 {
			key = e[:idx]
		}
		upper := strings.ToUpper(key)
		if isSensitiveEnvVar(upper) {
			continue
		}
		env = append(env, e)
	}
	return env
}

var sensitiveEnvPrefixes = []string{
	"AWS_SECRET", "AWS_SESSION_TOKEN",
	"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY",
	"GITHUB_TOKEN", "GITLAB_TOKEN",
	"NPM_TOKEN", "PYPI_TOKEN",
	"DATABASE_URL", "DB_PASSWORD",
	"SECRET_KEY", "PRIVATE_KEY",
	"STRIPE_SECRET", "TWILIO_AUTH",
	"JWT_SECRET", "ENCRYPTION_KEY",
	"SSH_PRIVATE",
}

func isSensitiveEnvVar(name string) bool {
	for _, prefix := range sensitiveEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ─── Docker execution ──────────────────────────────────────────────────

func (se *SandboxExecutor) runDocker(ctx context.Context, command string, readOnly bool) (string, int, error) {
	timeout := time.Duration(se.config.TimeoutSecs) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"run", "--rm",
		fmt.Sprintf("--memory=%dm", se.config.MaxMemoryMB),
		fmt.Sprintf("--cpus=%.1f", float64(se.config.MaxCPUPercent)/100.0),
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
	}

	// Network isolation
	if !se.config.AllowNetwork {
		args = append(args, "--network=none")
	}

	// Mount workspace
	absWorkspace, _ := filepath.Abs(se.config.WorkspaceDir)
	mountFlag := ":rw"
	if readOnly {
		mountFlag = ":ro"
		args = append(args, "--read-only")
		args = append(args, "--tmpfs", "/tmp:rw,exec,size=256m")
	}
	args = append(args, "-v", absWorkspace+":/workspace"+mountFlag)
	args = append(args, "-w", "/workspace")

	// Mount additional allowed paths (read-write)
	if !readOnly {
		for _, p := range se.config.AllowedPaths {
			absP, _ := filepath.Abs(p)
			args = append(args, "-v", absP+":"+absP+":rw")
		}
	}

	args = append(args, se.config.Image, "bash", "-c", command)

	cmd := exec.CommandContext(execCtx, "docker", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", -1, nil
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return result, exitErr.ExitCode(), nil
		}
		return result, -1, err
	}
	return result, 0, nil
}

// ─── Path validation ───────────────────────────────────────────────────

// ValidatePath checks if a path operation is allowed under the current sandbox policy.
// Returns nil if allowed, or an error describing why it is blocked.
func ValidatePath(path string, policy SandboxPolicy, cfg SandboxConfig) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Always block explicitly denied paths
	for _, denied := range cfg.DeniedPaths {
		absDenied, _ := filepath.Abs(denied)
		if isSubpath(absPath, absDenied) {
			return fmt.Errorf("path %q is explicitly denied by sandbox policy", path)
		}
	}

	// Block sensitive read paths (for all policies except none)
	if policy != SandboxNone {
		for _, sp := range sensitiveReadPaths {
			if isSubpath(absPath, sp) {
				return fmt.Errorf("path %q is a sensitive system file and cannot be accessed", path)
			}
		}
	}

	// For write operations under workspace or stricter policies
	if policy == SandboxWorkspace || policy == SandboxReadOnly || policy == SandboxFull {
		// Block sensitive write paths
		for _, sp := range sensitiveWritePaths {
			if isSubpath(absPath, sp) {
				return fmt.Errorf("path %q is a protected system path", path)
			}
		}

		// Block user sensitive dirs
		home, _ := os.UserHomeDir()
		if home != "" {
			for _, dir := range userSensitiveDirs {
				sensitive := filepath.Join(home, dir)
				if isSubpath(absPath, sensitive) {
					return fmt.Errorf("path %q is in a sensitive user directory (~/%s)", path, dir)
				}
			}
		}

		// Block system directories
		for _, sysDir := range systemDirs {
			if isSubpath(absPath, sysDir) {
				return fmt.Errorf("path %q is in a protected system directory", path)
			}
		}
	}

	// Read-only policy: all writes blocked
	if policy == SandboxReadOnly {
		return fmt.Errorf("path %q: write access denied (read-only sandbox policy)", path)
	}

	// Workspace policy: must be within workspace or allowed paths
	if policy == SandboxWorkspace || policy == SandboxFull {
		absWorkspace, _ := filepath.Abs(cfg.WorkspaceDir)
		if isSubpath(absPath, absWorkspace) {
			return nil
		}

		// Check /tmp
		if isSubpath(absPath, os.TempDir()) {
			return nil
		}

		// Check explicitly allowed paths
		for _, allowed := range cfg.AllowedPaths {
			absAllowed, _ := filepath.Abs(allowed)
			if isSubpath(absPath, absAllowed) {
				return nil
			}
		}

		return fmt.Errorf("path %q is outside the workspace directory; sandbox policy requires writes within %q", path, cfg.WorkspaceDir)
	}

	return nil
}

// ValidatePathRead checks if a path can be read under the current sandbox policy.
// This is more permissive than ValidatePath (which checks write access).
func ValidatePathRead(path string, policy SandboxPolicy, cfg SandboxConfig) error {
	if policy == SandboxNone {
		return nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Block explicitly denied paths
	for _, denied := range cfg.DeniedPaths {
		absDenied, _ := filepath.Abs(denied)
		if isSubpath(absPath, absDenied) {
			return fmt.Errorf("path %q is explicitly denied by sandbox policy", path)
		}
	}

	// Block sensitive read paths
	for _, sp := range sensitiveReadPaths {
		if isSubpath(absPath, sp) {
			return fmt.Errorf("path %q is a sensitive system file", path)
		}
	}

	return nil
}

// isSubpath returns true if child is equal to or a subdirectory of parent.
func isSubpath(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// ─── Network sandbox ──────────────────────────────────────────────────

// networkCommands are commands that typically access the network.
var networkCommands = map[string]bool{
	"curl":     true,
	"wget":     true,
	"ssh":      true,
	"scp":      true,
	"sftp":     true,
	"nc":       true,
	"netcat":   true,
	"ncat":     true,
	"telnet":   true,
	"ftp":      true,
	"rsync":    true,
	"ping":     true,
	"nslookup": true,
	"dig":      true,
	"host":     true,
	"nmap":     true,
	"socat":    true,
}

// networkPatterns are patterns in commands that indicate network access.
var networkPatterns = []string{
	"http://",
	"https://",
	"ftp://",
	"ssh://",
	"/dev/tcp/",
	"/dev/udp/",
}

// IsNetworkAllowed returns true if the command does not appear to access the network.
// When the sandbox policy denies network, this can be used to detect and block
// network-accessing commands before execution.
func IsNetworkAllowed(command string) bool {
	// Extract base command
	base := getBaseCommand(command)
	if networkCommands[base] {
		return false
	}

	// Check for pipe chains: each segment may start with a network command
	for _, segment := range strings.Split(command, "|") {
		segment = strings.TrimSpace(segment)
		segBase := getBaseCommand(segment)
		if networkCommands[segBase] {
			return false
		}
	}

	// Check for network URL patterns
	lower := strings.ToLower(command)
	for _, pat := range networkPatterns {
		if strings.Contains(lower, pat) {
			return false
		}
	}

	return true
}

// getBaseCommand extracts the first word of a command string,
// stripping leading env assignments and sudo.
func getBaseCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	fields := strings.Fields(cmd)
	for _, f := range fields {
		// Skip env var assignments (FOO=bar)
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
			continue
		}
		// Skip sudo, env, nice, etc.
		switch f {
		case "sudo", "env", "nice", "nohup", "time", "timeout", "strace":
			continue
		}
		return f
	}
	if len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return ""
}

// ─── Docker availability ──────────────────────────────────────────────

func isDockerAvailable() bool {
	_, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// ─── Legacy Config + Sandbox (backward compatibility) ──────────────────

// Config holds legacy sandbox settings for backward compatibility with skill.Registry.
type Config struct {
	Enabled     bool   `yaml:"enabled"`
	Image       string `yaml:"image"`
	MemoryLimit string `yaml:"memory_limit"`
	CPULimit    string `yaml:"cpu_limit"`
	Timeout     int    `yaml:"timeout"`
	Network     bool   `yaml:"network"`
}

// Sandbox is the legacy Docker sandbox executor (backward compatible).
type Sandbox struct {
	config Config
	logger *slog.Logger
}

// New creates a legacy sandbox.
func New(cfg Config, logger *slog.Logger) *Sandbox {
	if cfg.Image == "" {
		cfg.Image = "ubuntu:22.04"
	}
	if cfg.MemoryLimit == "" {
		cfg.MemoryLimit = "256m"
	}
	if cfg.CPULimit == "" {
		cfg.CPULimit = "1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30
	}
	return &Sandbox{config: cfg, logger: logger}
}

// IsAvailable checks if Docker is running.
func (s *Sandbox) IsAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

// Execute runs a command inside a Docker container.
func (s *Sandbox) Execute(ctx context.Context, command, image string) (string, error) {
	if !s.IsAvailable() {
		return "", fmt.Errorf("docker nao disponivel")
	}
	if image == "" {
		image = s.config.Image
	}

	args := []string{
		"run", "--rm",
		"--memory=" + s.config.MemoryLimit,
		"--cpus=" + s.config.CPULimit,
	}
	if !s.config.Network {
		args = append(args, "--network=none")
	}
	args = append(args, image, "bash", "-c", command)

	timeout := time.Duration(s.config.Timeout) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "docker", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", nil
	}
	if err != nil {
		return result + "\n[exit: " + err.Error() + "]", nil
	}
	return result, nil
}

// ExecuteScript runs a script in the appropriate Docker image.
func (s *Sandbox) ExecuteScript(ctx context.Context, script, language string) (string, error) {
	if !s.IsAvailable() {
		return "", fmt.Errorf("docker nao disponivel")
	}

	var image, interpreter, ext string
	switch language {
	case "python":
		image, interpreter, ext = "python:3.12-slim", "python3", ".py"
	case "node", "javascript":
		image, interpreter, ext = "node:20-slim", "node", ".js"
	case "go":
		image, interpreter, ext = "golang:1.24", "go run", ".go"
	case "ruby":
		image, interpreter, ext = "ruby:3.3-slim", "ruby", ".rb"
	case "bash", "shell":
		image, interpreter, ext = "ubuntu:22.04", "bash", ".sh"
	default:
		image, interpreter, ext = "ubuntu:22.04", "bash", ".sh"
	}

	tmpFile, err := os.CreateTemp("", "polypod-sandbox-*"+ext)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(script)
	tmpFile.Close()

	timeout := time.Duration(s.config.Timeout) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"run", "--rm",
		"--memory=" + s.config.MemoryLimit,
		"--cpus=" + s.config.CPULimit,
		"--network=none",
		"-v", tmpFile.Name() + ":/script" + ext + ":ro",
		image,
	}
	args = append(args, strings.Fields(interpreter)...)
	args = append(args, "/script"+ext)

	cmd := exec.CommandContext(execCtx, "docker", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", nil
	}
	if err != nil {
		return result + "\n[exit: " + err.Error() + "]", nil
	}
	return result, nil
}

// RegisterSkills registers sandbox skills with the legacy skill.Registry.
func RegisterSkills(reg *skill.Registry, sb *Sandbox) {
	reg.Register(&skill.Skill{
		Name:        "sandbox_run",
		Description: "Executar comando em container Docker isolado (sandbox)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"command": {Type: jsonschema.String, Description: "Comando a executar"},
				"image":   {Type: jsonschema.String, Description: "Imagem Docker (default: ubuntu:22.04)"},
			},
			Required: []string{"command"},
		},
		Execute: func(args map[string]string) (string, error) {
			return sb.Execute(context.Background(), args["command"], args["image"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "sandbox_script",
		Description: "Executar script em sandbox Docker (python, node, go, bash, ruby)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"script":   {Type: jsonschema.String, Description: "Conteudo do script"},
				"language": {Type: jsonschema.String, Description: "Linguagem: python, node, go, bash, ruby"},
			},
			Required: []string{"script", "language"},
		},
		Execute: func(args map[string]string) (string, error) {
			return sb.ExecuteScript(context.Background(), args["script"], args["language"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "sandbox_available",
		Description: "Verificar se o sandbox Docker esta disponivel",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			if sb.IsAvailable() {
				return fmt.Sprintf("Docker sandbox disponivel.\nImagem padrao: %s\nMemoria: %s\nCPU: %s\nTimeout: %ds\nRede: %v",
					sb.config.Image, sb.config.MemoryLimit, sb.config.CPULimit, sb.config.Timeout, sb.config.Network), nil
			}
			return "Docker sandbox NAO disponivel. Instale o Docker para usar.", nil
		},
	})
}
