package sandbox

import (
	"path/filepath"
	"testing"
)

// TestNewSandboxExecutor_AllPolicies verifies the constructor handles every policy.
func TestNewSandboxExecutor_AllPolicies(t *testing.T) {
	policies := []SandboxPolicy{SandboxNone, SandboxReadOnly, SandboxWorkspace, SandboxFull}
	for _, p := range policies {
		cfg := SandboxConfig{Policy: p, WorkspaceDir: "/tmp/work"}
		e := NewSandboxExecutor(cfg)
		if e == nil {
			t.Fatalf("NewSandboxExecutor(%v) returned nil", p)
		}
		if e.Policy() != p {
			t.Errorf("Policy() = %v, want %v", e.Policy(), p)
		}
	}
}

// TestNewSandboxExecutor_Defaults checks that empty fields are populated with sane defaults.
func TestNewSandboxExecutor_Defaults(t *testing.T) {
	e := NewSandboxExecutor(SandboxConfig{Policy: SandboxWorkspace})
	cfg := e.Config()

	if cfg.Image != "ubuntu:22.04" {
		t.Errorf("default Image = %q, want %q", cfg.Image, "ubuntu:22.04")
	}
	if cfg.TimeoutSecs != 120 {
		t.Errorf("default TimeoutSecs = %d, want 120", cfg.TimeoutSecs)
	}
	if cfg.MaxMemoryMB != 512 {
		t.Errorf("default MaxMemoryMB = %d, want 512", cfg.MaxMemoryMB)
	}
	if cfg.MaxCPUPercent != 200 {
		t.Errorf("default MaxCPUPercent = %d, want 200", cfg.MaxCPUPercent)
	}
	if cfg.WorkspaceDir == "" {
		t.Error("default WorkspaceDir should be set (os.Getwd fallback)")
	}
}

// TestDefaultSandboxConfig verifies the factory returns expected safe defaults.
func TestDefaultSandboxConfig(t *testing.T) {
	cfg := DefaultSandboxConfig("/home/user/proj")

	if cfg.Policy != SandboxWorkspace {
		t.Errorf("Policy = %v, want SandboxWorkspace", cfg.Policy)
	}
	if cfg.WorkspaceDir != "/home/user/proj" {
		t.Errorf("WorkspaceDir = %q, want /home/user/proj", cfg.WorkspaceDir)
	}
	if !cfg.AllowNetwork {
		t.Error("AllowNetwork should default to true")
	}
	if cfg.MaxMemoryMB <= 0 {
		t.Error("MaxMemoryMB should be positive")
	}
	if cfg.TimeoutSecs <= 0 {
		t.Error("TimeoutSecs should be positive")
	}
	if cfg.Image == "" {
		t.Error("Image should be set")
	}
}

// TestValidatePath_AllowedWorkspace checks that paths inside the workspace are accepted.
func TestValidatePath_AllowedWorkspace(t *testing.T) {
	workspace, _ := filepath.Abs("/tmp/work")
	cfg := SandboxConfig{
		Policy:       SandboxWorkspace,
		WorkspaceDir: workspace,
	}

	if err := ValidatePath(filepath.Join(workspace, "file.go"), SandboxWorkspace, cfg); err != nil {
		t.Errorf("ValidatePath on allowed workspace path: %v", err)
	}
	if err := ValidatePath(filepath.Join(workspace, "sub", "deep.txt"), SandboxWorkspace, cfg); err != nil {
		t.Errorf("ValidatePath on nested workspace path: %v", err)
	}
}

// TestValidatePath_BlockedSensitivePaths ensures sensitive system files are rejected.
func TestValidatePath_BlockedSensitivePaths(t *testing.T) {
	cfg := SandboxConfig{
		Policy:       SandboxWorkspace,
		WorkspaceDir: "/tmp/work",
	}

	blocked := []string{
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers",
		"/root/.bashrc",
		"/bin/ls",
		"/usr/bin/env",
		"/sys/kernel",
		"/proc/self",
	}

	for _, path := range blocked {
		if err := ValidatePath(path, SandboxWorkspace, cfg); err == nil {
			t.Errorf("ValidatePath(%q) should fail under SandboxWorkspace", path)
		}
	}
}

// TestValidatePath_OutsideWorkspace ensures writes outside workspace are rejected.
func TestValidatePath_OutsideWorkspace(t *testing.T) {
	cfg := SandboxConfig{
		Policy:       SandboxWorkspace,
		WorkspaceDir: "/tmp/work",
	}

	// /home/other is outside the workspace, not in /tmp, not sensitive.
	if err := ValidatePath("/home/other/file.txt", SandboxWorkspace, cfg); err == nil {
		t.Error("ValidatePath outside workspace should fail")
	}
}

// TestValidatePath_AllowedPaths ensures explicitly allowed paths are permitted.
func TestValidatePath_AllowedPaths(t *testing.T) {
	cfg := SandboxConfig{
		Policy:       SandboxWorkspace,
		WorkspaceDir: "/tmp/work",
		AllowedPaths: []string{"/opt/data"},
	}

	if err := ValidatePath("/opt/data/result.json", SandboxWorkspace, cfg); err != nil {
		t.Errorf("ValidatePath on AllowedPaths entry: %v", err)
	}
}

// TestValidatePath_DeniedPaths ensures explicitly denied paths are blocked.
func TestValidatePath_DeniedPaths(t *testing.T) {
	cfg := SandboxConfig{
		Policy:       SandboxWorkspace,
		WorkspaceDir: "/tmp/work",
		DeniedPaths:  []string{"/tmp/work/secrets"},
	}

	if err := ValidatePath("/tmp/work/secrets/key.pem", SandboxWorkspace, cfg); err == nil {
		t.Error("ValidatePath on DeniedPaths entry should fail")
	}
}

// TestValidatePath_NonePolicy ensures SandboxNone allows everything.
func TestValidatePath_NonePolicy(t *testing.T) {
	cfg := SandboxConfig{Policy: SandboxNone}

	paths := []string{"/etc/passwd", "/bin/ls", "/home/anything"}
	for _, p := range paths {
		if err := ValidatePath(p, SandboxNone, cfg); err != nil {
			t.Errorf("ValidatePath(%q) under SandboxNone: %v", p, err)
		}
	}
}

// TestValidatePath_ReadOnlyBlocksWrites ensures ReadOnly policy denies writes.
func TestValidatePath_ReadOnlyBlocksWrites(t *testing.T) {
	cfg := SandboxConfig{
		Policy:       SandboxReadOnly,
		WorkspaceDir: "/tmp/work",
	}

	if err := ValidatePath("/tmp/work/file.go", SandboxReadOnly, cfg); err == nil {
		t.Error("ValidatePath should fail for any write under SandboxReadOnly")
	}
}

// TestValidatePathRead ensures read validation only blocks the most sensitive files.
func TestValidatePathRead(t *testing.T) {
	cfg := SandboxConfig{Policy: SandboxWorkspace}

	// Regular reads are allowed.
	if err := ValidatePathRead("/tmp/work/file.go", SandboxWorkspace, cfg); err != nil {
		t.Errorf("ValidatePathRead on normal path: %v", err)
	}
	// /etc/passwd is NOT in sensitiveReadPaths — it's only write-blocked.
	if err := ValidatePathRead("/etc/passwd", SandboxWorkspace, cfg); err != nil {
		t.Errorf("ValidatePathRead on /etc/passwd: %v", err)
	}
	// /etc/shadow IS read-blocked.
	if err := ValidatePathRead("/etc/shadow", SandboxWorkspace, cfg); err == nil {
		t.Error("ValidatePathRead on /etc/shadow should fail")
	}
	// SandboxNone allows everything for reads.
	if err := ValidatePathRead("/etc/shadow", SandboxNone, cfg); err != nil {
		t.Errorf("ValidatePathRead under SandboxNone: %v", err)
	}
}

// TestIsNetworkAllowed_NetworkCommands verifies network-accessing commands
// cause IsNetworkAllowed to return false.
func TestIsNetworkAllowed_NetworkCommands(t *testing.T) {
	networkCmds := []string{
		"curl https://example.com",
		"wget http://x.com/file",
		"ssh user@host",
		"scp file host:/tmp/",
		"ping 8.8.8.8",
		"nc host 80",
	}

	for _, cmd := range networkCmds {
		if IsNetworkAllowed(cmd) {
			t.Errorf("IsNetworkAllowed(%q) = true, want false (network command)", cmd)
		}
	}
}

// TestIsNetworkAllowed_LocalCommands verifies non-network commands return true.
func TestIsNetworkAllowed_LocalCommands(t *testing.T) {
	localCmds := []string{
		"ls -la",
		"cat file.txt",
		"go build ./...",
		"echo hello",
		"grep pattern file",
	}

	for _, cmd := range localCmds {
		if !IsNetworkAllowed(cmd) {
			t.Errorf("IsNetworkAllowed(%q) = false, want true (local command)", cmd)
		}
	}
}

// TestIsNetworkAllowed_URLPatterns verifies URL patterns in commands are detected.
func TestIsNetworkAllowed_URLPatterns(t *testing.T) {
	cmds := []string{
		"echo https://example.com",
		"git clone ssh://host/repo.git",
		"somecmd ftp://server/path",
	}

	for _, cmd := range cmds {
		if IsNetworkAllowed(cmd) {
			t.Errorf("IsNetworkAllowed(%q) = true, want false (URL pattern)", cmd)
		}
	}
}

// TestIsNetworkAllowed_SudoPrefix verifies sudo-prefixed network commands are detected.
func TestIsNetworkAllowed_SudoPrefix(t *testing.T) {
	if IsNetworkAllowed("sudo curl https://example.com") {
		t.Error("IsNetworkAllowed should detect curl after sudo")
	}
	if IsNetworkAllowed("FOO=bar curl example.com") {
		t.Error("IsNetworkAllowed should detect curl after env var assignment")
	}
}

// TestLegacyConfig_Defaults verifies the legacy Config/Sandbox defaults.
func TestLegacyConfig_Defaults(t *testing.T) {
	sb := New(Config{}, nil)
	if sb == nil {
		t.Fatal("New returned nil")
	}
	if sb.config.Image != "ubuntu:22.04" {
		t.Errorf("default Image = %q", sb.config.Image)
	}
	if sb.config.MemoryLimit != "256m" {
		t.Errorf("default MemoryLimit = %q", sb.config.MemoryLimit)
	}
	if sb.config.CPULimit != "1" {
		t.Errorf("default CPULimit = %q", sb.config.CPULimit)
	}
	if sb.config.Timeout != 30 {
		t.Errorf("default Timeout = %d", sb.config.Timeout)
	}
}
