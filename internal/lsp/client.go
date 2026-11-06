package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- JSON-RPC 2.0 types ---

// jsonrpcRequest is a JSON-RPC 2.0 request message.
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response message.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonrpcError is a JSON-RPC 2.0 error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *jsonrpcError) Error() string {
	return fmt.Sprintf("LSP error %d: %s", e.Code, e.Message)
}

// LSP error codes
const (
	lspErrContentModified = -32801
)

// --- LSP Protocol Types ---

// Position in a text document (0-based).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a file URI + range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// LocationLink is a more detailed location reference.
type LocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

// TextDocumentIdentifier identifies a text document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentPositionParams is a common params pattern.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// TextDocumentItem represents a fully specified text document.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// VersionedTextDocumentIdentifier identifies a versioned text document.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentContentChangeEvent describes a change to a text document.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// Hover result from LSP.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// MarkupContent represents markup content.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// SymbolKind is the kind of a symbol.
type SymbolKind int

const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// SymbolKindString returns a human-readable name for a SymbolKind.
func (sk SymbolKind) String() string {
	names := map[SymbolKind]string{
		1: "File", 2: "Module", 3: "Namespace", 4: "Package",
		5: "Class", 6: "Method", 7: "Property", 8: "Field",
		9: "Constructor", 10: "Enum", 11: "Interface", 12: "Function",
		13: "Variable", 14: "Constant", 15: "String", 16: "Number",
		17: "Boolean", 18: "Array", 19: "Object", 20: "Key",
		21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
		25: "Operator", 26: "TypeParameter",
	}
	if name, ok := names[sk]; ok {
		return name
	}
	return "Unknown"
}

// SymbolInformation describes a symbol.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// DocumentSymbol represents a document symbol with optional children.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// DiagnosticSeverity is the severity of a diagnostic.
type DiagnosticSeverity int

const (
	DiagnosticSeverityError       DiagnosticSeverity = 1
	DiagnosticSeverityWarning     DiagnosticSeverity = 2
	DiagnosticSeverityInformation DiagnosticSeverity = 3
	DiagnosticSeverityHint        DiagnosticSeverity = 4
)

// String returns the human-readable name for a DiagnosticSeverity.
func (ds DiagnosticSeverity) String() string {
	switch ds {
	case DiagnosticSeverityError:
		return "Error"
	case DiagnosticSeverityWarning:
		return "Warning"
	case DiagnosticSeverityInformation:
		return "Info"
	case DiagnosticSeverityHint:
		return "Hint"
	default:
		return "Error"
	}
}

// Diagnostic represents a diagnostic (error/warning).
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     any                `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// PublishDiagnosticsParams is the parameter for textDocument/publishDiagnostics.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// CallHierarchyItem is an item in the call hierarchy.
type CallHierarchyItem struct {
	Name           string     `json:"name"`
	Kind           SymbolKind `json:"kind"`
	Detail         string     `json:"detail,omitempty"`
	URI            string     `json:"uri"`
	Range          Range      `json:"range"`
	SelectionRange Range      `json:"selectionRange"`
}

// CallHierarchyIncomingCall is an incoming call.
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

// CallHierarchyOutgoingCall is an outgoing call.
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// ServerCapabilities caches what the server supports.
type ServerCapabilities struct {
	HoverProvider           bool `json:"hoverProvider,omitempty"`
	DefinitionProvider      bool `json:"definitionProvider,omitempty"`
	ReferencesProvider      bool `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider  bool `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider bool `json:"workspaceSymbolProvider,omitempty"`
	ImplementationProvider  bool `json:"implementationProvider,omitempty"`
	CallHierarchyProvider   bool `json:"callHierarchyProvider,omitempty"`
	DiagnosticProvider      bool `json:"diagnosticProvider,omitempty"`
	CompletionProvider      any  `json:"completionProvider,omitempty"`
	SignatureHelpProvider   any  `json:"signatureHelpProvider,omitempty"`
	CodeActionProvider      any  `json:"codeActionProvider,omitempty"`
	TextDocumentSync        any  `json:"textDocumentSync,omitempty"`
}

// InitializeResult is the result of the initialize request.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// --- pendingRequest tracks an in-flight JSON-RPC request ---

type pendingRequest struct {
	ch chan jsonrpcResponse
}

// --- LSPClient ---

// LSPClient manages a JSON-RPC 2.0 connection to an LSP server via stdio.
type LSPClient struct {
	mu            sync.Mutex
	serverName    string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	stderr        io.ReadCloser
	pending       map[int64]*pendingRequest
	nextID        atomic.Int64
	initialized   bool
	capabilities  *ServerCapabilities
	stopping      bool
	stopped       bool
	onCrash       func(error)
	notifyHandler map[string]func(json.RawMessage) // notification handlers

	// readDone is closed when the read loop exits
	readDone chan struct{}
}

// NewLSPClient creates a new LSP client for the given server name.
func NewLSPClient(serverName string, onCrash func(error)) *LSPClient {
	return &LSPClient{
		serverName:    serverName,
		pending:       make(map[int64]*pendingRequest),
		onCrash:       onCrash,
		notifyHandler: make(map[string]func(json.RawMessage)),
		readDone:      make(chan struct{}),
	}
}

// Start spawns the LSP server process and begins JSON-RPC communication.
func (c *LSPClient) Start(command string, args []string, env map[string]string, cwd string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd != nil {
		return fmt.Errorf("LSP client %s already started", c.serverName)
	}

	c.cmd = exec.Command(command, args...)
	if cwd != "" {
		c.cmd.Dir = cwd
	}

	// Merge environment
	c.cmd.Env = os.Environ()
	for k, v := range env {
		c.cmd.Env = append(c.cmd.Env, k+"="+v)
	}

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe for %s: %w", c.serverName, err)
	}
	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe for %s: %w", c.serverName, err)
	}
	c.stderr, err = c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe for %s: %w", c.serverName, err)
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.serverName, err)
	}

	// Drain stderr
	go func() {
		scanner := bufio.NewScanner(c.stderr)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			log.Printf("[LSP SERVER %s] %s", c.serverName, scanner.Text())
		}
	}()

	// Start reading JSON-RPC messages
	go c.readLoop()

	// Monitor process exit
	go func() {
		err := c.cmd.Wait()
		c.mu.Lock()
		wasStopping := c.stopping
		c.stopped = true
		c.mu.Unlock()

		if !wasStopping && err != nil {
			crashErr := fmt.Errorf("LSP server %s crashed: %w", c.serverName, err)
			log.Printf("%v", crashErr)
			if c.onCrash != nil {
				c.onCrash(crashErr)
			}
		}

		// Cancel all pending requests
		c.mu.Lock()
		for id, p := range c.pending {
			p.ch <- jsonrpcResponse{
				Error: &jsonrpcError{Code: -1, Message: "server exited"},
			}
			delete(c.pending, id)
		}
		c.mu.Unlock()
	}()

	return nil
}

// readLoop reads LSP Content-Length framed messages from stdout.
func (c *LSPClient) readLoop() {
	defer close(c.readDone)
	reader := bufio.NewReader(c.stdout)

	for {
		// Read headers until blank line
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return // connection closed
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break // end of headers
			}
			if strings.HasPrefix(line, "Content-Length:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				n, err := strconv.Atoi(val)
				if err == nil {
					contentLength = n
				}
			}
		}

		if contentLength <= 0 {
			continue
		}

		// Read exactly contentLength bytes
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return // connection broken
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			log.Printf("[LSP %s] malformed response: %v", c.serverName, err)
			continue
		}

		// If it's a notification (no id, has method)
		if resp.ID == nil && resp.Method != "" {
			c.mu.Lock()
			handler, ok := c.notifyHandler[resp.Method]
			c.mu.Unlock()
			if ok {
				go handler(resp.Params)
			}
			continue
		}

		// If it's a server-to-client request (has id + method)
		if resp.ID != nil && resp.Method != "" {
			// Respond with null result for workspace/configuration etc.
			c.mu.Lock()
			handler, ok := c.notifyHandler[resp.Method]
			c.mu.Unlock()
			if ok {
				go handler(resp.Params)
			}
			// Send a default response
			go func(id int64) {
				_ = c.writeMessage(jsonrpcRequest{
					JSONRPC: "2.0",
					ID:      &id,
					Method:  "",
				})
			}(*resp.ID)
			continue
		}

		// It's a response to one of our requests
		if resp.ID != nil {
			c.mu.Lock()
			p, ok := c.pending[*resp.ID]
			if ok {
				delete(c.pending, *resp.ID)
			}
			c.mu.Unlock()
			if ok {
				p.ch <- resp
			}
		}
	}
}

// writeMessage sends a JSON-RPC message with Content-Length header.
func (c *LSPClient) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopped || c.stdin == nil {
		return fmt.Errorf("connection closed")
	}

	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

// Initialize performs the LSP initialize handshake.
func (c *LSPClient) Initialize(workspaceDir string) (*InitializeResult, error) {
	absDir, _ := filepath.Abs(workspaceDir)
	workspaceURI := pathToFileURI(absDir)

	params := map[string]any{
		"processId":             os.Getpid(),
		"initializationOptions": map[string]any{},
		"workspaceFolders": []map[string]any{
			{
				"uri":  workspaceURI,
				"name": filepath.Base(absDir),
			},
		},
		"rootPath": absDir,
		"rootUri":  workspaceURI,
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":    false,
				"workspaceFolders": false,
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"dynamicRegistration": false,
					"willSave":            false,
					"willSaveWaitUntil":   false,
					"didSave":             true,
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
				},
				"hover": map[string]any{
					"dynamicRegistration": false,
					"contentFormat":       []string{"markdown", "plaintext"},
				},
				"definition": map[string]any{
					"dynamicRegistration": false,
					"linkSupport":         true,
				},
				"references": map[string]any{
					"dynamicRegistration": false,
				},
				"documentSymbol": map[string]any{
					"dynamicRegistration":               false,
					"hierarchicalDocumentSymbolSupport": true,
				},
				"callHierarchy": map[string]any{
					"dynamicRegistration": false,
				},
			},
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
	}

	raw, err := c.SendRequest("initialize", params, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("initialize %s: %w", c.serverName, err)
	}

	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse initialize result for %s: %w", c.serverName, err)
	}

	c.capabilities = &result.Capabilities

	// Send initialized notification
	if err := c.SendNotification("initialized", map[string]any{}); err != nil {
		return nil, fmt.Errorf("initialized notification for %s: %w", c.serverName, err)
	}

	c.mu.Lock()
	c.initialized = true
	c.mu.Unlock()

	return &result, nil
}

// SendRequest sends a JSON-RPC request and waits for the response.
func (c *LSPClient) SendRequest(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	pr := &pendingRequest{ch: make(chan jsonrpcResponse, 1)}
	c.mu.Lock()
	c.pending[id] = pr
	c.mu.Unlock()

	if err := c.writeMessage(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-pr.ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("request %s to %s timed out after %v", method, c.serverName, timeout)
	}
}

// SendRequestWithRetry sends a request, retrying on transient errors (ContentModified).
func (c *LSPClient) SendRequestWithRetry(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	const maxRetries = 3
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		raw, err := c.SendRequest(method, params, timeout)
		if err == nil {
			return raw, nil
		}
		lastErr = err

		// Check if this is a transient ContentModified error
		if rpcErr, ok := err.(*jsonrpcError); ok && rpcErr.Code == lspErrContentModified && attempt < maxRetries {
			delay := baseDelay * time.Duration(1<<attempt)
			log.Printf("[LSP %s] ContentModified on %s, retrying in %v (attempt %d/%d)",
				c.serverName, method, delay, attempt+1, maxRetries)
			time.Sleep(delay)
			continue
		}
		break
	}
	return nil, lastErr
}

// SendNotification sends a JSON-RPC notification (no response expected).
func (c *LSPClient) SendNotification(method string, params any) error {
	msg := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.writeMessage(msg)
}

// OnNotification registers a handler for server notifications.
func (c *LSPClient) OnNotification(method string, handler func(json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyHandler[method] = handler
}

// IsInitialized returns whether the server has completed initialization.
func (c *LSPClient) IsInitialized() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.initialized
}

// Capabilities returns the cached server capabilities, or nil if not initialized.
func (c *LSPClient) Capabilities() *ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.capabilities
}

// Stop gracefully shuts down the LSP server.
func (c *LSPClient) Stop() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopping = true
	c.mu.Unlock()

	// Try graceful shutdown: send shutdown request, then exit notification
	if c.initialized {
		_, _ = c.SendRequest("shutdown", nil, 5*time.Second)
		_ = c.SendNotification("exit", nil)
	}

	// Close stdin to signal process
	if c.stdin != nil {
		c.stdin.Close()
	}

	// Wait for read loop to finish (with timeout)
	select {
	case <-c.readDone:
	case <-time.After(5 * time.Second):
	}

	// Force kill if still running
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}

	c.mu.Lock()
	c.stopped = true
	c.initialized = false
	c.capabilities = nil
	c.mu.Unlock()

	return nil
}

// --- Utility functions ---

// pathToFileURI converts an absolute file path to a file:// URI.
func pathToFileURI(absPath string) string {
	// On Windows: file:///C:/path
	// On Unix: file:///path
	absPath = filepath.ToSlash(absPath)
	if !strings.HasPrefix(absPath, "/") {
		return "file:///" + absPath
	}
	return "file://" + absPath
}

// fileURIToPath converts a file:// URI to a local file path.
func fileURIToPath(uri string) string {
	s := strings.TrimPrefix(uri, "file://")
	// Handle Windows paths: /C:/path → C:/path
	if len(s) >= 3 && s[0] == '/' && s[2] == ':' {
		s = s[1:]
	}
	return filepath.FromSlash(s)
}
