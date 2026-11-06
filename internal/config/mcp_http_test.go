package config

import (
	"gopkg.in/yaml.v3"
	"testing"
)

func TestMCPHTTPConfig(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(`mcp:
  - name: polyclaw
    transport: http
    url: http://127.0.0.1:18792/mcp
    headers:
      Authorization: Bearer test-token
    auto_start: false
`), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.MCP) != 1 {
		t.Fatal("missing MCP config")
	}
	server := cfg.MCP[0]
	if server.Transport != "http" || server.URL != "http://127.0.0.1:18792/mcp" || server.Headers["Authorization"] != "Bearer test-token" || server.AutoStart {
		t.Fatal("HTTP config fields lost")
	}
}
