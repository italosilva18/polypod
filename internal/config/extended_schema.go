package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ExtendedConfig is a non-invasive wrapper around the main Config. It holds a
// pointer to an already-loaded base Config plus the new TS-compatible sections
// (permissions, mcpServers, outputStyle, env) that were added in round 1. The
// wrapper exists so we can ship these extensions without editing config.go or
// the existing loader pipeline.
//
// Env is declared as a pointer with the ",inline" tag purely for
// documentation; yaml.v3 will not honour ",inline" on pointer fields, so the
// custom ParseExtendedConfig function below does the inline merge manually by
// unmarshalling the env fields into a side struct.
type ExtendedConfig struct {
	Base *Config `yaml:"-" json:"-"`

	Permissions *PermissionsConfig `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	MCPServers  MCPServersConfig   `yaml:"mcpServers,omitempty" json:"mcpServers,omitempty"`
	OutputStyle *OutputStyleConfig `yaml:"outputStyle,omitempty" json:"outputStyle,omitempty"`
	Env         *EnvConfig         `yaml:",inline" json:",inline"`
}

// extendedWireYAML is the internal shape used for unmarshalling. Env is kept
// as a concrete value so yaml.v3 can populate its fields; we convert to a
// pointer afterwards if anything non-zero was provided.
type extendedWireYAML struct {
	Permissions *PermissionsConfig `yaml:"permissions,omitempty"`
	MCPServers  MCPServersConfig   `yaml:"mcpServers,omitempty"`
	OutputStyle *OutputStyleConfig `yaml:"outputStyle,omitempty"`
	// Env fields are inlined at the top level.
	EnvVars          map[string]string `yaml:"env,omitempty"`
	APIKeyHelper     string            `yaml:"apiKeyHelper,omitempty"`
	DisableHooks     bool              `yaml:"disableHooks,omitempty"`
	OnlyManagedHooks bool              `yaml:"onlyManagedHooks,omitempty"`
}

// ParseExtendedConfig parses the raw YAML/JSON bytes into a new
// ExtendedConfig, wrapping the already-loaded base Config. The base may be
// nil if the caller only wants the extension sections.
func ParseExtendedConfig(base *Config, data []byte) (*ExtendedConfig, error) {
	wire := extendedWireYAML{}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("parsing extended config: %w", err)
		}
	}

	ext := &ExtendedConfig{
		Base:        base,
		Permissions: wire.Permissions,
		MCPServers:  wire.MCPServers,
		OutputStyle: wire.OutputStyle,
	}

	if len(wire.EnvVars) > 0 || wire.APIKeyHelper != "" || wire.DisableHooks || wire.OnlyManagedHooks {
		ext.Env = &EnvConfig{
			Vars:             wire.EnvVars,
			APIKeyHelper:     wire.APIKeyHelper,
			DisableHooks:     wire.DisableHooks,
			OnlyManagedHooks: wire.OnlyManagedHooks,
		}
	}

	if err := ext.Validate(); err != nil {
		return nil, err
	}
	return ext, nil
}

// LoadExtendedFromFile reads the given path and delegates to
// ParseExtendedConfig. It is the preferred entrypoint for callers that already
// have a base Config loaded via config.Load().
func LoadExtendedFromFile(base *Config, path string) (*ExtendedConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading extended config %s: %w", path, err)
	}
	return ParseExtendedConfig(base, data)
}

// Validate runs each sub-Validate() and joins the errors with errors.Join so
// callers get the full picture in one pass instead of the first failure.
func (e *ExtendedConfig) Validate() error {
	if e == nil {
		return nil
	}
	var errs []error
	if err := e.Permissions.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("permissions: %w", err))
	}
	if err := e.MCPServers.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("mcpServers: %w", err))
	}
	if err := e.OutputStyle.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("outputStyle: %w", err))
	}
	if err := e.Env.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("env: %w", err))
	}
	return errors.Join(errs...)
}

// Apply performs side-effectful setup required by the extended sections. At
// the moment that is limited to exporting env vars so downstream subprocesses
// (apiKeyHelper, MCP servers, hooks) see them.
func (e *ExtendedConfig) Apply() error {
	if e == nil {
		return nil
	}
	if e.Env != nil {
		if err := e.Env.Apply(); err != nil {
			return fmt.Errorf("applying env: %w", err)
		}
	}
	return nil
}

// HasPermissions reports whether the caller configured a Permissions section.
func (e *ExtendedConfig) HasPermissions() bool {
	return e != nil && e.Permissions != nil
}

// HasMCPServers reports whether at least one MCP server was configured.
func (e *ExtendedConfig) HasMCPServers() bool {
	return e != nil && len(e.MCPServers) > 0
}

// HasOutputStyle reports whether the caller configured an OutputStyle section.
func (e *ExtendedConfig) HasOutputStyle() bool {
	return e != nil && e.OutputStyle != nil
}

// HasEnv reports whether the caller configured an Env section.
func (e *ExtendedConfig) HasEnv() bool {
	return e != nil && e.Env != nil
}

// PermissionMode returns the active permission mode. It prefers Permissions.Mode,
// falls back to Permissions.DefaultMode, and ultimately to "default".
func (e *ExtendedConfig) PermissionMode() string {
	if e == nil || e.Permissions == nil {
		return "default"
	}
	return e.Permissions.PermissionMode()
}
