package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxResourceUpdates = 64

// ResourceUpdate is a notification from a subscribed MCP resource. It does
// not contain resource contents; callers read the URI again when needed.
type ResourceUpdate struct {
	Server string    `json:"server"`
	URI    string    `json:"uri"`
	At     time.Time `json:"at"`
}

func (m *Manager) refreshResourceTemplates(ctx context.Context, session *mcpsdk.ClientSession) error {
	name := m.serverForSession(session)
	if name == "" {
		return nil
	}
	var templates []*mcpsdk.ResourceTemplate
	seen := map[string]bool{}
	cursor := ""
	for {
		result, err := session.ListResourceTemplates(ctx, &mcpsdk.ListResourceTemplatesParams{Cursor: cursor})
		if err != nil {
			return fmt.Errorf("list resource templates for %s: %w", name, err)
		}
		templates = append(templates, result.ResourceTemplates...)
		if result.NextCursor == "" {
			break
		}
		if seen[result.NextCursor] {
			return fmt.Errorf("repeated MCP resource templates cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	m.mu.Lock()
	if !m.closed && m.sessions[name] == session {
		m.resourceTemplates[name] = templates
	}
	m.mu.Unlock()
	m.logger.Info("mcp resource templates refreshed", "server", name, "count", len(templates))
	return nil
}

// ListResourceTemplates returns the latest template catalog, optionally for a
// single server. Template URIs are descriptions, not concrete resources.
func (m *Manager) ListResourceTemplates(server string) map[string][]*mcpsdk.ResourceTemplate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]*mcpsdk.ResourceTemplate)
	for name, items := range m.resourceTemplates {
		if server == "" || server == name {
			out[name] = append([]*mcpsdk.ResourceTemplate(nil), items...)
		}
	}
	return out
}

// SubscribeResource asks a connected server to announce updates for a URI.
// The desired subscription is retained across reconnects of this Manager.
func (m *Manager) SubscribeResource(ctx context.Context, server, uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return fmt.Errorf("mcp: URI de recurso obrigatoria")
	}
	m.mu.RLock()
	session := m.sessions[server]
	_, already := m.subscriptions[server][uri]
	m.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("mcp: servidor %q nao conectado", server)
	}
	if already {
		return nil
	}
	initialization := session.InitializeResult()
	if initialization == nil || initialization.Capabilities.Resources == nil || !initialization.Capabilities.Resources.Subscribe {
		return fmt.Errorf("mcp: servidor %q nao oferece assinatura de recursos", server)
	}
	if err := session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}); err != nil {
		return fmt.Errorf("mcp subscribe %s %s: %w", server, uri, err)
	}
	m.mu.Lock()
	if m.closed || m.sessions[server] != session {
		m.mu.Unlock()
		return fmt.Errorf("mcp: conexao de %q mudou durante a assinatura", server)
	}
	if m.subscriptions[server] == nil {
		m.subscriptions[server] = make(map[string]struct{})
	}
	m.subscriptions[server][uri] = struct{}{}
	m.mu.Unlock()
	return nil
}

// UnsubscribeResource removes a desired subscription and, while connected,
// asks the server to stop notifications. The local state changes only after a
// successful remote acknowledgement.
func (m *Manager) UnsubscribeResource(ctx context.Context, server, uri string) error {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return fmt.Errorf("mcp: URI de recurso obrigatoria")
	}
	m.mu.RLock()
	session := m.sessions[server]
	_, subscribed := m.subscriptions[server][uri]
	m.mu.RUnlock()
	if !subscribed {
		return nil
	}
	if session != nil {
		if err := session.Unsubscribe(ctx, &mcpsdk.UnsubscribeParams{URI: uri}); err != nil {
			return fmt.Errorf("mcp unsubscribe %s %s: %w", server, uri, err)
		}
	}
	m.mu.Lock()
	delete(m.subscriptions[server], uri)
	if len(m.subscriptions[server]) == 0 {
		delete(m.subscriptions, server)
	}
	m.mu.Unlock()
	return nil
}

// ListResourceSubscriptions returns requested subscriptions by server.
func (m *Manager) ListResourceSubscriptions(server string) map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]string)
	for name, desired := range m.subscriptions {
		if server != "" && name != server {
			continue
		}
		uris := make([]string, 0, len(desired))
		for uri := range desired {
			uris = append(uris, uri)
		}
		sort.Strings(uris)
		out[name] = uris
	}
	return out
}

func (m *Manager) restoreSubscriptions(ctx context.Context, server string, session *mcpsdk.ClientSession) {
	initialization := session.InitializeResult()
	if initialization == nil || initialization.Capabilities.Resources == nil || !initialization.Capabilities.Resources.Subscribe {
		return
	}
	m.mu.RLock()
	var uris []string
	for uri := range m.subscriptions[server] {
		uris = append(uris, uri)
	}
	m.mu.RUnlock()
	sort.Strings(uris)
	for _, uri := range uris {
		if err := session.Subscribe(ctx, &mcpsdk.SubscribeParams{URI: uri}); err != nil {
			m.logger.Warn("mcp resource subscription restore failed", "server", server, "uri", uri, "error", err)
		}
	}
}

func (m *Manager) recordResourceUpdate(req *mcpsdk.ResourceUpdatedNotificationRequest) {
	if req == nil || req.Session == nil || req.Params == nil || req.Params.URI == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	for name, session := range m.sessions {
		if session != req.Session {
			continue
		}
		items := append(m.resourceUpdates[name], ResourceUpdate{Server: name, URI: req.Params.URI, At: time.Now()})
		if len(items) > maxResourceUpdates {
			items = items[len(items)-maxResourceUpdates:]
		}
		m.resourceUpdates[name] = items
		return
	}
}

// ListResourceUpdates reports recent notifications; reading a resource always
// obtains its current contents from the server rather than a cached snapshot.
func (m *Manager) ListResourceUpdates(server string) []ResourceUpdate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ResourceUpdate
	for name, items := range m.resourceUpdates {
		if server == "" || name == server {
			out = append(out, items...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// ResourceActivity combines desired subscriptions and recent notifications for
// the read-only agent tool without exposing connection credentials.
func (m *Manager) ResourceActivity(server string) any {
	return struct {
		Subscriptions map[string][]string `json:"subscriptions"`
		Updates       []ResourceUpdate    `json:"updates"`
	}{
		Subscriptions: m.ListResourceSubscriptions(server),
		Updates:       m.ListResourceUpdates(server),
	}
}
