package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/hooks"
	"github.com/costa/polypod/internal/mcp"
	"github.com/costa/polypod/internal/skill"
)

// Runtime reconciles declarative plugin hooks and MCP servers with the live managers.
// Skills remain owned by PluginManager, so a failed external connection does not
// remove a valid skill. A later Sync retries the failed connection.
type Runtime struct {
	Plugins *skill.PluginManager
	Hooks   *hooks.Manager
	MCP     *mcp.Manager
	mu      sync.Mutex
	hooks   map[string]hooks.Hook
	servers map[string]mcp.ServerConfig
}

func New(plugins *skill.PluginManager, hookManager *hooks.Manager, mcpManager *mcp.Manager) *Runtime {
	return &Runtime{Plugins: plugins, Hooks: hookManager, MCP: mcpManager,
		hooks: make(map[string]hooks.Hook), servers: make(map[string]mcp.ServerConfig)}
}

func (r *Runtime) Sync(ctx context.Context) error {
	if r == nil || r.Plugins == nil {
		return errors.New("gerenciador de plugins indisponível")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	infos, err := r.Plugins.List(ctx)
	if err != nil {
		return err
	}
	desiredHooks := make(map[string]hooks.Hook)
	desiredServers := make(map[string]mcp.ServerConfig)
	for _, info := range infos {
		if !info.Enabled {
			continue
		}
		assets, err := skill.ReadPluginAssets(info)
		if err != nil {
			return fmt.Errorf("plugin %s: %w", info.ID, err)
		}
		for _, hook := range assets.Hooks {
			hook.Name = "plugin:" + info.ID + ":" + hook.Name
			desiredHooks[hook.Name] = hook
		}
		for name, definition := range assets.MCPServers {
			transport := definition.Type
			if transport == "" {
				transport = "stdio"
			}
			command := definition.Command
			if strings.HasPrefix(command, "."+string(filepath.Separator)) {
				command = filepath.Join(info.Path, command)
			}
			key := "plugin:" + info.ID + ":" + name
			desiredServers[key] = mcp.ServerConfig{Name: key, Transport: transport,
				Command: command, Args: definition.Args, URL: definition.URL, Env: definition.Env, Headers: definition.Headers}
		}
	}
	if r.Hooks != nil {
		for name := range r.hooks {
			if _, exists := desiredHooks[name]; !exists {
				r.Hooks.Remove(name)
				delete(r.hooks, name)
			}
		}
		for name, hook := range desiredHooks {
			if reflect.DeepEqual(r.hooks[name], hook) {
				continue
			}
			r.Hooks.Remove(name)
			r.Hooks.Register(hook)
			r.hooks[name] = hook
		}
	}
	if r.MCP == nil {
		return nil
	}
	for name := range r.servers {
		if _, exists := desiredServers[name]; !exists {
			if err := r.MCP.Disconnect(name); err != nil {
				return err
			}
			delete(r.servers, name)
		}
	}
	var names []string
	for name := range desiredServers {
		names = append(names, name)
	}
	sort.Strings(names)
	var failures []error
	for _, name := range names {
		definition := desiredServers[name]
		if reflect.DeepEqual(r.servers[name], definition) {
			continue
		}
		connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := r.MCP.Connect(connectCtx, definition)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			continue
		}
		r.servers[name] = definition
	}
	return errors.Join(failures...)
}

func (r *Runtime) Watch(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Sync(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
