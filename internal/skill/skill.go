package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const (
	MaxOutputBytes = 10 * 1024 // 10KB
	ExecTimeout    = 30 * time.Second
)

// Skill defines a callable capability.
type Skill struct {
	Name        string
	Description string
	Parameters  jsonschema.Definition
	Execute     func(args map[string]string) (string, error)
}

// Registry holds all registered skills.
type Registry struct {
	skills map[string]*Skill
}

// NewRegistry creates a registry with all built-in skills.
func NewRegistry() *Registry {
	r := &Registry{skills: make(map[string]*Skill)}
	r.registerBuiltins()
	return r
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// List returns all skill names.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	return names
}

// ToolDefinitions returns OpenAI tool definitions for the given skill names.
// If names is empty, returns all skills.
func (r *Registry) ToolDefinitions(names []string) []openai.Tool {
	if len(names) == 0 {
		names = r.List()
	}
	tools := make([]openai.Tool, 0, len(names))
	for _, name := range names {
		s, ok := r.skills[name]
		if !ok {
			continue
		}
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return tools
}

// Execute runs a skill by name with JSON arguments.
func (r *Registry) Execute(name string, argsJSON string) (string, error) {
	s, ok := r.skills[name]
	if !ok {
		return "", fmt.Errorf("skill desconhecida: %s", name)
	}

	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parsing args: %w", err)
	}

	result, err := s.Execute(args)
	if err != nil {
		return fmt.Sprintf("Erro: %v", err), nil
	}
	return truncate(result), nil
}

// Register adds a skill to the registry.
func (r *Registry) Register(s *Skill) {
	r.skills[s.Name] = s
}

// Unregister removes a skill from the registry.
func (r *Registry) Unregister(name string) {
	delete(r.skills, name)
}

func (r *Registry) registerBuiltins() {
	r.Register(&Skill{
		Name:        "read_file",
		Description: "Ler o conteudo de um arquivo no sistema local",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho absoluto ou relativo do arquivo"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			data, err := os.ReadFile(args["path"])
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	})

	r.Register(&Skill{
		Name:        "list_directory",
		Description: "Listar arquivos e pastas em um diretorio",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"path": {Type: jsonschema.String, Description: "Caminho do diretorio a listar"},
			},
			Required: []string{"path"},
		},
		Execute: func(args map[string]string) (string, error) {
			entries, err := os.ReadDir(args["path"])
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				t := "-"
				if e.IsDir() {
					t = "d"
				}
				sb.WriteString(fmt.Sprintf("%s %8d %s %s\n",
					t, info.Size(), info.ModTime().Format("2006-01-02 15:04"), e.Name()))
			}
			return sb.String(), nil
		},
	})

	r.Register(&Skill{
		Name:        "run_command",
		Description: "Executar um comando shell no sistema local",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"command": {Type: jsonschema.String, Description: "Comando a executar (ex: uname -a, ls -la)"},
			},
			Required: []string{"command"},
		},
		Execute: func(args map[string]string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), ExecTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", args["command"])
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return string(out) + "\n[timeout: comando excedeu 30s]", nil
			}
			if err != nil {
				return string(out) + "\n[exit: " + err.Error() + "]", nil
			}
			return string(out), nil
		},
	})

	r.Register(&Skill{
		Name:        "search_files",
		Description: "Buscar arquivos por glob pattern (ex: *.go, **/*.md)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"pattern": {Type: jsonschema.String, Description: "Glob pattern para buscar (ex: *.go, **/*.yaml)"},
				"path":    {Type: jsonschema.String, Description: "Diretorio base para a busca (default: diretorio atual)"},
			},
			Required: []string{"pattern"},
		},
		Execute: func(args map[string]string) (string, error) {
			base := args["path"]
			if base == "" {
				base = "."
			}
			var matches []string
			filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				matched, _ := filepath.Match(args["pattern"], info.Name())
				if matched {
					matches = append(matches, path)
				}
				return nil
			})
			if len(matches) == 0 {
				return "Nenhum arquivo encontrado.", nil
			}
			return strings.Join(matches, "\n"), nil
		},
	})
}

func truncate(s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	return s[:MaxOutputBytes] + "\n... [saida truncada em 10KB]"
}
