package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
	"gopkg.in/yaml.v3"
)

// Plugin describes an installable plugin.
type Plugin struct {
	Name        string        `yaml:"name"`
	Version     string        `yaml:"version"`
	Description string        `yaml:"description"`
	Author      string        `yaml:"author"`
	Homepage    string        `yaml:"homepage"`
	License     string        `yaml:"license"`
	Skills      []PluginSkill `yaml:"skills"`
	InstalledAt time.Time     `yaml:"installed_at"`
	Dir         string        `yaml:"-"`
}

// PluginSkill defines a single capability provided by a plugin.
type PluginSkill struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Command     string        `yaml:"command"`
	Script      string        `yaml:"script"`
	Language    string        `yaml:"language"` // bash, python, node
	Parameters  []PluginParam `yaml:"parameters"`
	Timeout     int           `yaml:"timeout"`
}

// PluginParam is a skill parameter.
type PluginParam struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
}

// Manager handles plugin lifecycle.
type Manager struct {
	pluginsDir string
	plugins    map[string]*Plugin
	logger     *slog.Logger
}

// NewManager creates a plugin manager.
func NewManager(pluginsDir string, logger *slog.Logger) *Manager {
	os.MkdirAll(pluginsDir, 0755)
	return &Manager{
		pluginsDir: pluginsDir,
		plugins:    make(map[string]*Plugin),
		logger:     logger,
	}
}

// LoadAll loads all installed plugins.
func (m *Manager) LoadAll() error {
	entries, err := os.ReadDir(m.pluginsDir)
	if err != nil {
		return nil // no plugins dir is fine
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.pluginsDir, e.Name())
		p, err := LoadPlugin(dir)
		if err != nil {
			m.logger.Warn("plugin load failed", "dir", dir, "error", err)
			continue
		}
		m.plugins[p.Name] = p
	}
	m.logger.Info("plugins carregados", "count", len(m.plugins))
	return nil
}

// LoadPlugin reads a plugin.yaml from a directory.
func LoadPlugin(dir string) (*Plugin, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return nil, fmt.Errorf("lendo plugin.yaml: %w", err)
	}
	var p Plugin
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing plugin.yaml: %w", err)
	}
	p.Dir = dir
	if p.Name == "" {
		p.Name = filepath.Base(dir)
	}
	return &p, nil
}

// Install installs a plugin from a git URL or local path.
func (m *Manager) Install(source string) error {
	if strings.HasPrefix(source, "http") || strings.HasPrefix(source, "git@") {
		name := filepath.Base(strings.TrimSuffix(source, ".git"))
		dest := filepath.Join(m.pluginsDir, name)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "clone", source, dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone: %s\n%w", string(out), err)
		}
		p, err := LoadPlugin(dest)
		if err != nil {
			os.RemoveAll(dest)
			return err
		}
		p.InstalledAt = time.Now()
		m.plugins[p.Name] = p
		return nil
	}

	// Local path
	p, err := LoadPlugin(source)
	if err != nil {
		return err
	}
	dest := filepath.Join(m.pluginsDir, p.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cp", "-r", source, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp: %s\n%w", string(out), err)
	}
	p.Dir = dest
	p.InstalledAt = time.Now()
	m.plugins[p.Name] = p
	return nil
}

// Remove removes an installed plugin.
func (m *Manager) Remove(name string) error {
	p, ok := m.plugins[name]
	if !ok {
		return fmt.Errorf("plugin '%s' nao encontrado", name)
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return err
	}
	delete(m.plugins, name)
	return nil
}

// Get returns a plugin by name.
func (m *Manager) Get(name string) (*Plugin, bool) {
	p, ok := m.plugins[name]
	return p, ok
}

// List returns all installed plugins.
func (m *Manager) List() []*Plugin {
	result := make([]*Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		result = append(result, p)
	}
	return result
}

// RegisterAllSkills registers skills from all plugins into the skill registry.
func (m *Manager) RegisterAllSkills(reg *skill.Registry) int {
	count := 0
	for _, p := range m.plugins {
		for _, ps := range p.Skills {
			s := m.pluginSkillToSkill(p, ps)
			reg.Register(s)
			count++
		}
	}
	return count
}

func (m *Manager) pluginSkillToSkill(p *Plugin, ps PluginSkill) *skill.Skill {
	skillName := fmt.Sprintf("%s_%s", p.Name, ps.Name)

	params := jsonschema.Definition{
		Type:       jsonschema.Object,
		Properties: make(map[string]jsonschema.Definition),
	}
	for _, pp := range ps.Parameters {
		params.Properties[pp.Name] = jsonschema.Definition{
			Type:        jsonschema.String,
			Description: pp.Description,
		}
		if pp.Required {
			params.Required = append(params.Required, pp.Name)
		}
	}

	timeout := ps.Timeout
	if timeout == 0 {
		timeout = 30
	}

	return &skill.Skill{
		Name:        skillName,
		Description: fmt.Sprintf("[Plugin:%s] %s", p.Name, ps.Description),
		Parameters:  params,
		Execute: func(args map[string]string) (string, error) {
			return m.executePluginSkill(p, ps, args, timeout)
		},
	}
}

func (m *Manager) executePluginSkill(p *Plugin, ps PluginSkill, args map[string]string, timeout int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Set env vars from args
	env := os.Environ()
	env = append(env, "POLYPOD_PLUGIN_DIR="+p.Dir)
	for k, v := range args {
		env = append(env, fmt.Sprintf("POLYPOD_ARG_%s=%s", strings.ToUpper(k), v))
	}

	if ps.Command != "" {
		cmd := exec.CommandContext(ctx, "bash", "-c", ps.Command)
		cmd.Dir = p.Dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return string(out) + "\n[timeout]", nil
		}
		if err != nil {
			return string(out), nil
		}
		return strings.TrimSpace(string(out)), nil
	}

	if ps.Script != "" {
		tmpFile, err := os.CreateTemp("", "polypod-plugin-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString(ps.Script)
		tmpFile.Close()

		var interpreter string
		switch ps.Language {
		case "python":
			interpreter = "python3"
		case "node", "javascript":
			interpreter = "node"
		default:
			interpreter = "bash"
		}

		// Inject args as JSON via stdin
		argsJSON, _ := json.Marshal(args)

		cmd := exec.CommandContext(ctx, interpreter, tmpFile.Name())
		cmd.Dir = p.Dir
		cmd.Env = append(env, "POLYPOD_ARGS="+string(argsJSON))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), nil
		}
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("skill '%s' nao tem command nem script", ps.Name)
}

// RegisterManagementSkills registers plugin management skills.
func RegisterManagementSkills(reg *skill.Registry, mgr *Manager) {
	reg.Register(&skill.Skill{
		Name:        "plugin_list",
		Description: "Listar plugins instalados e suas skills",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			plugins := mgr.List()
			if len(plugins) == 0 {
				return "Nenhum plugin instalado.", nil
			}
			var sb strings.Builder
			for _, p := range plugins {
				fmt.Fprintf(&sb, "## %s v%s\n", p.Name, p.Version)
				fmt.Fprintf(&sb, "%s\n", p.Description)
				for _, s := range p.Skills {
					fmt.Fprintf(&sb, "  - %s_%s: %s\n", p.Name, s.Name, s.Description)
				}
				sb.WriteByte('\n')
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "plugin_install",
		Description: "Instalar um plugin (URL git ou caminho local)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"source": {Type: jsonschema.String, Description: "URL git ou caminho local do plugin"},
			},
			Required: []string{"source"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := mgr.Install(args["source"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Plugin instalado com sucesso de %s", args["source"]), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "plugin_remove",
		Description: "Remover um plugin instalado",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do plugin"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := mgr.Remove(args["name"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Plugin '%s' removido.", args["name"]), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "plugin_create",
		Description: "Criar um template de plugin em um diretorio",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name": {Type: jsonschema.String, Description: "Nome do plugin"},
				"path": {Type: jsonschema.String, Description: "Diretorio (default: ./plugins/<name>)"},
			},
			Required: []string{"name"},
		},
		Execute: func(args map[string]string) (string, error) {
			name := args["name"]
			dir := args["path"]
			if dir == "" {
				dir = filepath.Join("plugins", name)
			}
			os.MkdirAll(dir, 0755)

			template := fmt.Sprintf(`name: %s
version: "1.0.0"
description: "Descricao do plugin %s"
author: "Seu Nome"
skills:
  - name: hello
    description: "Skill de exemplo"
    language: bash
    script: |
      echo "Ola do plugin %s! Args: $POLYPOD_ARGS"
    parameters:
      - name: mensagem
        description: "Mensagem para exibir"
        required: false
`, name, name, name)

			path := filepath.Join(dir, "plugin.yaml")
			if err := os.WriteFile(path, []byte(template), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Template de plugin criado em %s", dir), nil
		},
	})
}
