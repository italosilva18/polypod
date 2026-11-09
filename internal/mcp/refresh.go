package mcp

import (
	"context"
	"fmt"
	"reflect"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const listRefreshTimeout = 20 * time.Second

func (m *Manager) refreshNotification(session *mcpsdk.ClientSession, kind string) {
	if session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), listRefreshTimeout)
	defer cancel()

	var err error
	switch kind {
	case "tools":
		err = m.refreshTools(ctx, session)
	case "resources":
		err = m.refreshResources(ctx, session)
		if templateErr := m.refreshResourceTemplates(ctx, session); templateErr != nil {
			m.logger.Debug("mcp resource templates unavailable", "error", templateErr)
		}
	case "prompts":
		err = m.refreshPrompts(ctx, session)
	}
	if err != nil {
		m.logger.Warn("mcp list refresh failed", "kind", kind, "error", err)
	}
}

func (m *Manager) serverForSession(session *mcpsdk.ClientSession) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, current := range m.sessions {
		if current == session {
			return name
		}
	}
	return ""
}

func (m *Manager) refreshTools(ctx context.Context, session *mcpsdk.ClientSession) error {
	name := m.serverForSession(session)
	if name == "" {
		return nil
	}
	tools, err := listAllTools(ctx, session)
	if err != nil {
		return fmt.Errorf("list tools for %s: %w", name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.sessions[name] != session {
		return nil
	}
	old := m.tools[name]
	m.tools[name] = tools
	if m.skillReg != nil {
		previous := make(map[string]*mcpsdk.Tool, len(old))
		for _, item := range old {
			if item != nil {
				previous[item.Name] = item
			}
		}
		present := make(map[string]bool, len(tools))
		for _, item := range tools {
			if item != nil {
				present[item.Name] = true
			}
		}
		for _, item := range old {
			if item != nil && !present[item.Name] {
				m.unregisterMCPToolLocked(name, item.Name)
			}
		}
		for _, item := range tools {
			if item != nil && !reflect.DeepEqual(previous[item.Name], item) {
				m.registerMCPToolLocked(name, item, session)
			}
		}
	}
	m.logger.Info("mcp tools refreshed", "server", name, "count", len(tools))
	return nil
}

func listAllTools(ctx context.Context, session *mcpsdk.ClientSession) ([]*mcpsdk.Tool, error) {
	var tools []*mcpsdk.Tool
	seen := map[string]bool{}
	cursor := ""
	for {
		result, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		if seen[result.NextCursor] {
			return nil, fmt.Errorf("repeated MCP tools cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
}

func (m *Manager) registerMCPToolLocked(server string, item *mcpsdk.Tool, session *mcpsdk.ClientSession) {
	if m.skillReg == nil || item == nil {
		return
	}
	s := m.toolToSkill(server, item, session)
	if existing, ok := m.skillReg.Get(s.Name); ok && existing.Origin != s.Origin {
		m.logger.Warn("mcp tool name already owned", "name", s.Name, "server", server)
		return
	}
	if err := m.skillReg.TryRegister(s); err != nil {
		m.logger.Warn("mcp tool registration failed", "name", s.Name, "error", err)
	}
}

func (m *Manager) unregisterMCPToolLocked(server, toolName string) {
	if m.skillReg == nil {
		return
	}
	name := fmt.Sprintf("mcp_%s_%s", server, toolName)
	if err := m.skillReg.UnregisterIfOrigin(name, "mcp:"+server, false); err != nil {
		m.logger.Warn("mcp tool removal skipped", "name", name, "error", err)
	}
}

func (m *Manager) removeServerSkillsLocked(server string) {
	for _, item := range m.tools[server] {
		if item != nil {
			m.unregisterMCPToolLocked(server, item.Name)
		}
	}
}

func (m *Manager) refreshOptionalLists(ctx context.Context, session *mcpsdk.ClientSession) {
	if session == nil || session.InitializeResult() == nil {
		return
	}
	caps := session.InitializeResult().Capabilities
	if caps.Resources != nil {
		if err := m.refreshResources(ctx, session); err != nil {
			m.logger.Warn("mcp initial resource discovery failed", "error", err)
		}
		if err := m.refreshResourceTemplates(ctx, session); err != nil {
			m.logger.Debug("mcp resource templates unavailable", "error", err)
		}
	}
	if caps.Prompts != nil {
		if err := m.refreshPrompts(ctx, session); err != nil {
			m.logger.Warn("mcp initial prompt discovery failed", "error", err)
		}
	}
}

func (m *Manager) refreshResources(ctx context.Context, session *mcpsdk.ClientSession) error {
	name := m.serverForSession(session)
	if name == "" {
		return nil
	}
	var resources []*mcpsdk.Resource
	seen := map[string]bool{}
	cursor := ""
	for {
		result, err := session.ListResources(ctx, &mcpsdk.ListResourcesParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("list resources for %s: %w", name, err)
		}
		resources = append(resources, result.Resources...)
		if result.NextCursor == "" {
			break
		}
		if seen[result.NextCursor] {
			return fmt.Errorf("repeated MCP resources cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	m.mu.Lock()
	if !m.closed && m.sessions[name] == session {
		m.resources[name] = resources
	}
	m.mu.Unlock()
	m.logger.Info("mcp resources refreshed", "server", name, "count", len(resources))
	return nil
}

func (m *Manager) refreshPrompts(ctx context.Context, session *mcpsdk.ClientSession) error {
	name := m.serverForSession(session)
	if name == "" {
		return nil
	}
	var prompts []*mcpsdk.Prompt
	seen := map[string]bool{}
	cursor := ""
	for {
		result, err := session.ListPrompts(ctx, &mcpsdk.ListPromptsParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("list prompts for %s: %w", name, err)
		}
		prompts = append(prompts, result.Prompts...)
		if result.NextCursor == "" {
			break
		}
		if seen[result.NextCursor] {
			return fmt.Errorf("repeated MCP prompts cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	m.mu.Lock()
	if !m.closed && m.sessions[name] == session {
		m.prompts[name] = prompts
	}
	m.mu.Unlock()
	m.logger.Info("mcp prompts refreshed", "server", name, "count", len(prompts))
	return nil
}

// ListResources returns the latest resource catalog for a connected server.
func (m *Manager) ListResources(server string) map[string][]*mcpsdk.Resource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]*mcpsdk.Resource)
	for name, items := range m.resources {
		if server == "" || name == server {
			result[name] = append([]*mcpsdk.Resource(nil), items...)
		}
	}
	return result
}

// ListPrompts returns the latest prompt catalog for a connected server.
func (m *Manager) ListPrompts(server string) map[string][]*mcpsdk.Prompt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]*mcpsdk.Prompt)
	for name, items := range m.prompts {
		if server == "" || name == server {
			result[name] = append([]*mcpsdk.Prompt(nil), items...)
		}
	}
	return result
}

// ReadResource delegates to the connected server and returns its typed content.
func (m *Manager) ReadResource(ctx context.Context, server, uri string) (*mcpsdk.ReadResourceResult, error) {
	m.mu.RLock()
	session := m.sessions[server]
	m.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("mcp: servidor %q nao conectado", server)
	}
	return session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: uri})
}

// GetPrompt delegates to the connected server and returns its typed messages.
func (m *Manager) GetPrompt(ctx context.Context, server, name string, args map[string]string) (*mcpsdk.GetPromptResult, error) {
	m.mu.RLock()
	session := m.sessions[server]
	m.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("mcp: servidor %q nao conectado", server)
	}
	return session.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: name, Arguments: args})
}
