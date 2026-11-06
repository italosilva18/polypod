package mcputil

import (
	"strings"
	"testing"
)

func TestNormalizeMCPToolName(t *testing.T) {
	got := NormalizeMCPToolName("github", "search")
	want := "mcp_github_search"
	if got != want {
		t.Errorf("NormalizeMCPToolName(github, search) = %q; want %q", got, want)
	}

	// Sanitization should be applied
	got = NormalizeMCPToolName("GitHub Server", "do-thing")
	if !strings.HasPrefix(got, "mcp_") {
		t.Errorf("expected mcp_ prefix, got %q", got)
	}
	if strings.Contains(got, " ") || strings.Contains(got, "-") {
		t.Errorf("expected sanitized name, got %q", got)
	}
}

func TestParseMCPToolNameRoundtrip(t *testing.T) {
	// Roundtrip only works reliably when server name has no underscores
	// because ParseMCPToolName splits on first underscore after prefix.
	cases := []struct {
		server, tool string
	}{
		{"github", "search"},
		{"fs", "readfile"},
		{"db", "query"},
	}
	for _, tc := range cases {
		normalized := NormalizeMCPToolName(tc.server, tc.tool)
		server, tool, ok := ParseMCPToolName(normalized)
		if !ok {
			t.Errorf("ParseMCPToolName(%q) ok=false", normalized)
			continue
		}
		if server != tc.server {
			t.Errorf("server = %q; want %q (from %q)", server, tc.server, normalized)
		}
		if tool != tc.tool {
			t.Errorf("tool = %q; want %q (from %q)", tool, tc.tool, normalized)
		}
	}
}

func TestParseMCPToolNameInvalid(t *testing.T) {
	cases := []string{
		"read_file",      // no mcp_ prefix
		"",               // empty
		"mcp_",           // just prefix
		"mcp_onlyserver", // no tool part (no underscore after prefix)
		"mcp__tool",      // empty server
	}
	for _, name := range cases {
		if _, _, ok := ParseMCPToolName(name); ok {
			t.Errorf("ParseMCPToolName(%q) ok=true; want false", name)
		}
	}
}

func TestIsMCPTool(t *testing.T) {
	normalized := NormalizeMCPToolName("server", "tool")
	if !IsMCPTool(normalized) {
		t.Errorf("IsMCPTool(%q) = false; want true", normalized)
	}
	if IsMCPTool("read_file") {
		t.Error("IsMCPTool(read_file) should return false")
	}
	if IsMCPTool("") {
		t.Error("IsMCPTool(empty) should return false")
	}
	if !IsMCPTool("mcp_anything") {
		t.Error("IsMCPTool(mcp_anything) should return true")
	}
}

func TestBuildMCPToolDescription(t *testing.T) {
	got := BuildMCPToolDescription("github", "search", "Search repositories")
	if !strings.Contains(got, "github") {
		t.Errorf("description %q missing server name", got)
	}
	if !strings.Contains(got, "search") {
		t.Errorf("description %q missing tool name", got)
	}
	if !strings.Contains(got, "Search repositories") {
		t.Errorf("description %q missing original description", got)
	}
	if !strings.Contains(got, "MCP") {
		t.Errorf("description %q missing MCP marker", got)
	}
}

func TestValidateMCPConfigValid(t *testing.T) {
	valid := []MCPServerConfig{
		{Type: "stdio", Command: "node", Args: []string{"server.js"}},
		{Type: "sse", URL: "https://example.com/sse"},
		{Type: "sse", URL: "http://localhost:8080/sse"},
	}
	for i, cfg := range valid {
		if err := ValidateMCPConfig(cfg); err != nil {
			t.Errorf("case %d: ValidateMCPConfig(%+v) = %v; want nil", i, cfg, err)
		}
	}
}

func TestValidateMCPConfigInvalid(t *testing.T) {
	cases := []struct {
		name string
		cfg  MCPServerConfig
	}{
		{"missing type", MCPServerConfig{}},
		{"stdio without command", MCPServerConfig{Type: "stdio"}},
		{"sse without URL", MCPServerConfig{Type: "sse"}},
		{"sse with bad URL scheme", MCPServerConfig{Type: "sse", URL: "ftp://example.com"}},
		{"unknown type", MCPServerConfig{Type: "websocket", Command: "x"}},
	}
	for _, tc := range cases {
		if err := ValidateMCPConfig(tc.cfg); err == nil {
			t.Errorf("%s: ValidateMCPConfig(%+v) = nil; want error", tc.name, tc.cfg)
		}
	}
}

func TestSanitizeServerName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"github", "github"},
		{"GitHub", "github"},
		{"GitHub Server", "github_server"},
		{"server-name", "server_name"},
		{"my.server", "my_server"},
		{"___name___", "name"},
		{"a!!!b", "a_b"},
		{"CamelCase", "camelcase"},
	}

	for _, tt := range tests {
		got := SanitizeServerName(tt.input)
		if got != tt.want {
			t.Errorf("SanitizeServerName(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestMCPServerConfigConstruction(t *testing.T) {
	cfg := MCPServerConfig{
		Type:    "stdio",
		Command: "python",
		Args:    []string{"-m", "server"},
		URL:     "",
		Env:     map[string]string{"KEY": "value", "DEBUG": "1"},
	}

	if cfg.Type != "stdio" {
		t.Errorf("Type = %q; want stdio", cfg.Type)
	}
	if cfg.Command != "python" {
		t.Errorf("Command = %q; want python", cfg.Command)
	}
	if len(cfg.Args) != 2 {
		t.Errorf("len(Args) = %d; want 2", len(cfg.Args))
	}
	if cfg.Args[0] != "-m" || cfg.Args[1] != "server" {
		t.Errorf("Args = %v; want [-m server]", cfg.Args)
	}
	if cfg.Env["KEY"] != "value" {
		t.Errorf("Env[KEY] = %q; want value", cfg.Env["KEY"])
	}
	if len(cfg.Env) != 2 {
		t.Errorf("len(Env) = %d; want 2", len(cfg.Env))
	}

	// Validate should pass
	if err := ValidateMCPConfig(cfg); err != nil {
		t.Errorf("ValidateMCPConfig = %v; want nil", err)
	}
}
