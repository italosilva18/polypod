package config

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// MCPServersConfig maps server name → server definition. This type lives in
// the config package and is deliberately named with a "Config" prefix on its
// element type so it does not collide with the runtime MCP types defined in
// internal/mcp/.
type MCPServersConfig map[string]ConfigMCPServer

// ConfigMCPServer describes a single MCP server entry as it appears in
// settings.json / config.yaml. It covers both stdio-style servers (Command,
// Args, Env) and remote transports (URL, Headers). Type is the transport
// discriminator.
type ConfigMCPServer struct {
	// Type is the transport: "stdio", "sse", "http", "websocket",
	// "streamable-http". Empty defaults to "stdio".
	Type string `yaml:"type,omitempty" json:"type,omitempty"`

	// Stdio fields.
	Command string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Remote fields.
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Timeout is the connection / request timeout in seconds (0 = default).
	Timeout int `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

var validMCPTransports = map[string]struct{}{
	"":                {}, // defaults to stdio
	"stdio":           {},
	"sse":             {},
	"http":            {},
	"websocket":       {},
	"streamable-http": {},
}

// Validate checks the server definition is internally consistent:
//   - Type must be a known transport.
//   - Stdio transports require Command.
//   - Remote transports (sse/http/websocket/streamable-http) require URL.
func (s *ConfigMCPServer) Validate() error {
	if s == nil {
		return nil
	}
	if _, ok := validMCPTransports[s.Type]; !ok {
		return fmt.Errorf("invalid mcp server type %q", s.Type)
	}
	t := s.Type
	if t == "" {
		t = "stdio"
	}
	switch t {
	case "stdio":
		if s.Command == "" {
			return fmt.Errorf("stdio mcp server requires command")
		}
	case "sse", "http", "websocket", "streamable-http":
		if s.URL == "" {
			return fmt.Errorf("%s mcp server requires url", t)
		}
	}
	if s.Timeout < 0 {
		return fmt.Errorf("mcp server timeout must be non-negative")
	}
	return nil
}

// Validate applies ConfigMCPServer.Validate to every entry and returns the
// first error with the server name prefixed.
func (m MCPServersConfig) Validate() error {
	for name, srv := range m {
		if name == "" {
			return fmt.Errorf("mcp server name must not be empty")
		}
		if err := srv.Validate(); err != nil {
			return fmt.Errorf("mcpServers[%q]: %w", name, err)
		}
	}
	return nil
}

// ParseMCPServersYAML unmarshals a YAML document containing either a bare
// map of server definitions or an object with an "mcpServers" key and
// validates the result.
func ParseMCPServersYAML(data []byte) (MCPServersConfig, error) {
	// Try the bare map shape first.
	var direct MCPServersConfig
	if err := yaml.Unmarshal(data, &direct); err == nil && direct != nil {
		// If the map parsed but every entry is empty the user likely passed
		// the wrapped shape; fall through to the wrapper attempt below.
		if hasAnyServer(direct) {
			if err := direct.Validate(); err != nil {
				return nil, err
			}
			return direct, nil
		}
	}
	var wrapper struct {
		MCPServers MCPServersConfig `yaml:"mcpServers"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing mcpServers yaml: %w", err)
	}
	if err := wrapper.MCPServers.Validate(); err != nil {
		return nil, err
	}
	return wrapper.MCPServers, nil
}

// ParseMCPServersJSON is the JSON equivalent of ParseMCPServersYAML.
func ParseMCPServersJSON(data []byte) (MCPServersConfig, error) {
	var direct MCPServersConfig
	if err := json.Unmarshal(data, &direct); err == nil && hasAnyServer(direct) {
		if err := direct.Validate(); err != nil {
			return nil, err
		}
		return direct, nil
	}
	var wrapper struct {
		MCPServers MCPServersConfig `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing mcpServers json: %w", err)
	}
	if err := wrapper.MCPServers.Validate(); err != nil {
		return nil, err
	}
	return wrapper.MCPServers, nil
}

func hasAnyServer(m MCPServersConfig) bool {
	for _, v := range m {
		if v.Command != "" || v.URL != "" || v.Type != "" || len(v.Args) > 0 {
			return true
		}
	}
	return false
}
