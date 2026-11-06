package config

import (
	"os"
	"testing"
)

func TestParsePermissionsYAML(t *testing.T) {
	yamlDoc := []byte(`
mode: plan
defaultMode: default
allow:
  - tool: Bash
    pattern: "git *"
    reason: "read-only git"
deny:
  - tool: Bash
    pattern: "rm -rf *"
    reason: "destructive"
ask:
  - tool: Write
additionalDirectories:
  - /tmp/work
`)
	got, err := ParsePermissionsYAML(yamlDoc)
	if err != nil {
		t.Fatalf("ParsePermissionsYAML: %v", err)
	}
	if got.Mode != "plan" {
		t.Errorf("Mode: got %q want %q", got.Mode, "plan")
	}
	if got.DefaultMode != "default" {
		t.Errorf("DefaultMode: got %q want %q", got.DefaultMode, "default")
	}
	if len(got.AllowRules) != 1 || got.AllowRules[0].Tool != "Bash" {
		t.Errorf("AllowRules: %+v", got.AllowRules)
	}
	if len(got.DenyRules) != 1 || got.DenyRules[0].Pattern != "rm -rf *" {
		t.Errorf("DenyRules: %+v", got.DenyRules)
	}
	if len(got.AskRules) != 1 || got.AskRules[0].Tool != "Write" {
		t.Errorf("AskRules: %+v", got.AskRules)
	}
	if len(got.AdditionalDirectories) != 1 {
		t.Errorf("AdditionalDirectories: %+v", got.AdditionalDirectories)
	}

	// JSON round-trip should also work.
	jsonDoc := []byte(`{"mode":"acceptEdits","allow":[{"tool":"Read"}]}`)
	if _, err := ParsePermissionsJSON(jsonDoc); err != nil {
		t.Errorf("ParsePermissionsJSON: %v", err)
	}

	// Invalid mode must fail validation.
	bad := []byte(`mode: totally-made-up`)
	if _, err := ParsePermissionsYAML(bad); err == nil {
		t.Error("expected error for invalid mode, got nil")
	}

	// Rule without tool must fail.
	badRule := []byte(`
allow:
  - pattern: "echo *"
`)
	if _, err := ParsePermissionsYAML(badRule); err == nil {
		t.Error("expected error for rule without tool, got nil")
	}
}

func TestParseMCPServersYAML(t *testing.T) {
	yamlDoc := []byte(`
mcpServers:
  local-fs:
    type: stdio
    command: /usr/local/bin/fs-server
    args: ["--root", "/home"]
    env:
      LOG_LEVEL: debug
    timeout: 30
  remote-http:
    type: http
    url: https://mcp.example.com/v1
    headers:
      Authorization: Bearer xyz
`)
	got, err := ParseMCPServersYAML(yamlDoc)
	if err != nil {
		t.Fatalf("ParseMCPServersYAML: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(got))
	}
	local := got["local-fs"]
	if local.Command != "/usr/local/bin/fs-server" || len(local.Args) != 2 {
		t.Errorf("local-fs: %+v", local)
	}
	if local.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("local-fs env: %+v", local.Env)
	}
	remote := got["remote-http"]
	if remote.URL == "" || remote.Headers["Authorization"] == "" {
		t.Errorf("remote-http: %+v", remote)
	}

	// Bare-map shape also parses.
	bare := []byte(`
echo:
  type: stdio
  command: /bin/echo
`)
	if _, err := ParseMCPServersYAML(bare); err != nil {
		t.Errorf("ParseMCPServersYAML (bare): %v", err)
	}

	// stdio without command must fail.
	bad := []byte(`
mcpServers:
  broken:
    type: stdio
`)
	if _, err := ParseMCPServersYAML(bad); err == nil {
		t.Error("expected error for stdio without command, got nil")
	}

	// http without url must fail.
	bad2 := []byte(`
mcpServers:
  broken:
    type: http
`)
	if _, err := ParseMCPServersYAML(bad2); err == nil {
		t.Error("expected error for http without url, got nil")
	}

	// Unknown transport must fail.
	bad3 := []byte(`
mcpServers:
  broken:
    type: carrier-pigeon
    command: /bin/true
`)
	if _, err := ParseMCPServersYAML(bad3); err == nil {
		t.Error("expected error for unknown transport, got nil")
	}
}

func TestParseOutputStyleYAML(t *testing.T) {
	// Structured form.
	yamlDoc := []byte(`
active: concise
custom:
  terse:
    name: Terse
    prompt: "Answer with the fewest words possible."
    description: "Ultra-short replies"
`)
	got, err := ParseOutputStyleYAML(yamlDoc)
	if err != nil {
		t.Fatalf("ParseOutputStyleYAML: %v", err)
	}
	if got.Active != "concise" {
		t.Errorf("Active: got %q want %q", got.Active, "concise")
	}
	if _, ok := got.Custom["terse"]; !ok {
		t.Errorf("Custom[terse] missing: %+v", got.Custom)
	}

	// Scalar (string) shape.
	scalar := []byte(`"default"`)
	scalarGot, err := ParseOutputStyleYAML(scalar)
	if err != nil {
		t.Fatalf("ParseOutputStyleYAML (scalar): %v", err)
	}
	if scalarGot.Active != "default" {
		t.Errorf("scalar Active: got %q want %q", scalarGot.Active, "default")
	}

	// DefaultOutputStyle sanity check.
	d := DefaultOutputStyle()
	if d.Active != "default" || d.Custom == nil {
		t.Errorf("DefaultOutputStyle: %+v", d)
	}

	// Invalid custom entry (missing prompt) must fail.
	bad := []byte(`
active: broken
custom:
  broken:
    name: Broken
`)
	if _, err := ParseOutputStyleYAML(bad); err == nil {
		t.Error("expected error for custom style without prompt, got nil")
	}
}

func TestParseEnvYAML(t *testing.T) {
	yamlDoc := []byte(`
env:
  POLYPOD_TEST_VAR: "hello world"
  POLYPOD_OTHER: "1"
apiKeyHelper: /usr/local/bin/get-key
disableHooks: true
onlyManagedHooks: false
`)
	got, err := ParseEnvYAML(yamlDoc)
	if err != nil {
		t.Fatalf("ParseEnvYAML: %v", err)
	}
	if got.Vars["POLYPOD_TEST_VAR"] != "hello world" {
		t.Errorf("Vars: %+v", got.Vars)
	}
	if got.APIKeyHelper != "/usr/local/bin/get-key" {
		t.Errorf("APIKeyHelper: %q", got.APIKeyHelper)
	}
	if !got.DisableHooks {
		t.Errorf("DisableHooks: %v", got.DisableHooks)
	}
	if got.OnlyManagedHooks {
		t.Errorf("OnlyManagedHooks: %v", got.OnlyManagedHooks)
	}

	// Apply exports into the current process.
	if err := got.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if os.Getenv("POLYPOD_TEST_VAR") != "hello world" {
		t.Errorf("Apply did not export POLYPOD_TEST_VAR: %q", os.Getenv("POLYPOD_TEST_VAR"))
	}
	// Clean up to avoid leaking into sibling tests.
	_ = os.Unsetenv("POLYPOD_TEST_VAR")
	_ = os.Unsetenv("POLYPOD_OTHER")

	// Empty variable name must fail validation. Build the struct directly
	// because YAML with an empty key is awkward to express.
	bad := &EnvConfig{Vars: map[string]string{"": "oops"}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty env var name, got nil")
	}
}
