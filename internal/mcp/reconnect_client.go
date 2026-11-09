package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Reconnect redials a configured server and keeps the old session available
// until the replacement has initialized and published its tool catalog.
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	cfg, configured := m.configs[name]
	old := m.sessions[name]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return fmt.Errorf("mcp: manager fechado")
	}
	if !configured {
		return fmt.Errorf("mcp: servidor %q nao configurado", name)
	}
	cfg = cloneServerConfig(cfg)

	var replacement *mcpsdk.ClientSession
	if err := ReconnectLoop(ctx, DefaultReconnectConfig(), m.logger, func(attemptCtx context.Context) error {
		transport, err := transportForConfig(cfg)
		if err != nil {
			return err
		}
		candidate, err := m.client.Connect(attemptCtx, transport, nil)
		if err != nil {
			if candidate != nil {
				if closeErr := candidate.Close(); closeErr != nil {
					m.logger.Warn("mcp failed candidate close failed", "server", name, "error", closeErr)
				}
			}
			return err
		}
		replacement = candidate
		return nil
	}); err != nil {
		return fmt.Errorf("mcp reconnect %s: %w", name, err)
	}
	if replacement == nil {
		return fmt.Errorf("mcp reconnect %s: sessao ausente", name)
	}

	var items []*mcpsdk.Tool
	if initialization := replacement.InitializeResult(); initialization != nil && initialization.Capabilities.Tools != nil {
		var err error
		items, err = listAllTools(ctx, replacement)
		if err != nil {
			if closeErr := replacement.Close(); closeErr != nil {
				m.logger.Warn("mcp replacement close failed", "server", name, "error", closeErr)
			}
			return fmt.Errorf("mcp reconnect list tools %s: %w", name, err)
		}
	}

	m.mu.Lock()
	if m.closed || m.sessions[name] != old {
		m.mu.Unlock()
		if closeErr := replacement.Close(); closeErr != nil {
			m.logger.Warn("mcp stale replacement close failed", "server", name, "error", closeErr)
		}
		return fmt.Errorf("mcp: estado de %q mudou durante reconexao", name)
	}
	m.removeServerSkillsLocked(name)
	m.sessions[name] = replacement
	m.tools[name] = items
	delete(m.resources, name)
	delete(m.resourceTemplates, name)
	delete(m.resourceUpdates, name)
	delete(m.prompts, name)
	if m.skillReg != nil {
		for _, item := range items {
			m.registerMCPToolLocked(name, item, replacement)
		}
	}
	m.mu.Unlock()

	// The previous reconnect state owns the previous session's context. Stop it
	// only after the new session is visible to callers.
	m.reconnect.Stop(name)
	if old != nil {
		if err := old.Close(); err != nil {
			m.logger.Warn("mcp replaced session close failed", "server", name, "error", err)
		}
	}
	m.refreshOptionalLists(ctx, replacement)
	m.restoreSubscriptions(ctx, name, replacement)
	m.logger.Info("mcp server reconnected", "server", name, "tools", len(items))
	return nil
}

// ServerStatus exposes connection state without stored credentials.
type ServerStatus struct {
	Name      string
	Transport string
	Connected bool
	Tools     []string
}

// ListConfiguredServers includes configured servers that are currently offline.
func (m *Manager) ListConfiguredServers() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(m.configs))
	for name, cfg := range m.configs {
		transport := cfg.Transport
		if transport == "" {
			transport = "stdio"
		}
		status := ServerStatus{Name: name, Transport: transport, Connected: m.sessions[name] != nil}
		for _, item := range m.tools[name] {
			if item != nil {
				status.Tools = append(status.Tools, item.Name)
			}
		}
		sort.Strings(status.Tools)
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

func cloneServerConfig(cfg ServerConfig) ServerConfig {
	cfg.Args = append([]string(nil), cfg.Args...)
	if cfg.Env != nil {
		copied := make(map[string]string, len(cfg.Env))
		for key, value := range cfg.Env {
			copied[key] = value
		}
		cfg.Env = copied
	}
	if cfg.Headers != nil {
		copied := make(map[string]string, len(cfg.Headers))
		for key, value := range cfg.Headers {
			copied[key] = value
		}
		cfg.Headers = copied
	}
	return cfg
}

func transportForConfig(cfg ServerConfig) (mcpsdk.Transport, error) {
	switch cfg.Transport {
	case "", "stdio":
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if len(cfg.Env) > 0 {
			cmd.Env = os.Environ()
			for key, value := range cfg.Env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		return &mcpsdk.CommandTransport{Command: cmd}, nil
	case "sse":
		return &mcpsdk.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: mcpHTTPClient(cfg.Headers)}, nil
	case "http", "streamable-http":
		return &mcpsdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: mcpHTTPClient(cfg.Headers)}, nil
	default:
		return nil, fmt.Errorf("mcp: transport desconhecido %q (use 'stdio', 'sse' ou 'http')", cfg.Transport)
	}
}
