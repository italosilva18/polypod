package skill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"text/template"
	"time"

	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
)

const maxScriptBytes = 4096

var validName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// CustomSkillDef defines a user-created script-based skill.
type CustomSkillDef struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Parameters  map[string]string `yaml:"parameters"` // name -> description
	Script      string            `yaml:"script"`      // bash template with {{.param}}
	CreatedAt   time.Time         `yaml:"created_at"`
}

// LoadAndRegisterCustomSkills loads all custom skill YAML files from a directory and registers them.
func LoadAndRegisterCustomSkills(reg *Registry, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading custom skills dir: %w", err)
	}

	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var def CustomSkillDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			continue
		}

		if def.Name == "" || def.Script == "" {
			continue
		}

		RegisterCustomSkill(reg, &def)
	}
	return nil
}

// RegisterCustomSkill registers a single custom skill in the registry.
func RegisterCustomSkill(reg *Registry, def *CustomSkillDef) {
	// Build parameter schema
	props := make(map[string]jsonschema.Definition)
	required := make([]string, 0)
	for name, desc := range def.Parameters {
		props[name] = jsonschema.Definition{
			Type:        jsonschema.String,
			Description: desc,
		}
		required = append(required, name)
	}

	scriptTemplate := def.Script

	reg.Register(&Skill{
		Name:        def.Name,
		Description: def.Description,
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: props,
			Required:   required,
		},
		Execute: func(args map[string]string) (string, error) {
			// Render script template with args
			tmpl, err := template.New("script").Parse(scriptTemplate)
			if err != nil {
				return "", fmt.Errorf("parsing script template: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, args); err != nil {
				return "", fmt.Errorf("rendering script: %w", err)
			}

			// Execute with same timeout as built-in run_command
			ctx, cancel := context.WithTimeout(context.Background(), ExecTimeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, "bash", "-c", buf.String())
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return string(out) + "\n[timeout: script excedeu 30s]", nil
			}
			if err != nil {
				return string(out) + "\n[exit: " + err.Error() + "]", nil
			}
			return string(out), nil
		},
	})
}

// ListCustomSkills returns all custom skill definitions from a directory.
func ListCustomSkills(dir string) ([]CustomSkillDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading custom skills dir: %w", err)
	}

	var defs []CustomSkillDef
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var def CustomSkillDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			continue
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// SaveCustomSkill saves a custom skill definition to a YAML file.
func SaveCustomSkill(dir string, def *CustomSkillDef) error {
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("serializing skill: %w", err)
	}
	path := filepath.Join(dir, def.Name+".yaml")
	return os.WriteFile(path, data, 0644)
}

// DeleteCustomSkill removes a custom skill YAML file.
func DeleteCustomSkill(dir, name string) error {
	path := filepath.Join(dir, name+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill '%s' nao encontrada", name)
		}
		return fmt.Errorf("removendo skill: %w", err)
	}
	return nil
}

