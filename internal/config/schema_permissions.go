package config

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// PermissionsConfig mirrors the `permissions` section of the Claude Code
// settings schema (see src_extracted/src/utils/settings/types.ts:42). It is a
// standalone type so it can be added to the main Config struct in a later
// pass without touching the existing loader.
type PermissionsConfig struct {
	// Mode is an optional shortcut for DefaultMode kept as a free-form field
	// for backward compatibility with tools that stamp it directly.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// AllowRules lists operations permitted without prompting.
	AllowRules []PermissionRule `yaml:"allow,omitempty" json:"allow,omitempty"`
	// DenyRules lists operations that are always rejected.
	DenyRules []PermissionRule `yaml:"deny,omitempty" json:"deny,omitempty"`
	// AskRules lists operations that must always prompt.
	AskRules []PermissionRule `yaml:"ask,omitempty" json:"ask,omitempty"`
	// DefaultMode selects the permission mode at startup.
	DefaultMode string `yaml:"defaultMode,omitempty" json:"defaultMode,omitempty"`
	// AdditionalDirectories is an optional list of extra workspace roots.
	AdditionalDirectories []string `yaml:"additionalDirectories,omitempty" json:"additionalDirectories,omitempty"`
}

// PermissionMode returns the configured startup mode. Mode takes precedence
// over DefaultMode so an explicit runtime override wins.
func (p *PermissionsConfig) PermissionMode() string {
	if p == nil {
		return "default"
	}
	if p.Mode != "" {
		return p.Mode
	}
	if p.DefaultMode != "" {
		return p.DefaultMode
	}
	return "default"
}

// PermissionRule is a single allow/deny/ask entry. Tool is the tool name
// (e.g. "Bash"), Pattern constrains the invocation (e.g. a glob on the
// command), and Reason is a human-readable explanation kept for audit logs.
type PermissionRule struct {
	Tool    string `yaml:"tool" json:"tool"`
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Reason  string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// validPermissionModes enumerates the accepted values for both Mode and
// DefaultMode. This matches EXTERNAL_PERMISSION_MODES in the TS reference.
var validPermissionModes = map[string]struct{}{
	"default":           {},
	"acceptEdits":       {},
	"bypassPermissions": {},
	"plan":              {},
	"dontAsk":           {},
}

// Validate checks that Mode and DefaultMode (when set) are recognised values
// and that each rule has a tool name.
func (p *PermissionsConfig) Validate() error {
	if p == nil {
		return nil
	}
	if p.Mode != "" {
		if _, ok := validPermissionModes[p.Mode]; !ok {
			return fmt.Errorf("invalid permissions.mode %q", p.Mode)
		}
	}
	if p.DefaultMode != "" {
		if _, ok := validPermissionModes[p.DefaultMode]; !ok {
			return fmt.Errorf("invalid permissions.defaultMode %q", p.DefaultMode)
		}
	}
	for i, r := range p.AllowRules {
		if r.Tool == "" {
			return fmt.Errorf("permissions.allow[%d]: tool is required", i)
		}
	}
	for i, r := range p.DenyRules {
		if r.Tool == "" {
			return fmt.Errorf("permissions.deny[%d]: tool is required", i)
		}
	}
	for i, r := range p.AskRules {
		if r.Tool == "" {
			return fmt.Errorf("permissions.ask[%d]: tool is required", i)
		}
	}
	return nil
}

// ParsePermissionsYAML unmarshals a YAML document into a PermissionsConfig
// and validates it.
func ParsePermissionsYAML(data []byte) (*PermissionsConfig, error) {
	p := &PermissionsConfig{}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parsing permissions yaml: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// ParsePermissionsJSON unmarshals a JSON document into a PermissionsConfig
// and validates it. Settings.json files use this codepath.
func ParsePermissionsJSON(data []byte) (*PermissionsConfig, error) {
	p := &PermissionsConfig{}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("parsing permissions json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}
