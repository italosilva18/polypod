package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/config"
	"github.com/costa/polypod/internal/hooks"
	"gopkg.in/yaml.v3"
)

// PluginAssets are declarative hook and MCP configurations shipped alongside skills.
// Runtime names are scoped by the plugin manager's consumer.
type PluginAssets struct {
	Hooks      []hooks.Hook
	MCPServers config.MCPServersConfig
}

func ReadPluginAssets(info PluginInfo) (PluginAssets, error) {
	assets := PluginAssets{MCPServers: make(config.MCPServersConfig)}
	seenFiles := make(map[string]bool)
	seenHooks := make(map[string]bool)
	for _, relPath := range info.HookFiles {
		data, err := readPluginAsset(info.Path, relPath, seenFiles)
		if err != nil {
			return assets, err
		}
		var declared []hooks.Hook
		if err := yaml.Unmarshal(data, &declared); err != nil || len(declared) == 0 {
			return assets, fmt.Errorf("hooks de %q inválidos: %v", relPath, err)
		}
		for _, hook := range declared {
			if strings.TrimSpace(hook.Name) == "" || seenHooks[hook.Name] || !hooks.IsValidHookEvent(string(hook.Event)) {
				return assets, fmt.Errorf("hook duplicado ou inválido em %q: %q", relPath, hook.Name)
			}
			if hook.Type == hooks.HandlerShell && strings.TrimSpace(hook.Command) == "" ||
				hook.Type == hooks.HandlerHTTP && strings.TrimSpace(hook.URL) == "" ||
				hook.Type != hooks.HandlerShell && hook.Type != hooks.HandlerHTTP {
				return assets, fmt.Errorf("tipo ou destino de hook inválido em %q: %q", relPath, hook.Name)
			}
			seenHooks[hook.Name] = true
			assets.Hooks = append(assets.Hooks, hook)
		}
	}
	for _, relPath := range info.MCPFiles {
		data, err := readPluginAsset(info.Path, relPath, seenFiles)
		if err != nil {
			return assets, err
		}
		var servers config.MCPServersConfig
		if strings.EqualFold(filepath.Ext(relPath), ".json") {
			servers, err = config.ParseMCPServersJSON(data)
		} else {
			servers, err = config.ParseMCPServersYAML(data)
		}
		if err != nil || len(servers) == 0 {
			return assets, fmt.Errorf("MCP de %q inválido: %v", relPath, err)
		}
		for name, server := range servers {
			if server.Type == "websocket" {
				return assets, fmt.Errorf("transporte MCP websocket não suportado pelo runtime: %q", name)
			}
			if _, duplicate := assets.MCPServers[name]; duplicate {
				return assets, fmt.Errorf("servidor MCP duplicado: %q", name)
			}
			assets.MCPServers[name] = server
		}
	}
	return assets, nil
}

func readPluginAsset(root, relative string, seen map[string]bool) ([]byte, error) {
	if !filepath.IsLocal(relative) || filepath.Clean(relative) != relative || seen[relative] {
		return nil, fmt.Errorf("caminho de recurso inválido ou duplicado: %q", relative)
	}
	seen[relative] = true
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("recurso fora do plugin: %q", relative)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("recurso de plugin precisa ser arquivo regular de até 1 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) && strings.EqualFold(filepath.Ext(relative), ".json") {
		return nil, fmt.Errorf("JSON inválido em %q", relative)
	}
	return data, nil
}
