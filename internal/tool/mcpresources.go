package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/costa/polypod/internal/types"
)

// MCPResource represents a resource exposed by an MCP server.
type MCPResource struct {
	Server   string            `json:"server"`
	URI      string            `json:"uri"`
	Name     string            `json:"name"`
	MIME     string            `json:"mime,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Contents []byte            `json:"-"`
}

// MCPResourceStore manages resources in memory.
type MCPResourceStore struct {
	mu        sync.Mutex
	resources map[string]*MCPResource // key = server + ":" + uri
	backend   MCPResourceBackend
}

// MCPResourceBackend supplies the live MCP resource catalog and typed content.
type MCPResourceBackend interface {
	ListResources(server string) map[string][]*mcpsdk.Resource
	ReadResource(context.Context, string, string) (*mcpsdk.ReadResourceResult, error)
}

var globalMCPResourceStore = &MCPResourceStore{resources: make(map[string]*MCPResource)}

// GetMCPResourceStore returns the global MCP resource store.
func GetMCPResourceStore() *MCPResourceStore { return globalMCPResourceStore }

// SetBackend connects the resource tools to the active MCP manager.
func (s *MCPResourceStore) SetBackend(backend MCPResourceBackend) {
	s.mu.Lock()
	s.backend = backend
	s.mu.Unlock()
}

func (s *MCPResourceStore) getBackend() MCPResourceBackend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend
}

// Register adds or replaces a resource in the store.
func (s *MCPResourceStore) Register(r *MCPResource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := r.Server + ":" + r.URI
	s.resources[key] = r
}

// List returns resources, optionally filtered by server. Contents are stripped.
func (s *MCPResourceStore) List(server string) []*MCPResource {
	if backend := s.getBackend(); backend != nil {
		catalog := backend.ListResources(server)
		out := make([]*MCPResource, 0)
		for serverName, resources := range catalog {
			for _, item := range resources {
				if item != nil {
					out = append(out, &MCPResource{
						Server: serverName, URI: item.URI, Name: item.Name, MIME: item.MIMEType,
						Metadata: map[string]string{"description": item.Description},
					})
				}
			}
		}
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*MCPResource, 0)
	for _, r := range s.resources {
		if server == "" || r.Server == server {
			cp := *r
			cp.Contents = nil
			out = append(out, &cp)
		}
	}
	return out
}

// Get returns a copy of a specific resource.
func (s *MCPResourceStore) Get(server, uri string) (*MCPResource, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := server + ":" + uri
	r, ok := s.resources[key]
	if !ok {
		return nil, false
	}
	cp := *r
	return &cp, true
}

// NewListMCPResourcesTool lists available MCP resources.
func NewListMCPResourcesTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "list_mcp_resources" },
		DescriptionFn: func() string { return "List available resources from connected MCP servers. Optional server filter." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "Optional server name to filter",
					},
				},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			server, _ := args["server"].(string)
			resources := GetMCPResourceStore().List(server)
			data, _ := json.Marshal(resources)
			return types.ToolResult{Content: fmt.Sprintf("%d resources:\n%s", len(resources), string(data))}, nil
		},
	}
}

// NewReadMCPResourceTool reads a specific MCP resource.
func NewReadMCPResourceTool() types.ToolDef {
	return types.ToolDef{
		NameFn:        func() string { return "read_mcp_resource" },
		DescriptionFn: func() string { return "Read the contents of a specific MCP resource by URI." },
		InputSchemaFn: func() map[string]any {
			return map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server": map[string]any{
						"type":        "string",
						"description": "MCP server name",
					},
					"uri": map[string]any{
						"type":        "string",
						"description": "Resource URI to read",
					},
				},
				"required": []string{"server", "uri"},
			}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsDestructiveFn: func(map[string]any) bool { return false },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			server, _ := args["server"].(string)
			uri, _ := args["uri"].(string)
			if server == "" || uri == "" {
				return types.ToolResult{IsError: true, Content: "server and uri are required"}, nil
			}
			if backend := GetMCPResourceStore().getBackend(); backend != nil {
				requestCtx := ctx.Context
				if requestCtx == nil {
					requestCtx = context.Background()
				}
				result, err := backend.ReadResource(requestCtx, server, uri)
				if err != nil {
					return types.ToolResult{IsError: true, Content: err.Error()}, nil
				}
				data, err := json.Marshal(result.Contents)
				if err != nil {
					return types.ToolResult{IsError: true, Content: fmt.Sprintf("encode MCP resource: %v", err)}, nil
				}
				return types.ToolResult{Content: string(data)}, nil
			}
			r, ok := GetMCPResourceStore().Get(server, uri)
			if !ok {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("resource not found: %s:%s", server, uri)}, nil
			}
			result := map[string]any{
				"server":   r.Server,
				"uri":      r.URI,
				"name":     r.Name,
				"mime":     r.MIME,
				"metadata": r.Metadata,
				"contents": string(r.Contents),
			}
			data, _ := json.Marshal(result)
			return types.ToolResult{Content: string(data)}, nil
		},
	}
}

// RegisterMCPResourceTools registers both MCP resource tools.
func RegisterMCPResourceTools(reg *Registry) {
	reg.RegisterDef(NewListMCPResourcesTool())
	reg.RegisterDef(NewReadMCPResourceTool())
}
