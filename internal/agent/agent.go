package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/types"
	"gopkg.in/yaml.v3"
)

// Agent defines a configurable AI personality with specific skills.
type Agent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Persona     string   `yaml:"persona"`
	Skills      []string `yaml:"skills"`
	Model       string   `yaml:"model,omitempty"`
	MaxTokens   int      `yaml:"max_tokens,omitempty"`
	Temperature float32  `yaml:"temperature,omitempty"`

	// Enhanced fields (inspired by Claude Code AgentDefinition)
	Aliases     []string `yaml:"aliases,omitempty"`      // alternate names for lookup
	IsBuiltIn   bool     `yaml:"-"`                      // true for built-in agents
	ParentAgent string   `yaml:"parent_agent,omitempty"` // for agent inheritance
}

// Registry holds all loaded agents.
type Registry struct {
	agents  map[string]*Agent
	Default string
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents:  make(map[string]*Agent),
		Default: "default",
	}
}

// Register adds an agent to the registry.
func (r *Registry) Register(a *Agent) {
	r.agents[a.Name] = a
	// Also register under aliases
	for _, alias := range a.Aliases {
		r.agents[alias] = a
	}
}

// Get returns an agent by name. Falls back to default.
func (r *Registry) Get(name string) *Agent {
	if a, ok := r.agents[name]; ok {
		return a
	}
	if a, ok := r.agents[r.Default]; ok {
		return a
	}
	return DefaultAgent()
}

// Has returns true if an agent with the given name exists.
func (r *Registry) Has(name string) bool {
	_, ok := r.agents[name]
	return ok
}

// List returns all agent names (deduplicated, excluding aliases).
func (r *Registry) List() []string {
	seen := make(map[string]bool)
	names := make([]string, 0, len(r.agents))
	for _, a := range r.agents {
		if !seen[a.Name] {
			seen[a.Name] = true
			names = append(names, a.Name)
		}
	}
	return names
}

// Count returns the number of registered agents.
func (r *Registry) Count() int {
	return len(r.List())
}

// LoadDir loads all .yaml/.yml agent definitions from a directory.
func (r *Registry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // directory doesn't exist, that's fine
		}
		return fmt.Errorf("reading agents dir: %w", err)
	}

	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("reading agent %s: %w", entry.Name(), err)
		}

		var a Agent
		if err := yaml.Unmarshal(data, &a); err != nil {
			return fmt.Errorf("parsing agent %s: %w", entry.Name(), err)
		}

		if a.Name == "" {
			a.Name = entry.Name()[:len(entry.Name())-len(ext)]
		}

		r.Register(&a)
	}
	return nil
}

// AgentID returns a types.AgentID for the given agent name.
func (r *Registry) AgentID(name string) types.AgentID {
	if !r.Has(name) {
		name = r.Default
	}
	return types.NewAgentID(name)
}

// DefaultAgent returns the built-in default agent.
func DefaultAgent() *Agent {
	return &Agent{
		Name:        "default",
		Description: "Assistente padrao com acesso total ao sistema",
		IsBuiltIn:   true,
		Persona: `Voce e um assistente inteligente com acesso total ao sistema local. Siga estas regras:

1. Voce tem ferramentas para acessar o sistema: ler arquivos, listar diretorios, executar comandos e buscar arquivos.
2. Quando o usuario pedir para ler um arquivo, listar pastas, executar comandos ou buscar arquivos, USE as ferramentas disponiveis.
3. Seja conciso e direto nas respostas.
4. Responda no mesmo idioma da pergunta do usuario.
5. Se tiver contexto da base de conhecimento, use-o tambem.
6. Ao mostrar conteudo de arquivos, formate de forma legivel.`,
		Skills: []string{"read_file", "list_directory", "run_command", "search_files"},
	}
}

// BuildSystemPrompt generates the system prompt for this agent, with optional knowledge context.
func (a *Agent) BuildSystemPrompt(knowledgeContext string) string {
	prompt := a.Persona
	if knowledgeContext != "" {
		prompt += fmt.Sprintf("\n\nCONTEXTO DISPONIVEL:\n---\n%s\n---", knowledgeContext)
	}
	return prompt
}

// GetEffectiveModel returns the model to use, falling back to the provided default.
func (a *Agent) GetEffectiveModel(defaultModel string) string {
	if a.Model != "" {
		return a.Model
	}
	return defaultModel
}

// GetEffectiveMaxTokens returns the max tokens, falling back to the provided default.
func (a *Agent) GetEffectiveMaxTokens(defaultMax int) int {
	if a.MaxTokens > 0 {
		return a.MaxTokens
	}
	return defaultMax
}
