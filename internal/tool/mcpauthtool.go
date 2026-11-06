package tool

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// MCPAuthState tracks OAuth state per MCP server (in-memory).
type MCPAuthState struct {
	mu      sync.Mutex
	servers map[string]*MCPServerAuth
}

type MCPServerAuth struct {
	Server    string    `json:"server"`
	Status    string    `json:"status"` // "unauthenticated", "pending", "authenticated", "expired"
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

var globalMCPAuthState = &MCPAuthState{servers: make(map[string]*MCPServerAuth)}

func GetMCPAuthState() *MCPAuthState { return globalMCPAuthState }

func (s *MCPAuthState) Login(server string) *MCPServerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	auth := &MCPServerAuth{
		Server:    server,
		Status:    "authenticated",
		Token:     fmt.Sprintf("mock-token-%d", time.Now().UnixNano()),
		ExpiresAt: time.Now().Add(1 * time.Hour),
		UpdatedAt: time.Now(),
	}
	s.servers[server] = auth
	return auth
}

func (s *MCPAuthState) Logout(server string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[server]; !ok {
		return false
	}
	delete(s.servers, server)
	return true
}

func (s *MCPAuthState) Status(server string) *MCPServerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	auth, ok := s.servers[server]
	if !ok {
		return &MCPServerAuth{Server: server, Status: "unauthenticated"}
	}
	if time.Now().After(auth.ExpiresAt) {
		auth.Status = "expired"
	}
	snapshot := *auth
	return &snapshot
}

func (s *MCPAuthState) List() []*MCPServerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*MCPServerAuth, 0, len(s.servers))
	for _, a := range s.servers {
		snapshot := *a
		out = append(out, &snapshot)
	}
	return out
}

// NewMCPAuthTool creates the mcp_auth tool.
func NewMCPAuthTool() types.ToolDef {
	return types.ToolDef{
		NameFn: func() string { return "mcp_auth" },
		DescriptionFn: func() string {
			return "Manage OAuth authentication for MCP servers. Actions: login, logout, status, list."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"login", "logout", "status", "list"},
						"description": "Action to perform",
					},
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name (required for login/logout/status)",
					},
				},
				"required": []string{"action"},
			}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			a, _ := args["action"].(string)
			return a == "status" || a == "list"
		},
		IsDestructiveFn: func(args map[string]any) bool {
			a, _ := args["action"].(string)
			return a == "logout"
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			action, _ := args["action"].(string)
			server, _ := args["server"].(string)
			state := GetMCPAuthState()

			switch action {
			case "login":
				if server == "" {
					return types.ToolResult{IsError: true, Content: "server is required for login"}, nil
				}
				auth := state.Login(server)
				data, _ := json.Marshal(auth)
				return types.ToolResult{Content: "Authenticated:\n" + string(data)}, nil
			case "logout":
				if server == "" {
					return types.ToolResult{IsError: true, Content: "server is required for logout"}, nil
				}
				if !state.Logout(server) {
					return types.ToolResult{IsError: true, Content: "not authenticated: " + server}, nil
				}
				return types.ToolResult{Content: "Logged out: " + server}, nil
			case "status":
				if server == "" {
					return types.ToolResult{IsError: true, Content: "server is required for status"}, nil
				}
				auth := state.Status(server)
				data, _ := json.Marshal(auth)
				return types.ToolResult{Content: string(data)}, nil
			case "list":
				auths := state.List()
				data, _ := json.Marshal(auths)
				return types.ToolResult{Content: fmt.Sprintf("%d authenticated servers:\n%s", len(auths), string(data))}, nil
			default:
				return types.ToolResult{IsError: true, Content: "unknown action: " + action}, nil
			}
		},
	}
}

func RegisterMCPAuthTool(reg *Registry) {
	reg.RegisterDef(NewMCPAuthTool())
}
