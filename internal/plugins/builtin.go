// Package plugins provides a built-in plugin registry for managing plugins
// that ship with the CLI and can be loaded from disk.
//
// Ported from src/plugins/builtinPlugins.ts and src/utils/plugins/pluginLoader.ts.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Plugin represents a registered plugin with its metadata and state.
type Plugin struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Enabled     bool     `json:"enabled"`
	Skills      []string `json:"skills,omitempty"`
	Commands    []string `json:"commands,omitempty"`
	Hooks       []string `json:"hooks,omitempty"`
}

// PluginRegistry is a thread-safe registry of plugins.
type PluginRegistry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewPluginRegistry creates an empty plugin registry.
func NewPluginRegistry() *PluginRegistry {
	return &PluginRegistry{
		plugins: make(map[string]Plugin),
	}
}

// Register adds or replaces a plugin in the registry.
func (r *PluginRegistry) Register(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Name] = p
}

// Get returns a plugin by name and whether it was found.
func (r *PluginRegistry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// List returns all registered plugins sorted by name.
func (r *PluginRegistry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Enable enables a plugin by name. Returns an error if the plugin is not found.
func (r *PluginRegistry) Enable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}
	p.Enabled = true
	r.plugins[name] = p
	return nil
}

// Disable disables a plugin by name. Returns an error if the plugin is not found.
func (r *PluginRegistry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}
	p.Enabled = false
	r.plugins[name] = p
	return nil
}

// pluginManifest is the on-disk format parsed from plugin.json files.
type pluginManifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Skills      []string `json:"skills"`
	Commands    []string `json:"commands"`
	Hooks       []string `json:"hooks"`
}

// LoadFromDir scans a directory for subdirectories containing a plugin.json
// manifest and registers each discovered plugin (enabled by default).
// It returns the number of plugins loaded and the first error encountered
// (remaining plugins are still attempted).
func (r *PluginRegistry) LoadFromDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading plugin directory: %w", err)
	}

	var (
		loaded   int
		firstErr error
	)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(dir, entry.Name(), "plugin.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			// Skip directories without a manifest.
			if os.IsNotExist(err) {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("reading %s: %w", manifestPath, err)
			}
			continue
		}

		var m pluginManifest
		if err := json.Unmarshal(data, &m); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("parsing %s: %w", manifestPath, err)
			}
			continue
		}

		// Use directory name as fallback if manifest has no name.
		name := m.Name
		if name == "" {
			name = entry.Name()
		}

		r.Register(Plugin{
			Name:        name,
			Version:     m.Version,
			Description: m.Description,
			Author:      m.Author,
			Enabled:     true,
			Skills:      m.Skills,
			Commands:    m.Commands,
			Hooks:       m.Hooks,
		})
		loaded++
	}

	return loaded, firstErr
}

// BuiltinPlugins returns the set of plugins that ship with the CLI.
func BuiltinPlugins() []Plugin {
	return []Plugin{
		{
			Name:        "code-review",
			Version:     "1.0.0",
			Description: "Code review skills for analyzing diffs, suggesting improvements, and enforcing standards",
			Author:      "polypod",
			Enabled:     true,
			Skills:      []string{"review-diff", "review-pr", "lint-suggestions"},
			Commands:    []string{"/review"},
			Hooks:       []string{"pre-commit-review"},
		},
		{
			Name:        "git-tools",
			Version:     "1.0.0",
			Description: "Git operation skills for status, branching, committing, and history analysis",
			Author:      "polypod",
			Enabled:     true,
			Skills:      []string{"git-status", "git-diff", "git-log", "git-commit"},
			Commands:    []string{"/git"},
			Hooks:       []string{"post-checkout"},
		},
		{
			Name:        "doc-tools",
			Version:     "1.0.0",
			Description: "Documentation skills for generating, updating, and validating docs",
			Author:      "polypod",
			Enabled:     true,
			Skills:      []string{"generate-docs", "update-readme", "doc-lint"},
			Commands:    []string{"/docs"},
			Hooks:       nil,
		},
		{
			Name:        "test-tools",
			Version:     "1.0.0",
			Description: "Testing skills for generating, running, and analyzing test results",
			Author:      "polypod",
			Enabled:     true,
			Skills:      []string{"generate-tests", "run-tests", "coverage-report"},
			Commands:    []string{"/test"},
			Hooks:       []string{"pre-push-test"},
		},
		{
			Name:        "mcp-bridge",
			Version:     "1.0.0",
			Description: "MCP server bridge for connecting external tool servers",
			Author:      "polypod",
			Enabled:     true,
			Skills:      []string{"mcp-connect", "mcp-disconnect", "mcp-call"},
			Commands:    []string{"/mcp"},
			Hooks:       nil,
		},
	}
}

// RegisterBuiltins registers all built-in plugins into the given registry.
func RegisterBuiltins(r *PluginRegistry) {
	for _, p := range BuiltinPlugins() {
		r.Register(p)
	}
}
