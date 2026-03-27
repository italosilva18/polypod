package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/costa/polypod/internal/skill"
)

// Server exposes Polypod's skills via MCP protocol over stdio.
type Server struct {
	skills *skill.Registry
	logger *slog.Logger
	reader *bufio.Reader
	writer io.Writer
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// New creates an MCP server.
func New(skills *skill.Registry, logger *slog.Logger) *Server {
	return &Server{
		skills: skills,
		logger: logger,
		reader: bufio.NewReader(os.Stdin),
		writer: os.Stdout,
	}
}

// Serve runs the MCP server loop over stdio. Blocks until EOF.
func (s *Server) Serve() error {
	s.logger.Info("MCP server iniciado via stdio")

	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("mcp server read: %w", err)
		}

		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		resp := s.handleRequest(req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			s.writer.Write(data)
		}
	}
}

func (s *Server) handleRequest(req jsonrpcRequest) *jsonrpcResponse {
	switch req.Method {
	case "initialize":
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]string{
					"name":    "polypod",
					"version": "0.4.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			},
		}

	case "notifications/initialized":
		return nil // notification, no response

	case "tools/list":
		tools := s.listTools()
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": tools},
		}

	case "tools/call":
		return s.callTool(req)

	default:
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   jsonrpcError{Code: -32601, Message: fmt.Sprintf("metodo desconhecido: %s", req.Method)},
		}
	}
}

func (s *Server) listTools() []map[string]any {
	var tools []map[string]any
	for _, name := range s.skills.List() {
		sk, ok := s.skills.Get(name)
		if !ok {
			continue
		}
		// Convert jsonschema.Definition to map
		paramBytes, _ := json.Marshal(sk.Parameters)
		var paramMap map[string]any
		json.Unmarshal(paramBytes, &paramMap)

		tools = append(tools, map[string]any{
			"name":        sk.Name,
			"description": sk.Description,
			"inputSchema": paramMap,
		})
	}
	return tools
}

func (s *Server) callTool(req jsonrpcRequest) *jsonrpcResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   jsonrpcError{Code: -32602, Message: "parametros invalidos"},
		}
	}

	// Convert arguments to map[string]string for skill execution
	args := make(map[string]string)
	for k, v := range params.Arguments {
		args[k] = fmt.Sprintf("%v", v)
	}

	argsJSON, _ := json.Marshal(args)
	result, err := s.skills.Execute(params.Name, string(argsJSON))

	if err != nil {
		return &jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{{"type": "text", "text": err.Error()}},
				"isError": true,
			},
		}
	}

	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]string{{"type": "text", "text": result}},
		},
	}
}

// ListToolNames returns a formatted list for display.
func (s *Server) ListToolNames() string {
	names := s.skills.List()
	return fmt.Sprintf("Polypod MCP Server: %d ferramentas disponiveis\n%s",
		len(names), strings.Join(names, ", "))
}
