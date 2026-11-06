package mcputil

import (
	"fmt"
	"regexp"
	"strings"
)

const mcpPrefix = "mcp_"

// MCPServerConfig holds configuration for an MCP server connection.
type MCPServerConfig struct {
	Type    string            // "stdio" or "sse"
	Command string            // Command to run (for stdio type)
	Args    []string          // Arguments for the command
	URL     string            // URL to connect to (for sse type)
	Env     map[string]string // Environment variables to set
}

// NormalizeMCPToolName creates a normalized tool name from a server name and
// tool name, using the format "mcp_<server>_<tool>". Special characters
// in both names are replaced with underscores.
func NormalizeMCPToolName(serverName, toolName string) string {
	server := SanitizeServerName(serverName)
	tool := sanitizeName(toolName)
	return mcpPrefix + server + "_" + tool
}

// ParseMCPToolName extracts the server and tool names from a normalized MCP
// tool name. Returns ok=false if the name doesn't follow the expected format.
func ParseMCPToolName(name string) (server, tool string, ok bool) {
	if !IsMCPTool(name) {
		return "", "", false
	}
	// Strip the "mcp_" prefix
	rest := name[len(mcpPrefix):]
	// Split on first underscore to get server_tool
	idx := strings.Index(rest, "_")
	if idx < 0 {
		// Only server name, no tool name
		return rest, "", false
	}
	server = rest[:idx]
	tool = rest[idx+1:]
	if server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// IsMCPTool returns true if the tool name starts with the "mcp_" prefix,
// indicating it is a proxy tool for an MCP server.
func IsMCPTool(name string) bool {
	return strings.HasPrefix(name, mcpPrefix)
}

// BuildMCPToolDescription creates a formatted description for an MCP tool,
// including the originating server name and tool name for clarity.
func BuildMCPToolDescription(serverName, toolName, description string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[MCP: %s/%s] ", serverName, toolName))
	sb.WriteString(description)
	return sb.String()
}

// ValidateMCPConfig checks that an MCPServerConfig has the required fields
// for its type. Returns an error describing the first validation failure,
// or nil if the config is valid.
func ValidateMCPConfig(cfg MCPServerConfig) error {
	switch cfg.Type {
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("stdio MCP server requires a command")
		}
	case "sse":
		if cfg.URL == "" {
			return fmt.Errorf("sse MCP server requires a URL")
		}
		if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
			return fmt.Errorf("sse MCP server URL must start with http:// or https://")
		}
	case "":
		return fmt.Errorf("MCP server type is required (stdio or sse)")
	default:
		return fmt.Errorf("unknown MCP server type: %q (expected stdio or sse)", cfg.Type)
	}
	return nil
}

// validNameRe matches characters allowed in sanitized names: alphanumerics and underscores.
var validNameRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// SanitizeServerName removes special characters from a server name,
// replacing them with underscores and collapsing consecutive underscores.
// The result is lowercased and trimmed of leading/trailing underscores.
func SanitizeServerName(name string) string {
	return sanitizeName(name)
}

// sanitizeName is the internal implementation shared by SanitizeServerName
// and NormalizeMCPToolName for consistent sanitization.
func sanitizeName(name string) string {
	// Replace special characters with underscore
	sanitized := validNameRe.ReplaceAllString(name, "_")
	// Lowercase
	sanitized = strings.ToLower(sanitized)
	// Collapse consecutive underscores
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}
	// Trim leading/trailing underscores
	sanitized = strings.Trim(sanitized, "_")
	return sanitized
}
