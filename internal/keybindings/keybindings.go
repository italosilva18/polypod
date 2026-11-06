// Package keybindings provides a configurable keybinding system.
//
// It ports the TypeScript keybindings subsystem (parser, resolver,
// default bindings, YAML persistence) to Go. Bindings are grouped
// by category/context and resolved by key combination lookup.
package keybindings

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

// Key represents a single keystroke with an optional modifier.
type Key struct {
	// Mod is the modifier key: "ctrl", "alt", "shift", "meta", or "" for none.
	Mod string `yaml:"mod,omitempty" json:"mod,omitempty"`
	// Key is the base key name: a letter ("c"), or a special name
	// ("escape", "enter", "tab", "up", "down", "left", "right",
	// "backspace", "delete", "pageup", "pagedown", "home", "end", "space").
	Key string `yaml:"key" json:"key"`
}

// String returns the canonical representation of a Key (e.g. "ctrl+c").
func (k Key) String() string {
	if k.Mod == "" {
		return k.Key
	}
	return k.Mod + "+" + k.Key
}

// Binding links a key combination to an action.
type Binding struct {
	// Keys is the key combination that triggers the action.
	// A single-key binding has len 1; chords have len > 1.
	Keys []Key `yaml:"keys" json:"keys"`
	// Action is the identifier executed when the binding fires
	// (e.g. "app:interrupt", "chat:submit").
	Action string `yaml:"action" json:"action"`
	// Description is a human-readable explanation shown in help.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Category groups related bindings (e.g. "Global", "Chat").
	Category string `yaml:"category,omitempty" json:"category,omitempty"`
	// Enabled controls whether the binding is active. Defaults to true.
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Registry stores and resolves keybindings. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	bindings []Binding
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds (or replaces) a binding for the given action.
// If a binding with the same action already exists it is replaced;
// otherwise a new one is appended. The binding is enabled by default.
func (r *Registry) Register(action string, keys []Key, desc string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, b := range r.bindings {
		if b.Action == action {
			r.bindings[i].Keys = keys
			r.bindings[i].Description = desc
			r.bindings[i].Enabled = true
			return
		}
	}

	r.bindings = append(r.bindings, Binding{
		Keys:        keys,
		Action:      action,
		Description: desc,
		Enabled:     true,
	})
}

// RegisterBinding adds a full Binding struct (including Category).
// If a binding with the same action already exists it is replaced.
func (r *Registry) RegisterBinding(b Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.bindings {
		if existing.Action == b.Action {
			r.bindings[i] = b
			return
		}
	}
	r.bindings = append(r.bindings, b)
}

// Lookup returns the action name for the given key combination.
// It only considers enabled bindings. The second return value
// indicates whether a match was found.
func (r *Registry) Lookup(keys []Key) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Last match wins (user overrides come after defaults).
	var match string
	found := false
	for _, b := range r.bindings {
		if !b.Enabled {
			continue
		}
		if keysEqual(b.Keys, keys) {
			match = b.Action
			found = true
		}
	}
	return match, found
}

// List returns all registered bindings sorted by Category, then Action.
func (r *Registry) List() []Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Binding, len(r.bindings))
	copy(out, r.bindings)

	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Action < out[j].Action
	})
	return out
}

// ---------------------------------------------------------------------------
// YAML persistence
// ---------------------------------------------------------------------------

// yamlFile is the top-level structure for the YAML file.
type yamlFile struct {
	Bindings []yamlBinding `yaml:"bindings"`
}

// yamlBinding is the per-binding YAML representation.
type yamlBinding struct {
	Keys        string `yaml:"keys"` // e.g. "ctrl+c", "ctrl+x ctrl+k"
	Action      string `yaml:"action"`
	Description string `yaml:"description,omitempty"`
	Category    string `yaml:"category,omitempty"`
	Enabled     bool   `yaml:"enabled"`
}

// LoadFromYAML reads a keybindings file and merges its entries into the
// registry. Existing bindings for the same action are replaced.
func (r *Registry) LoadFromYAML(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("keybindings: read %s: %w", path, err)
	}

	var file yamlFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("keybindings: parse %s: %w", path, err)
	}

	for _, yb := range file.Bindings {
		keys, err := ParseKeyCombo(yb.Keys)
		if err != nil {
			return fmt.Errorf("keybindings: parse key %q in %s: %w", yb.Keys, path, err)
		}
		r.RegisterBinding(Binding{
			Keys:        keys,
			Action:      yb.Action,
			Description: yb.Description,
			Category:    yb.Category,
			Enabled:     yb.Enabled,
		})
	}
	return nil
}

// SaveToYAML writes all bindings to a YAML file at path.
func (r *Registry) SaveToYAML(path string) error {
	bindings := r.List() // already sorted

	file := yamlFile{
		Bindings: make([]yamlBinding, 0, len(bindings)),
	}
	for _, b := range bindings {
		file.Bindings = append(file.Bindings, yamlBinding{
			Keys:        FormatKeyCombo(b.Keys),
			Action:      b.Action,
			Description: b.Description,
			Category:    b.Category,
			Enabled:     b.Enabled,
		})
	}

	data, err := yaml.Marshal(&file)
	if err != nil {
		return fmt.Errorf("keybindings: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("keybindings: write %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Default bindings
// ---------------------------------------------------------------------------

// DefaultBindings returns the standard set of keybindings that mirror the
// original TypeScript implementation's core bindings.
func DefaultBindings() []Binding {
	return []Binding{
		{Keys: []Key{{Mod: "ctrl", Key: "c"}}, Action: "app:interrupt", Description: "Cancel / interrupt current operation", Category: "Global", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "d"}}, Action: "app:exit", Description: "Exit the application", Category: "Global", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "l"}}, Action: "app:redraw", Description: "Clear / redraw the screen", Category: "Global", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "r"}}, Action: "history:search", Description: "Search command history", Category: "Global", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "k"}}, Action: "chat:clear_line", Description: "Clear line after cursor", Category: "Chat", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "a"}}, Action: "chat:line_start", Description: "Move cursor to start of line", Category: "Chat", Enabled: true},
		{Keys: []Key{{Mod: "ctrl", Key: "e"}}, Action: "chat:line_end", Description: "Move cursor to end of line", Category: "Chat", Enabled: true},
		{Keys: []Key{{Key: "tab"}}, Action: "autocomplete:accept", Description: "Accept autocomplete suggestion", Category: "Autocomplete", Enabled: true},
		{Keys: []Key{{Key: "escape"}}, Action: "chat:cancel", Description: "Cancel current input", Category: "Chat", Enabled: true},
		{Keys: []Key{{Key: "enter"}}, Action: "chat:submit", Description: "Submit message", Category: "Chat", Enabled: true},
		{Keys: []Key{{Key: "up"}}, Action: "history:previous", Description: "Previous history entry", Category: "Chat", Enabled: true},
		{Keys: []Key{{Key: "down"}}, Action: "history:next", Description: "Next history entry", Category: "Chat", Enabled: true},
	}
}

// ---------------------------------------------------------------------------
// Parsing & formatting
// ---------------------------------------------------------------------------

// modAliases maps alternative modifier names to the canonical form.
var modAliases = map[string]string{
	"ctrl":    "ctrl",
	"control": "ctrl",
	"alt":     "alt",
	"opt":     "alt",
	"option":  "alt",
	"shift":   "shift",
	"meta":    "meta",
	"cmd":     "meta",
	"command": "meta",
	"super":   "meta",
	"win":     "meta",
}

// specialKeyAliases normalises alternative key names.
var specialKeyAliases = map[string]string{
	"esc":       "escape",
	"return":    "enter",
	"space":     "space",
	"pageup":    "pageup",
	"pagedown":  "pagedown",
	"backspace": "backspace",
	"delete":    "delete",
	"home":      "home",
	"end":       "end",
	"up":        "up",
	"down":      "down",
	"left":      "left",
	"right":     "right",
	"tab":       "tab",
	"enter":     "enter",
	"escape":    "escape",
}

// ParseKeyCombo parses a human-readable key combo string into a slice of
// Key values. It handles single keys ("escape"), modified keys ("ctrl+c"),
// and chords ("ctrl+x ctrl+k") separated by whitespace.
//
// Modifier aliases are normalized: control->ctrl, opt/option->alt,
// cmd/command/super/win->meta.
//
// Returns an error if the string is empty or contains an unrecognized
// modifier.
func ParseKeyCombo(s string) ([]Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("keybindings: empty key combo")
	}

	// Split on whitespace to support chords like "ctrl+x ctrl+k".
	parts := strings.Fields(s)
	keys := make([]Key, 0, len(parts))

	for _, part := range parts {
		k, err := parseSingleKey(part)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// parseSingleKey parses one keystroke segment like "ctrl+c" or "escape".
func parseSingleKey(s string) (Key, error) {
	segments := strings.Split(strings.ToLower(s), "+")
	if len(segments) == 0 {
		return Key{}, fmt.Errorf("keybindings: empty key segment")
	}

	// Last segment is always the key; everything before is a modifier.
	rawKey := segments[len(segments)-1]
	modParts := segments[:len(segments)-1]

	// Normalise the key name.
	keyName := rawKey
	if alias, ok := specialKeyAliases[rawKey]; ok {
		keyName = alias
	}

	// Collect modifiers (there should be at most one in this simplified model,
	// but we accept compound modifiers like "ctrl+shift+k" by keeping the
	// last recognized modifier — the Key.Mod field stores a single modifier;
	// for compound modifiers we concatenate with "+").
	var mods []string
	for _, m := range modParts {
		canonical, ok := modAliases[m]
		if !ok {
			return Key{}, fmt.Errorf("keybindings: unknown modifier %q in %q", m, s)
		}
		mods = append(mods, canonical)
	}

	mod := strings.Join(mods, "+")

	return Key{Mod: mod, Key: keyName}, nil
}

// FormatKeyCombo formats a key combination for human-readable display.
// Single keys render as e.g. "ctrl+c"; chords as "ctrl+x ctrl+k".
func FormatKeyCombo(keys []Key) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k.String())
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// keysEqual compares two key slices for equality.
func keysEqual(a, b []Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i].Mod, b[i].Mod) {
			return false
		}
		if !strings.EqualFold(a[i].Key, b[i].Key) {
			return false
		}
	}
	return true
}
