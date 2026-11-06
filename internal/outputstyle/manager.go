// Package outputstyle manages output formatting styles for the CLI.
//
// It provides a registry of named styles that control rendering options
// such as markdown, color, verbosity, and token/timestamp display.
// Ported from the TypeScript outputStyles/ module.
package outputstyle

import (
	"fmt"
	"sort"
	"sync"
)

// Style describes a named output formatting preset.
type Style struct {
	Name            string
	Description     string
	ShowThinking    bool
	ShowToolCalls   bool
	ShowTokens      bool
	ShowTimestamps  bool
	MarkdownEnabled bool
	ColorEnabled    bool
	Verbose         bool
	MaxWidth        int
}

// StyleManager holds the current style and a catalogue of registered styles.
type StyleManager struct {
	mu       sync.RWMutex
	current  string
	registry map[string]Style
}

// NewStyleManager creates a StyleManager with no registered styles and the
// current style set to "default".
func NewStyleManager() *StyleManager {
	return &StyleManager{
		current:  "default",
		registry: make(map[string]Style),
	}
}

// Register adds or replaces a style in the catalogue.
func (m *StyleManager) Register(style Style) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registry[style.Name] = style
}

// Set activates a registered style by name.
// It returns an error if the name is not in the catalogue.
func (m *StyleManager) Set(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.registry[name]; !ok {
		return fmt.Errorf("outputstyle: unknown style %q", name)
	}
	m.current = name
	return nil
}

// Current returns the active style. If the active name does not match any
// registered style (e.g. the registry is empty), a zero-value Style with
// the current name is returned.
func (m *StyleManager) Current() Style {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.registry[m.current]; ok {
		return s
	}
	return Style{Name: m.current}
}

// List returns all registered styles sorted alphabetically by name.
func (m *StyleManager) List() []Style {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Style, 0, len(m.registry))
	for _, s := range m.registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// ApplyToConfig merges the current style settings into a generic
// configuration map. Existing keys are overwritten with the style values.
func (m *StyleManager) ApplyToConfig(cfg *map[string]any) {
	if cfg == nil {
		return
	}
	if *cfg == nil {
		*cfg = make(map[string]any)
	}
	s := m.Current()
	(*cfg)["outputStyle"] = s.Name
	(*cfg)["showThinking"] = s.ShowThinking
	(*cfg)["showToolCalls"] = s.ShowToolCalls
	(*cfg)["showTokens"] = s.ShowTokens
	(*cfg)["showTimestamps"] = s.ShowTimestamps
	(*cfg)["markdownEnabled"] = s.MarkdownEnabled
	(*cfg)["colorEnabled"] = s.ColorEnabled
	(*cfg)["verbose"] = s.Verbose
	if s.MaxWidth > 0 {
		(*cfg)["maxWidth"] = s.MaxWidth
	}
}

// ---------------------------------------------------------------------------
// Default styles
// ---------------------------------------------------------------------------

// DefaultStyles returns the built-in style presets.
func DefaultStyles() []Style {
	return []Style{
		{
			Name:            "default",
			Description:     "Balanced output: markdown and colors on, thinking hidden, tool calls shown",
			ShowThinking:    false,
			ShowToolCalls:   true,
			ShowTokens:      false,
			ShowTimestamps:  false,
			MarkdownEnabled: true,
			ColorEnabled:    true,
			Verbose:         false,
			MaxWidth:        0,
		},
		{
			Name:            "verbose",
			Description:     "Everything visible: thinking, tokens, timestamps, tool calls",
			ShowThinking:    true,
			ShowToolCalls:   true,
			ShowTokens:      true,
			ShowTimestamps:  true,
			MarkdownEnabled: true,
			ColorEnabled:    true,
			Verbose:         true,
			MaxWidth:        0,
		},
		{
			Name:            "minimal",
			Description:     "Clean output: no markdown, tool calls hidden, tokens hidden",
			ShowThinking:    false,
			ShowToolCalls:   false,
			ShowTokens:      false,
			ShowTimestamps:  false,
			MarkdownEnabled: false,
			ColorEnabled:    true,
			Verbose:         false,
			MaxWidth:        0,
		},
		{
			Name:            "json",
			Description:     "Raw JSON output: no markdown, no colors, all data visible",
			ShowThinking:    true,
			ShowToolCalls:   true,
			ShowTokens:      true,
			ShowTimestamps:  true,
			MarkdownEnabled: false,
			ColorEnabled:    false,
			Verbose:         true,
			MaxWidth:        0,
		},
		{
			Name:            "explanatory",
			Description:     "Verbose output with educational insights about implementation choices",
			ShowThinking:    true,
			ShowToolCalls:   true,
			ShowTokens:      true,
			ShowTimestamps:  true,
			MarkdownEnabled: true,
			ColorEnabled:    true,
			Verbose:         true,
			MaxWidth:        0,
		},
		{
			Name:            "learning",
			Description:     "Interactive learning mode: pauses for hands-on practice with insights",
			ShowThinking:    true,
			ShowToolCalls:   true,
			ShowTokens:      false,
			ShowTimestamps:  false,
			MarkdownEnabled: true,
			ColorEnabled:    true,
			Verbose:         true,
			MaxWidth:        0,
		},
		{
			Name:            "concise",
			Description:     "Short responses: no extras, minimal decoration",
			ShowThinking:    false,
			ShowToolCalls:   false,
			ShowTokens:      false,
			ShowTimestamps:  false,
			MarkdownEnabled: true,
			ColorEnabled:    true,
			Verbose:         false,
			MaxWidth:        80,
		},
	}
}

// RegisterDefaults registers all built-in styles into the given manager.
func RegisterDefaults(m *StyleManager) {
	for _, s := range DefaultStyles() {
		m.Register(s)
	}
}
