package config

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// OutputStyleConfig captures the `outputStyle` setting plus an optional
// catalogue of user-defined styles. In the TS reference outputStyle is just
// a string; Polypod extends it with Custom so users can register named
// prompts via YAML.
type OutputStyleConfig struct {
	// Active is the currently selected style name (e.g. "default",
	// "concise", or a key from Custom).
	Active string `yaml:"active,omitempty" json:"active,omitempty"`
	// Custom is a catalogue of named styles keyed by identifier.
	Custom map[string]CustomStyle `yaml:"custom,omitempty" json:"custom,omitempty"`
}

// CustomStyle is a named prompt template. Description is optional and is
// shown in selection UIs.
type CustomStyle struct {
	Name        string `yaml:"name" json:"name"`
	Prompt      string `yaml:"prompt" json:"prompt"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// DefaultOutputStyle returns a sentinel config with Active="default" and no
// custom styles. Callers use this when the user has not configured
// outputStyle at all.
func DefaultOutputStyle() OutputStyleConfig {
	return OutputStyleConfig{
		Active: "default",
		Custom: map[string]CustomStyle{},
	}
}

// Validate checks that any custom style entries have a non-empty Name and
// Prompt and that Active, if set, references a known style or a built-in
// name.
func (o *OutputStyleConfig) Validate() error {
	if o == nil {
		return nil
	}
	for key, s := range o.Custom {
		if key == "" {
			return fmt.Errorf("outputStyle.custom key must not be empty")
		}
		if s.Name == "" {
			return fmt.Errorf("outputStyle.custom[%q]: name is required", key)
		}
		if s.Prompt == "" {
			return fmt.Errorf("outputStyle.custom[%q]: prompt is required", key)
		}
	}
	return nil
}

// ParseOutputStyleYAML accepts either a bare string (legacy shape matching
// the TS reference) or a structured object. It always returns a non-nil
// *OutputStyleConfig on success.
func ParseOutputStyleYAML(data []byte) (*OutputStyleConfig, error) {
	// Try the scalar form first.
	var asString string
	if err := yaml.Unmarshal(data, &asString); err == nil && asString != "" {
		return &OutputStyleConfig{Active: asString}, nil
	}
	o := &OutputStyleConfig{}
	if err := yaml.Unmarshal(data, o); err != nil {
		return nil, fmt.Errorf("parsing outputStyle yaml: %w", err)
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}

// ParseOutputStyleJSON mirrors ParseOutputStyleYAML for JSON input.
func ParseOutputStyleJSON(data []byte) (*OutputStyleConfig, error) {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil && asString != "" {
		return &OutputStyleConfig{Active: asString}, nil
	}
	o := &OutputStyleConfig{}
	if err := json.Unmarshal(data, o); err != nil {
		return nil, fmt.Errorf("parsing outputStyle json: %w", err)
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}
