package lsp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServerState tracks a server instance lifecycle.
type ServerState string

const (
	ServerStateStopped  ServerState = "stopped"
	ServerStateStarting ServerState = "starting"
	ServerStateRunning  ServerState = "running"
	ServerStateStopping ServerState = "stopping"
	ServerStateError    ServerState = "error"
)

// ServerConfig defines how to launch and connect to an LSP server.
type ServerConfig struct {
	// Command to launch the server (e.g. "gopls", "pyright-langserver").
	Command string `json:"command"`
	// Args for the command (e.g. ["serve"]).
	Args []string `json:"args,omitempty"`
	// Environment variables to set.
	Env map[string]string `json:"env,omitempty"`
	// WorkspaceFolder overrides the workspace root sent to the server.
	WorkspaceFolder string `json:"workspaceFolder,omitempty"`
	// ExtensionToLanguage maps file extensions to language IDs.
	// e.g. {".go": "go", ".py": "python"}
	ExtensionToLanguage map[string]string `json:"extensionToLanguage"`
	// MaxRestarts is the maximum number of automatic restarts (default 3).
	MaxRestarts int `json:"maxRestarts,omitempty"`
	// StartupTimeout in milliseconds (0 = no timeout).
	StartupTimeout int `json:"startupTimeout,omitempty"`
	// InitializationOptions sent to the server.
	InitializationOptions map[string]any `json:"initializationOptions,omitempty"`
}

// ServerInstance wraps an LSPClient with lifecycle management.
type ServerInstance struct {
	mu           sync.Mutex
	Name         string
	Config       ServerConfig
	client       *LSPClient
	state        ServerState
	startTime    *time.Time
	lastError    error
	restartCount int
	crashCount   int
}

// State returns the current server state.
func (si *ServerInstance) State() ServerState {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.state
}

// LastError returns the last error.
func (si *ServerInstance) LastError() error {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.lastError
}

// IsHealthy returns true if the server is running and initialized.
func (si *ServerInstance) IsHealthy() bool {
	si.mu.Lock()
	defer si.mu.Unlock()
	return si.state == ServerStateRunning && si.client != nil && si.client.IsInitialized()
}

// Start launches the LSP server and performs the initialize handshake.
func (si *ServerInstance) Start(workspaceDir string) error {
	si.mu.Lock()
	if si.state == ServerStateRunning || si.state == ServerStateStarting {
		si.mu.Unlock()
		return nil
	}

	maxRestarts := si.Config.MaxRestarts
	if maxRestarts == 0 {
		maxRestarts = 3
	}
	if si.state == ServerStateError && si.crashCount > maxRestarts {
		si.mu.Unlock()
		return fmt.Errorf("LSP server '%s' exceeded max crash recovery attempts (%d)", si.Name, maxRestarts)
	}

	si.state = ServerStateStarting
	si.mu.Unlock()

	client := NewLSPClient(si.Name, func(err error) {
		si.mu.Lock()
		si.state = ServerStateError
		si.lastError = err
		si.crashCount++
		si.mu.Unlock()
	})

	cwd := si.Config.WorkspaceFolder
	if cwd == "" {
		cwd = workspaceDir
	}

	if err := client.Start(si.Config.Command, si.Config.Args, si.Config.Env, cwd); err != nil {
		si.mu.Lock()
		si.state = ServerStateError
		si.lastError = err
		si.mu.Unlock()
		return err
	}

	if _, err := client.Initialize(cwd); err != nil {
		client.Stop()
		si.mu.Lock()
		si.state = ServerStateError
		si.lastError = err
		si.mu.Unlock()
		return err
	}

	now := time.Now()
	si.mu.Lock()
	si.client = client
	si.state = ServerStateRunning
	si.startTime = &now
	si.crashCount = 0
	si.mu.Unlock()

	log.Printf("[LSP MANAGER] Server '%s' started successfully", si.Name)
	return nil
}

// Stop gracefully shuts down the server.
func (si *ServerInstance) Stop() error {
	si.mu.Lock()
	if si.state == ServerStateStopped || si.state == ServerStateStopping {
		si.mu.Unlock()
		return nil
	}
	si.state = ServerStateStopping
	client := si.client
	si.mu.Unlock()

	var err error
	if client != nil {
		err = client.Stop()
	}

	si.mu.Lock()
	si.state = ServerStateStopped
	si.client = nil
	si.mu.Unlock()

	return err
}

// Restart stops and starts the server.
func (si *ServerInstance) Restart(workspaceDir string) error {
	if err := si.Stop(); err != nil {
		return fmt.Errorf("stop during restart of %s: %w", si.Name, err)
	}
	si.mu.Lock()
	si.restartCount++
	maxRestarts := si.Config.MaxRestarts
	if maxRestarts == 0 {
		maxRestarts = 3
	}
	if si.restartCount > maxRestarts {
		si.mu.Unlock()
		return fmt.Errorf("max restart attempts (%d) exceeded for server '%s'", maxRestarts, si.Name)
	}
	si.mu.Unlock()
	return si.Start(workspaceDir)
}

// SendRequest sends a request to the server with retry logic.
func (si *ServerInstance) SendRequest(method string, params any) (json.RawMessage, error) {
	si.mu.Lock()
	healthy := si.state == ServerStateRunning && si.client != nil && si.client.IsInitialized()
	if !healthy {
		err := fmt.Errorf("server '%s' is not healthy (state: %s)", si.Name, si.state)
		si.mu.Unlock()
		return nil, err
	}
	client := si.client
	si.mu.Unlock()

	return client.SendRequestWithRetry(method, params, 30*time.Second)
}

// SendNotification sends a notification to the server.
func (si *ServerInstance) SendNotification(method string, params any) error {
	si.mu.Lock()
	client := si.client
	si.mu.Unlock()
	if client == nil {
		return fmt.Errorf("server '%s' not running", si.Name)
	}
	return client.SendNotification(method, params)
}

// OnNotification registers a handler for server notifications.
func (si *ServerInstance) OnNotification(method string, handler func(json.RawMessage)) {
	si.mu.Lock()
	client := si.client
	si.mu.Unlock()
	if client != nil {
		client.OnNotification(method, handler)
	}
}

// --- Default server configurations for common languages ---

// DefaultServerConfigs returns built-in LSP server configurations.
func DefaultServerConfigs() map[string]ServerConfig {
	return map[string]ServerConfig{
		"gopls": {
			Command: "gopls",
			Args:    []string{"serve"},
			ExtensionToLanguage: map[string]string{
				".go":  "go",
				".mod": "go.mod",
				".sum": "go.sum",
			},
		},
		"pyright": {
			Command: "pyright-langserver",
			Args:    []string{"--stdio"},
			ExtensionToLanguage: map[string]string{
				".py":  "python",
				".pyi": "python",
			},
		},
		"typescript-language-server": {
			Command: "typescript-language-server",
			Args:    []string{"--stdio"},
			ExtensionToLanguage: map[string]string{
				".ts":  "typescript",
				".tsx": "typescriptreact",
				".js":  "javascript",
				".jsx": "javascriptreact",
			},
		},
		"rust-analyzer": {
			Command: "rust-analyzer",
			ExtensionToLanguage: map[string]string{
				".rs": "rust",
			},
		},
		"clangd": {
			Command: "clangd",
			ExtensionToLanguage: map[string]string{
				".c":   "c",
				".cpp": "cpp",
				".cc":  "cpp",
				".cxx": "cpp",
				".h":   "c",
				".hpp": "cpp",
				".hxx": "cpp",
			},
		},
		"lua-language-server": {
			Command: "lua-language-server",
			ExtensionToLanguage: map[string]string{
				".lua": "lua",
			},
		},
	}
}

// --- LSPServerManager ---

// LSPServerManager manages multiple LSP server instances, routing requests by file extension.
type LSPServerManager struct {
	mu           sync.RWMutex
	servers      map[string]*ServerInstance
	extensionMap map[string][]string // ext -> server names
	openedFiles  map[string]string   // file URI -> server name
	workspaceDir string
}

// NewLSPServerManager creates a server manager for the given workspace.
func NewLSPServerManager(workspaceDir string) *LSPServerManager {
	return &LSPServerManager{
		servers:      make(map[string]*ServerInstance),
		extensionMap: make(map[string][]string),
		openedFiles:  make(map[string]string),
		workspaceDir: workspaceDir,
	}
}

// Initialize loads server configurations and registers them.
func (m *LSPServerManager) Initialize(configs map[string]ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, config := range configs {
		if config.Command == "" {
			log.Printf("[LSP MANAGER] Skipping server '%s': no command", name)
			continue
		}
		if len(config.ExtensionToLanguage) == 0 {
			log.Printf("[LSP MANAGER] Skipping server '%s': no extension mappings", name)
			continue
		}

		// Map extensions to this server
		for ext := range config.ExtensionToLanguage {
			norm := strings.ToLower(ext)
			m.extensionMap[norm] = append(m.extensionMap[norm], name)
		}

		m.servers[name] = &ServerInstance{
			Name:   name,
			Config: config,
			state:  ServerStateStopped,
		}
	}

	log.Printf("[LSP MANAGER] Initialized with %d server(s)", len(m.servers))
}

// InitializeDefaults loads the built-in default server configs and filters to those
// whose command is available on PATH.
func (m *LSPServerManager) InitializeDefaults() {
	defaults := DefaultServerConfigs()
	available := make(map[string]ServerConfig)
	for name, cfg := range defaults {
		if _, err := exec.LookPath(cfg.Command); err == nil {
			available[name] = cfg
		}
	}
	m.Initialize(available)
}

// Shutdown stops all running servers.
func (m *LSPServerManager) Shutdown() error {
	m.mu.Lock()
	servers := make([]*ServerInstance, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	m.mu.Unlock()

	var errs []string
	for _, s := range servers {
		if s.State() == ServerStateRunning || s.State() == ServerStateError {
			if err := s.Stop(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", s.Name, err))
			}
		}
	}

	m.mu.Lock()
	m.servers = make(map[string]*ServerInstance)
	m.extensionMap = make(map[string][]string)
	m.openedFiles = make(map[string]string)
	m.mu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop %d server(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// GetServerForFile returns the server instance for a file path (by extension).
func (m *LSPServerManager) GetServerForFile(filePath string) *ServerInstance {
	ext := strings.ToLower(filepath.Ext(filePath))
	m.mu.RLock()
	names := m.extensionMap[ext]
	if len(names) == 0 {
		m.mu.RUnlock()
		return nil
	}
	name := names[0]
	server := m.servers[name]
	m.mu.RUnlock()
	return server
}

// EnsureServerStarted starts the server for a file type if not already running.
func (m *LSPServerManager) EnsureServerStarted(filePath string) (*ServerInstance, error) {
	server := m.GetServerForFile(filePath)
	if server == nil {
		return nil, nil
	}

	state := server.State()
	if state == ServerStateStopped || state == ServerStateError {
		if err := server.Start(m.workspaceDir); err != nil {
			return nil, fmt.Errorf("start server for %s: %w", filePath, err)
		}
	}
	return server, nil
}

// SendRequest routes a request to the appropriate server for the file.
func (m *LSPServerManager) SendRequest(filePath, method string, params any) (json.RawMessage, error) {
	server, err := m.EnsureServerStarted(filePath)
	if err != nil {
		return nil, err
	}
	if server == nil {
		return nil, nil // no server for this file type
	}
	return server.SendRequest(method, params)
}

// OpenFile sends textDocument/didOpen to the appropriate server.
func (m *LSPServerManager) OpenFile(filePath, content string) error {
	server, err := m.EnsureServerStarted(filePath)
	if err != nil {
		return err
	}
	if server == nil {
		return nil
	}

	absPath, _ := filepath.Abs(filePath)
	fileURI := pathToFileURI(absPath)

	m.mu.RLock()
	existingServer := m.openedFiles[fileURI]
	m.mu.RUnlock()

	if existingServer == server.Name {
		return nil // already open on this server
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	languageID := server.Config.ExtensionToLanguage[ext]
	if languageID == "" {
		languageID = "plaintext"
	}

	err = server.SendNotification("textDocument/didOpen", map[string]any{
		"textDocument": TextDocumentItem{
			URI:        fileURI,
			LanguageID: languageID,
			Version:    1,
			Text:       content,
		},
	})
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.openedFiles[fileURI] = server.Name
	m.mu.Unlock()
	return nil
}

// ChangeFile sends textDocument/didChange to the server.
func (m *LSPServerManager) ChangeFile(filePath, content string) error {
	server := m.GetServerForFile(filePath)
	if server == nil || server.State() != ServerStateRunning {
		return m.OpenFile(filePath, content)
	}

	absPath, _ := filepath.Abs(filePath)
	fileURI := pathToFileURI(absPath)

	m.mu.RLock()
	existingServer := m.openedFiles[fileURI]
	m.mu.RUnlock()

	if existingServer != server.Name {
		return m.OpenFile(filePath, content)
	}

	return server.SendNotification("textDocument/didChange", map[string]any{
		"textDocument": VersionedTextDocumentIdentifier{
			URI:     fileURI,
			Version: 1,
		},
		"contentChanges": []TextDocumentContentChangeEvent{
			{Text: content},
		},
	})
}

// SaveFile sends textDocument/didSave to the server.
func (m *LSPServerManager) SaveFile(filePath string) error {
	server := m.GetServerForFile(filePath)
	if server == nil || server.State() != ServerStateRunning {
		return nil
	}
	absPath, _ := filepath.Abs(filePath)
	return server.SendNotification("textDocument/didSave", map[string]any{
		"textDocument": TextDocumentIdentifier{URI: pathToFileURI(absPath)},
	})
}

// CloseFile sends textDocument/didClose to the server.
func (m *LSPServerManager) CloseFile(filePath string) error {
	server := m.GetServerForFile(filePath)
	if server == nil || server.State() != ServerStateRunning {
		return nil
	}
	absPath, _ := filepath.Abs(filePath)
	fileURI := pathToFileURI(absPath)

	err := server.SendNotification("textDocument/didClose", map[string]any{
		"textDocument": TextDocumentIdentifier{URI: fileURI},
	})

	m.mu.Lock()
	delete(m.openedFiles, fileURI)
	m.mu.Unlock()

	return err
}

// IsFileOpen checks if a file is already open on a server.
func (m *LSPServerManager) IsFileOpen(filePath string) bool {
	absPath, _ := filepath.Abs(filePath)
	fileURI := pathToFileURI(absPath)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.openedFiles[fileURI]
	return ok
}

// IsConnected returns true if at least one server is running.
func (m *LSPServerManager) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.servers {
		if s.State() != ServerStateError && s.State() != ServerStateStopped {
			return true
		}
	}
	return false
}

// AllServers returns all registered server instances.
func (m *LSPServerManager) AllServers() map[string]*ServerInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*ServerInstance, len(m.servers))
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

// Ensure os and exec imports are used.
var (
	_ = os.Getenv
	_ = exec.LookPath
)
