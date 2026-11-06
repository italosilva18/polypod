package config

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// EnvConfig groups the environment-related settings from the TS reference:
// the freeform `env` map, the `apiKeyHelper` hook, plus the `disableHooks`
// and `onlyManagedHooks` booleans. They live together because Apply() needs
// to export Vars before apiKeyHelper subprocesses are spawned.
type EnvConfig struct {
	// Vars are exported into the current process environment on Apply().
	Vars map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// APIKeyHelper is a path to a script that outputs authentication values.
	APIKeyHelper string `yaml:"apiKeyHelper,omitempty" json:"apiKeyHelper,omitempty"`
	// DisableHooks mirrors the TS `disableAllHooks` flag.
	DisableHooks bool `yaml:"disableHooks,omitempty" json:"disableHooks,omitempty"`
	// OnlyManagedHooks mirrors the TS `allowManagedHooksOnly` flag.
	OnlyManagedHooks bool `yaml:"onlyManagedHooks,omitempty" json:"onlyManagedHooks,omitempty"`
}

// Validate rejects entries with empty variable names. Values are allowed to
// be empty because "unset" is a legitimate semantic (e.g. clearing a var
// inherited from the parent shell).
func (e *EnvConfig) Validate() error {
	if e == nil {
		return nil
	}
	for k := range e.Vars {
		if k == "" {
			return fmt.Errorf("env var name must not be empty")
		}
	}
	return nil
}

// Apply exports every entry in Vars into os.Environ via os.Setenv so that
// subsequently-spawned subprocesses (apiKeyHelper, hooks, MCP servers) see
// the configured values. It returns the first Setenv error, if any.
func (e *EnvConfig) Apply() error {
	if e == nil {
		return nil
	}
	if err := e.Validate(); err != nil {
		return err
	}
	for k, v := range e.Vars {
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setting env %s: %w", k, err)
		}
	}
	return nil
}

// ParseEnvYAML unmarshals an EnvConfig from YAML and validates it.
func ParseEnvYAML(data []byte) (*EnvConfig, error) {
	e := &EnvConfig{}
	if err := yaml.Unmarshal(data, e); err != nil {
		return nil, fmt.Errorf("parsing env yaml: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

// ParseEnvJSON is the JSON equivalent of ParseEnvYAML.
func ParseEnvJSON(data []byte) (*EnvConfig, error) {
	e := &EnvConfig{}
	if err := json.Unmarshal(data, e); err != nil {
		return nil, fmt.Errorf("parsing env json: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}
