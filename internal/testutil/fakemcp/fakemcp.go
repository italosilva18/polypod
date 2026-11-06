// Package fakemcp provides a minimal in-process MCP server for tests.
package fakemcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// JSON-RPC message types (minimal subset matching MCP protocol).

// Request is a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ToolDef defines a tool the server exposes.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
	Handler     func(args map[string]any) (any, error)
}

// ResourceDef defines a resource.
type ResourceDef struct {
	URI      string
	Name     string
	MIME     string
	Contents []byte
}

// PromptDef defines a prompt template.
type PromptDef struct {
	Name        string
	Description string
	Template    string
}

// Server is a fake MCP server.
type Server struct {
	mu        sync.Mutex
	tools     map[string]*ToolDef
	resources map[string]*ResourceDef
	prompts   map[string]*PromptDef

	initialized bool
	stopCh      chan struct{}

	// Tracking for assertions
	requestLog []string
}

// NewServer creates a new fake MCP server.
func NewServer() *Server {
	return &Server{
		tools:     make(map[string]*ToolDef),
		resources: make(map[string]*ResourceDef),
		prompts:   make(map[string]*PromptDef),
		stopCh:    make(chan struct{}),
	}
}

// AddTool registers a tool.
func (s *Server) AddTool(name string, schema map[string]any, handler func(args map[string]any) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[name] = &ToolDef{
		Name:    name,
		Schema:  schema,
		Handler: handler,
	}
}

// AddToolWithDescription adds a tool with a description.
func (s *Server) AddToolWithDescription(name, description string, schema map[string]any, handler func(args map[string]any) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[name] = &ToolDef{
		Name:        name,
		Description: description,
		Schema:      schema,
		Handler:     handler,
	}
}

// AddResource registers a resource.
func (s *Server) AddResource(uri, name, mime string, contents []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[uri] = &ResourceDef{
		URI:      uri,
		Name:     name,
		MIME:     mime,
		Contents: contents,
	}
}

// AddPrompt registers a prompt template.
func (s *Server) AddPrompt(name, description, template string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts[name] = &PromptDef{
		Name:        name,
		Description: description,
		Template:    template,
	}
}

// Pipes creates a pair of in-memory reader/writer for piping to a fake client.
type Pipes struct {
	ClientIn  *io.PipeWriter // writes go to server
	ClientOut *io.PipeReader // reads come from server

	serverIn  *io.PipeReader
	serverOut *io.PipeWriter
}

// NewPipes creates a pair of pipes for communication.
func NewPipes() *Pipes {
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	return &Pipes{
		ClientIn:  serverInW,
		ClientOut: serverOutR,
		serverIn:  serverInR,
		serverOut: serverOutW,
	}
}

// Close closes all pipes.
func (p *Pipes) Close() error {
	p.ClientIn.Close()
	p.ClientOut.Close()
	p.serverIn.Close()
	p.serverOut.Close()
	return nil
}

// Start runs the server loop, reading JSON-RPC lines from pipes.serverIn
// and writing responses to pipes.serverOut.
// Blocks until context is cancelled or the input pipe is closed.
func (s *Server) Start(ctx context.Context, pipes *Pipes) error {
	if pipes == nil {
		return errors.New("fakemcp: pipes cannot be nil")
	}

	scanner := bufio.NewScanner(pipes.serverIn)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024) // 8MB max

	done := make(chan error, 1)

	go func() {
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				s.writeError(pipes.serverOut, nil, -32700, "parse error")
				continue
			}

			s.mu.Lock()
			s.requestLog = append(s.requestLog, req.Method)
			s.mu.Unlock()

			resp := s.handleRequest(&req)
			data, err := json.Marshal(resp)
			if err != nil {
				continue
			}
			data = append(data, '\n')
			if _, err := pipes.serverOut.Write(data); err != nil {
				done <- err
				return
			}
		}
		done <- scanner.Err()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopCh:
		return nil
	case err := <-done:
		return err
	}
}

// Stop signals the server loop to exit.
func (s *Server) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// handleRequest dispatches a request to the appropriate handler.
func (s *Server) handleRequest(req *Request) *Response {
	resp := &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "fakemcp",
				"version": "1.0.0",
			},
		}

	case "tools/list":
		s.mu.Lock()
		tools := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.Schema,
			})
		}
		s.mu.Unlock()
		resp.Result = map[string]any{"tools": tools}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &Error{Code: -32602, Message: "invalid params"}
			return resp
		}
		s.mu.Lock()
		tool, ok := s.tools[params.Name]
		s.mu.Unlock()
		if !ok {
			resp.Error = &Error{Code: -32601, Message: "tool not found: " + params.Name}
			return resp
		}
		result, err := tool.Handler(params.Arguments)
		if err != nil {
			resp.Result = map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": err.Error()},
				},
				"isError": true,
			}
		} else {
			resp.Result = map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": fmt.Sprintf("%v", result)},
				},
			}
		}

	case "resources/list":
		s.mu.Lock()
		resources := make([]map[string]any, 0, len(s.resources))
		for _, r := range s.resources {
			resources = append(resources, map[string]any{
				"uri":      r.URI,
				"name":     r.Name,
				"mimeType": r.MIME,
			})
		}
		s.mu.Unlock()
		resp.Result = map[string]any{"resources": resources}

	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &Error{Code: -32602, Message: "invalid params"}
			return resp
		}
		s.mu.Lock()
		res, ok := s.resources[params.URI]
		s.mu.Unlock()
		if !ok {
			resp.Error = &Error{Code: -32601, Message: "resource not found: " + params.URI}
			return resp
		}
		content := map[string]any{
			"uri":      res.URI,
			"mimeType": res.MIME,
		}
		if isText(res.MIME) {
			content["text"] = string(res.Contents)
		} else {
			content["blob"] = base64.StdEncoding.EncodeToString(res.Contents)
		}
		resp.Result = map[string]any{"contents": []any{content}}

	case "prompts/list":
		s.mu.Lock()
		prompts := make([]map[string]any, 0, len(s.prompts))
		for _, p := range s.prompts {
			prompts = append(prompts, map[string]any{
				"name":        p.Name,
				"description": p.Description,
			})
		}
		s.mu.Unlock()
		resp.Result = map[string]any{"prompts": prompts}

	default:
		resp.Error = &Error{Code: -32601, Message: "method not found: " + req.Method}
	}

	return resp
}

func (s *Server) writeError(w io.Writer, id json.RawMessage, code int, msg string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: msg},
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	w.Write(data)
}

// RequestLog returns the list of method names received by the server.
func (s *Server) RequestLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.requestLog))
	copy(out, s.requestLog)
	return out
}

// ClearLog clears the request log.
func (s *Server) ClearLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestLog = nil
}

// IsInitialized returns true if the server received an initialize call.
func (s *Server) IsInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// ToolCount returns the number of registered tools.
func (s *Server) ToolCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tools)
}

// ResourceCount returns the number of registered resources.
func (s *Server) ResourceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.resources)
}

func isText(mime string) bool {
	if mime == "" {
		return true
	}
	switch mime {
	case "text/plain", "text/html", "text/markdown", "application/json",
		"application/xml", "text/xml", "application/yaml", "text/yaml",
		"text/csv", "application/javascript", "text/javascript":
		return true
	}
	return len(mime) >= 5 && mime[:5] == "text/"
}
