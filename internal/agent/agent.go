package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/costa/polypod/internal/types"
	"gopkg.in/yaml.v3"
)

// Agent defines a configurable AI personality with specific skills.
// Migrated from Claude Code's AgentDefinition.
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
	mu            sync.RWMutex
	agents        map[string]*Agent
	dynamicSkills map[string]map[string]bool
	Default       string
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents:        make(map[string]*Agent),
		dynamicSkills: make(map[string]map[string]bool),
		Default:       "default",
	}
}

// Register adds an agent to the registry.
func (r *Registry) Register(a *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := cloneAgent(a)
	r.agents[a.Name] = copy
	// Also register under aliases
	for _, alias := range a.Aliases {
		r.agents[alias] = copy
	}
}

// AddSkill enables a skill on an agent without losing concurrent additions.
// Get returns snapshots, so callers must use this method for live changes.
func (r *Registry) AddSkill(agentName, skillName string) bool {
	if skillName == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[agentName]
	if !ok {
		return false
	}
	for _, existing := range a.Skills {
		if existing == skillName {
			return false
		}
	}
	if r.dynamicSkills[a.Name] == nil {
		r.dynamicSkills[a.Name] = make(map[string]bool)
	}
	if r.dynamicSkills[a.Name][skillName] {
		return false
	}
	r.dynamicSkills[a.Name][skillName] = true
	return true
}

// RemoveSkill disables a skill on an agent after its runtime adapter is gone.
func (r *Registry) RemoveSkill(agentName, skillName string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[agentName]
	if !ok {
		return false
	}
	if !r.dynamicSkills[a.Name][skillName] {
		return false
	}
	delete(r.dynamicSkills[a.Name], skillName)
	return true
}

// Get returns an agent by name. Falls back to default.
func (r *Registry) Get(name string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.agents[name]; ok {
		return r.withDynamicSkills(a)
	}
	if a, ok := r.agents[r.Default]; ok {
		return r.withDynamicSkills(a)
	}
	return DefaultAgent()
}

// withDynamicSkills must be called while the registry read lock is held.
func (r *Registry) withDynamicSkills(a *Agent) *Agent {
	copy := cloneAgent(a)
	seen := make(map[string]bool, len(copy.Skills))
	for _, name := range copy.Skills {
		seen[name] = true
	}
	var dynamicNames []string
	for name := range r.dynamicSkills[a.Name] {
		if !seen[name] {
			dynamicNames = append(dynamicNames, name)
		}
	}
	sort.Strings(dynamicNames)
	copy.Skills = append(copy.Skills, dynamicNames...)
	return copy
}

// Has returns true if an agent with the given name exists.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.agents[name]
	return ok
}

// List returns all agent names (deduplicated, excluding aliases).
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
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

func cloneAgent(a *Agent) *Agent {
	copy := *a
	copy.Skills = append([]string(nil), a.Skills...)
	copy.Aliases = append([]string(nil), a.Aliases...)
	return &copy
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
		Description: "Assistente padrão com acesso total ao sistema",
		IsBuiltIn:   true,
		Persona: `Você é um assistente inteligente com acesso total ao sistema local. Siga estas regras:

1. Você tem ferramentas para acessar o sistema: ler arquivos, listar diretórios, executar comandos e buscar arquivos.
2. Quando o usuário pedir para ler um arquivo, listar pastas, executar comandos ou buscar arquivos, USE as ferramentas disponíveis.
3. Seja conciso e direto nas respostas.
4. Responda no mesmo idioma da pergunta do usuário.
5. Se tiver contexto da base de conhecimento, use-o também.
6. Ao mostrar conteúdo de arquivos, formate de forma legível.`,
		Skills: []string{"read_file", "list_directory", "run_command", "search_files"},
	}
}

// BuildSystemPrompt generates the system prompt for this agent, with optional knowledge context.
func (a *Agent) BuildSystemPrompt(knowledgeContext string) string {
	prompt := a.Persona
	if knowledgeContext != "" {
		prompt += fmt.Sprintf("\n\nCONTEXTO DISPONÍVEL:\n---\n%s\n---", knowledgeContext)
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
