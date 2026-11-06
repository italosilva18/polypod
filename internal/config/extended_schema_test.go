package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExtendedConfig_AllSections(t *testing.T) {
	yamlBlob := []byte(`
permissions:
  mode: default
  defaultMode: default
  allow:
    - tool: Bash
      pattern: "git *"
  deny:
    - tool: Bash
      pattern: "rm -rf /*"
mcpServers:
  gh:
    type: stdio
    command: gh-mcp
    args: ["serve"]
  linear:
    type: http
    url: https://mcp.linear.app
outputStyle:
  active: concise
  custom:
    terse:
      name: terse
      prompt: Be brief.
env:
  FOO: bar
  BAZ: qux
apiKeyHelper: /usr/local/bin/get-key
disableHooks: true
onlyManagedHooks: false
`)
	base := DefaultConfig()
	ext, err := ParseExtendedConfig(base, yamlBlob)
	if err != nil {
		t.Fatalf("ParseExtendedConfig failed: %v", err)
	}
	if ext.Base != base {
		t.Errorf("Base pointer not preserved")
	}
	if !ext.HasPermissions() || !ext.HasMCPServers() || !ext.HasOutputStyle() || !ext.HasEnv() {
		t.Errorf("expected all sections present, got perms=%v mcp=%v style=%v env=%v",
			ext.HasPermissions(), ext.HasMCPServers(), ext.HasOutputStyle(), ext.HasEnv())
	}
	if len(ext.MCPServers) != 2 {
		t.Errorf("expected 2 mcp servers, got %d", len(ext.MCPServers))
	}
	if ext.OutputStyle.Active != "concise" {
		t.Errorf("expected active=concise, got %q", ext.OutputStyle.Active)
	}
	if ext.Env.Vars["FOO"] != "bar" {
		t.Errorf("expected FOO=bar, got %q", ext.Env.Vars["FOO"])
	}
	if ext.Env.APIKeyHelper != "/usr/local/bin/get-key" {
		t.Errorf("expected apiKeyHelper populated, got %q", ext.Env.APIKeyHelper)
	}
	if !ext.Env.DisableHooks {
		t.Errorf("expected disableHooks=true")
	}
	if ext.PermissionMode() != "default" {
		t.Errorf("expected PermissionMode=default, got %q", ext.PermissionMode())
	}
}

func TestParseExtendedConfig_OnlyPermissions(t *testing.T) {
	yamlBlob := []byte(`
permissions:
  mode: plan
  allow:
    - tool: Read
`)
	ext, err := ParseExtendedConfig(nil, yamlBlob)
	if err != nil {
		t.Fatalf("ParseExtendedConfig failed: %v", err)
	}
	if !ext.HasPermissions() {
		t.Errorf("expected HasPermissions=true")
	}
	if ext.HasMCPServers() || ext.HasOutputStyle() || ext.HasEnv() {
		t.Errorf("expected only permissions, got mcp=%v style=%v env=%v",
			ext.HasMCPServers(), ext.HasOutputStyle(), ext.HasEnv())
	}
	if ext.PermissionMode() != "plan" {
		t.Errorf("expected mode=plan, got %q", ext.PermissionMode())
	}
	if ext.Base != nil {
		t.Errorf("expected nil Base")
	}
}

func TestExtendedConfig_ValidateInvalidMode(t *testing.T) {
	yamlBlob := []byte(`
permissions:
  mode: totally-bogus
`)
	_, err := ParseExtendedConfig(nil, yamlBlob)
	if err == nil {
		t.Fatal("expected validation error for invalid mode, got nil")
	}
	if !strings.Contains(err.Error(), "invalid permissions.mode") {
		t.Errorf("expected invalid mode error, got %v", err)
	}
}

func TestExtendedConfig_ApplyEnv(t *testing.T) {
	const key = "POLYPOD_EXTENDED_TEST_VAR"
	_ = os.Unsetenv(key)
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	yamlBlob := []byte(`
env:
  POLYPOD_EXTENDED_TEST_VAR: hello
`)
	ext, err := ParseExtendedConfig(nil, yamlBlob)
	if err != nil {
		t.Fatalf("ParseExtendedConfig failed: %v", err)
	}
	if !ext.HasEnv() {
		t.Fatal("expected env section")
	}
	if err := ext.Apply(); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if got := os.Getenv(key); got != "hello" {
		t.Errorf("expected POLYPOD_EXTENDED_TEST_VAR=hello, got %q", got)
	}
}

func TestExtendedConfig_HasFlags(t *testing.T) {
	empty, err := ParseExtendedConfig(nil, []byte(""))
	if err != nil {
		t.Fatalf("ParseExtendedConfig(empty) failed: %v", err)
	}
	if empty.HasPermissions() || empty.HasMCPServers() || empty.HasOutputStyle() || empty.HasEnv() {
		t.Errorf("empty config should have no sections, got perms=%v mcp=%v style=%v env=%v",
			empty.HasPermissions(), empty.HasMCPServers(), empty.HasOutputStyle(), empty.HasEnv())
	}
	if empty.PermissionMode() != "default" {
		t.Errorf("expected default mode for empty config, got %q", empty.PermissionMode())
	}

	// Nil receiver safety
	var nilExt *ExtendedConfig
	if nilExt.HasPermissions() || nilExt.HasMCPServers() || nilExt.HasOutputStyle() || nilExt.HasEnv() {
		t.Errorf("nil ExtendedConfig should report no sections")
	}
	if nilExt.PermissionMode() != "default" {
		t.Errorf("nil ExtendedConfig PermissionMode should be 'default'")
	}
	if err := nilExt.Validate(); err != nil {
		t.Errorf("nil Validate should not error, got %v", err)
	}
	if err := nilExt.Apply(); err != nil {
		t.Errorf("nil Apply should not error, got %v", err)
	}
}

func TestLoadExtendedFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extended.yaml")
	content := []byte(`
permissions:
  mode: acceptEdits
outputStyle:
  active: verbose
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	base := DefaultConfig()
	ext, err := LoadExtendedFromFile(base, path)
	if err != nil {
		t.Fatalf("LoadExtendedFromFile failed: %v", err)
	}
	if ext.Base != base {
		t.Errorf("Base pointer not preserved through file load")
	}
	if ext.PermissionMode() != "acceptEdits" {
		t.Errorf("expected acceptEdits, got %q", ext.PermissionMode())
	}
	if ext.OutputStyle == nil || ext.OutputStyle.Active != "verbose" {
		t.Errorf("expected outputStyle.active=verbose")
	}

	// Missing file should error.
	if _, err := LoadExtendedFromFile(base, filepath.Join(dir, "nope.yaml")); err == nil {
		t.Errorf("expected error for missing file")
	}
}
