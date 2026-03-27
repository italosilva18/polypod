package template

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
)

// Template is a reusable prompt pattern.
type Template struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	System      string `yaml:"system"`
	User        string `yaml:"user"` // {{input}} placeholder
	Author      string `yaml:"author"`
}

// Registry holds all loaded templates.
type Registry struct {
	templates map[string]*Template
	dir       string
}

// NewRegistry creates a template registry.
func NewRegistry(dir string) *Registry {
	return &Registry{templates: make(map[string]*Template), dir: dir}
}

// LoadAll loads all .yaml templates from the directory.
func (r *Registry) LoadAll() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, e.Name()))
		if err != nil {
			continue
		}
		var t Template
		if err := yaml.Unmarshal(data, &t); err != nil {
			continue
		}
		if t.Name == "" {
			t.Name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		r.templates[t.Name] = &t
	}
	return nil
}

// Get returns a template by name.
func (r *Registry) Get(name string) (*Template, bool) {
	t, ok := r.templates[name]
	return t, ok
}

// List returns template names grouped by category.
func (r *Registry) List() map[string][]string {
	result := make(map[string][]string)
	for _, t := range r.templates {
		cat := t.Category
		if cat == "" {
			cat = "geral"
		}
		result[cat] = append(result[cat], t.Name)
	}
	for _, names := range result {
		sort.Strings(names)
	}
	return result
}

// Apply returns the system and user prompts with {{input}} replaced.
func (r *Registry) Apply(name, input string) (system, user string, err error) {
	t, ok := r.templates[name]
	if !ok {
		return "", "", fmt.Errorf("template '%s' nao encontrado", name)
	}
	system = t.System
	user = strings.ReplaceAll(t.User, "{{input}}", input)
	return system, user, nil
}

// RegisterSkills registers template management skills.
func (r *Registry) RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "template_list",
		Description: "Listar templates de prompt disponiveis",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			groups := r.List()
			if len(groups) == 0 {
				return "Nenhum template disponivel.", nil
			}
			var sb strings.Builder
			for cat, names := range groups {
				fmt.Fprintf(&sb, "## %s\n", cat)
				for _, n := range names {
					t := r.templates[n]
					fmt.Fprintf(&sb, "  - **%s**: %s\n", n, t.Description)
				}
				sb.WriteByte('\n')
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "template_apply",
		Description: "Aplicar um template de prompt ao input fornecido",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":  {Type: jsonschema.String, Description: "Nome do template"},
				"input": {Type: jsonschema.String, Description: "Texto de entrada"},
			},
			Required: []string{"name", "input"},
		},
		Execute: func(args map[string]string) (string, error) {
			sys, usr, err := r.Apply(args["name"], args["input"])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("[System]\n%s\n\n[User]\n%s", sys, usr), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "template_create",
		Description: "Criar um novo template de prompt",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":        {Type: jsonschema.String, Description: "Nome do template"},
				"description": {Type: jsonschema.String, Description: "Descricao"},
				"category":    {Type: jsonschema.String, Description: "Categoria (coding, writing, analysis, devops)"},
				"system":      {Type: jsonschema.String, Description: "System prompt"},
				"user":        {Type: jsonschema.String, Description: "User prompt (use {{input}} como placeholder)"},
			},
			Required: []string{"name", "description", "system", "user"},
		},
		Execute: func(args map[string]string) (string, error) {
			t := Template{
				Name:        args["name"],
				Description: args["description"],
				Category:    args["category"],
				System:      args["system"],
				User:        args["user"],
			}
			data, err := yaml.Marshal(t)
			if err != nil {
				return "", err
			}
			os.MkdirAll(r.dir, 0755)
			path := filepath.Join(r.dir, t.Name+".yaml")
			if err := os.WriteFile(path, data, 0644); err != nil {
				return "", err
			}
			r.templates[t.Name] = &t
			return fmt.Sprintf("Template '%s' criado em %s", t.Name, path), nil
		},
	})
}
