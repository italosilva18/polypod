package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/types"
)

type acpSessionMCP struct {
	manager *mcp.Manager
	tools   []types.Tool
}

func (m *acpSessionMCP) close() {
	if m != nil && m.manager != nil {
		_ = m.manager.Close()
	}
}

type acpRawMCPServer struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Command string          `json:"command"`
	Args    []string        `json:"args"`
	URL     string          `json:"url"`
	Env     json.RawMessage `json:"env"`
	Headers json.RawMessage `json:"headers"`
}

func decodeACPKeyValues(raw json.RawMessage, fieldLabel string) (map[string]string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var pairs []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &pairs); err != nil {
			return nil, fmt.Errorf("%s inválido em mcpServers", fieldLabel)
		}
		if len(pairs) == 0 {
			return nil, nil
		}
		out := make(map[string]string, len(pairs))
		for _, item := range pairs {
			key := strings.TrimSpace(item.Name)
			if key == "" {
				return nil, fmt.Errorf("%s com nome vazio em mcpServers", fieldLabel)
			}
			out[key] = item.Value
		}
		return out, nil
	}
	var kv map[string]string
	if err := json.Unmarshal(raw, &kv); err != nil {
		return nil, fmt.Errorf("%s inválido em mcpServers", fieldLabel)
	}
	if len(kv) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(kv))
	for k, v := range kv {
		key := strings.TrimSpace(k)
		if key == "" {
			return nil, fmt.Errorf("%s com nome vazio em mcpServers", fieldLabel)
		}
		out[key] = v
	}
	return out, nil
}

func parseACPMCPServers(raw []json.RawMessage, cwd string) ([]mcp.ServerConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	configs := make([]mcp.ServerConfig, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		var srv acpRawMCPServer
		if err := json.Unmarshal(entry, &srv); err != nil {
			return nil, errors.New("entrada mcpServers inválida")
		}
		name := strings.TrimSpace(srv.Name)
		if name == "" {
			return nil, errors.New("servidor MCP sem nome")
		}
		if seen[name] {
			return nil, fmt.Errorf("servidor MCP duplicado na sessão: %s", name)
		}
		seen[name] = true

		transport := strings.ToLower(strings.TrimSpace(srv.Type))
		if transport == "" {
			if strings.TrimSpace(srv.URL) != "" && strings.TrimSpace(srv.Command) == "" {
				transport = "http"
			} else {
				transport = "stdio"
			}
		}

		env, err := decodeACPKeyValues(srv.Env, "env")
		if err != nil {
			return nil, err
		}
		headers, err := decodeACPKeyValues(srv.Headers, "headers")
		if err != nil {
			return nil, err
		}

		cfg := mcp.ServerConfig{
			Name:      name,
			Transport: transport,
			Args:      append([]string(nil), srv.Args...),
			Env:       env,
			Headers:   headers,
		}
		switch transport {
		case "stdio":
			cmd := strings.TrimSpace(srv.Command)
			if cmd == "" {
				return nil, fmt.Errorf("servidor MCP %q exige command", name)
			}
			cfg.Command = cmd
			if cwd != "" {
				if cfg.Env == nil {
					cfg.Env = make(map[string]string, 1)
				}
				if _, exists := cfg.Env["PWD"]; !exists {
					cfg.Env["PWD"] = cwd
				}
			}
		case "http", "sse":
			rawURL := strings.TrimSpace(srv.URL)
			parsedURL, parseErr := url.Parse(rawURL)
			if parseErr != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
				return nil, fmt.Errorf("servidor MCP %q exige url HTTP(S) válida", name)
			}
			cfg.URL = rawURL
		default:
			return nil, fmt.Errorf("transporte MCP não suportado no ACP: %s", transport)
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

func (s *Server) configureSessionMCP(ctx context.Context, sessionID, cwd string, raw []json.RawMessage) error {
	configs, err := parseACPMCPServers(raw, cwd)
	if err != nil {
		return err
	}
	var next *acpSessionMCP
	if len(configs) > 0 {
		mgr := mcp.NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
		for _, cfg := range configs {
			connectCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			connErr := mgr.Connect(connectCtx, cfg)
			cancel()
			if connErr != nil {
				_ = mgr.Close()
				return fmt.Errorf("falha ao conectar servidor MCP %q: %w", cfg.Name, connErr)
			}
		}
		next = &acpSessionMCP{
			manager: mgr,
			tools:   mgr.WrappedTools(),
		}
	}

	s.mu.Lock()
	if s.sessionMCP == nil {
		s.sessionMCP = make(map[string]*acpSessionMCP)
	}
	prev := s.sessionMCP[sessionID]
	if next == nil {
		delete(s.sessionMCP, sessionID)
	} else {
		s.sessionMCP[sessionID] = next
	}
	s.mu.Unlock()
	if prev != nil {
		prev.close()
	}
	return nil
}

func (s *Server) sessionTools(sessionID string) []types.Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionMCP == nil {
		return nil
	}
	state := s.sessionMCP[sessionID]
	if state == nil || len(state.tools) == 0 {
		return nil
	}
	return append([]types.Tool(nil), state.tools...)
}

func (s *Server) closeAllSessionMCP() {
	s.mu.Lock()
	states := make([]*acpSessionMCP, 0, len(s.sessionMCP))
	for k, state := range s.sessionMCP {
		if state != nil {
			states = append(states, state)
		}
		delete(s.sessionMCP, k)
	}
	s.mu.Unlock()
	for _, state := range states {
		state.close()
	}
}
