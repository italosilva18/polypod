package command

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// CommandType defines the category of a command.
type CommandType string

const (
	// CommandLocal executes locally and returns text output.
	CommandLocal CommandType = "local"

	// CommandPrompt expands to text sent to the model.
	CommandPrompt CommandType = "prompt"

	// CommandLocalJSX renders interactive UI (not for remote/headless).
	CommandLocalJSX CommandType = "local-jsx"
)

// CommandSource indicates where the command was loaded from.
type CommandSource string

const (
	SourceBuiltin     CommandSource = "builtin"
	SourceBundled     CommandSource = "bundled"
	SourceSkills      CommandSource = "skills"
	SourcePlugin      CommandSource = "plugin"
	SourceMCP         CommandSource = "mcp"
	SourceCommandsDir CommandSource = "commands_DEPRECATED"
	SourceDynamic     CommandSource = "dynamic"
)

// Command defines a slash command.
type Command struct {
	Name        string        `json:"name" yaml:"name"`
	Aliases     []string      `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Description string        `json:"description" yaml:"description"`
	Type        CommandType   `json:"type" yaml:"type"`
	Source      CommandSource `json:"source" yaml:"source"`

	// For type=prompt: expands to text sent to the model.
	GetPrompt func(args []string, ctx CommandContext) (string, error) `json:"-" yaml:"-"`

	// For type=local: executes and returns display result.
	Execute func(args []string, ctx CommandContext) (*CommandResult, error) `json:"-" yaml:"-"`

	// Metadata
	Disabled               bool     `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	DisableModelInvocation bool     `json:"disable_model_invocation,omitempty" yaml:"disable_model_invocation,omitempty"`
	Aliases2               []string `json:"-" yaml:"-"`                           // additional aliases loaded from config
	Kind                   string   `json:"kind,omitempty" yaml:"kind,omitempty"` // "workflow", etc.
}

// CompactResult holds the outcome of a compaction operation, returned by
// CommandContext.CompactFn. This is a lightweight mirror of compact.CompactResult
// so the command package does not import internal/compact.
type CompactResult struct {
	PreCompactTokens  int
	PostCompactTokens int
	MessagesRemoved   int
}

// BudgetSnapshot holds token-budget state returned by CommandContext.BudgetReportFn.
// This is a lightweight mirror so the command package does not import internal/tokenbudget.
type BudgetSnapshot struct {
	Used  int
	Limit int
	Turns int
	// Report is a pre-formatted multi-line breakdown (from BudgetTracker.BudgetReport).
	Report string
}

// CommandContext provides context to command handlers.
type CommandContext struct {
	WorkingDir string
	SessionID  string
	Verbose    bool

	// --- Context-management wiring (optional) ---
	//
	// These fields are nil until the orchestrator (or main.go) wires the
	// context-management subsystem. Commands must check for nil before calling.

	// CompactFn triggers conversation compaction. The string argument carries
	// optional custom instructions (may be empty). Returns a CompactResult or
	// an error. The second return string is a human-readable error category
	// ("not_enough", "in_progress", etc.) that commands can match on.
	CompactFn func(customInstructions string) (*CompactResult, error)

	// SnipFn triggers structural snipping of old tool results (no LLM call).
	// Returns the estimated number of tokens saved.
	SnipFn func() (tokensSaved int, err error)

	// BudgetReportFn returns the current token budget snapshot.
	BudgetReportFn func() (*BudgetSnapshot, error)
}

// CommandResult is the output of a local command.
type CommandResult struct {
	Display string // text shown to user
	Model   string // text sent to model (if different)
}

// Registry manages all available commands.
type Registry struct {
	mu       sync.RWMutex
	commands map[string]*Command // name -> command (includes aliases)
}

// NewRegistry creates an empty command registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]*Command),
	}
}

// Register adds a command.
func (r *Registry) Register(cmd *Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[cmd.Name] = cmd
	for _, alias := range cmd.Aliases {
		r.commands[alias] = cmd
	}
}

// Get returns a command by name or alias.
func (r *Registry) Get(name string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.commands[name]
	if ok && !cmd.Disabled {
		return cmd, true
	}
	return nil, false
}

// List returns all unique commands (deduplicated by name).
func (r *Registry) List() []*Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool)
	var result []*Command
	for _, cmd := range r.commands {
		if !seen[cmd.Name] && !cmd.Disabled {
			seen[cmd.Name] = true
			result = append(result, cmd)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListByType returns commands filtered by type.
func (r *Registry) ListByType(cmdType CommandType) []*Command {
	var result []*Command
	for _, cmd := range r.List() {
		if cmd.Type == cmdType {
			result = append(result, cmd)
		}
	}
	return result
}

// ListEnabled returns only enabled commands.
func (r *Registry) ListEnabled() []*Command {
	var result []*Command
	for _, cmd := range r.List() {
		if !cmd.Disabled {
			result = append(result, cmd)
		}
	}
	return result
}

// ModelInvocable returns commands the model can invoke (prompt type, not disabled).
func (r *Registry) ModelInvocable() []*Command {
	var result []*Command
	for _, cmd := range r.List() {
		if cmd.Type == CommandPrompt && !cmd.DisableModelInvocation && cmd.Source != SourceBuiltin {
			result = append(result, cmd)
		}
	}
	return result
}

// Remove removes a command by name.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd, ok := r.commands[name]
	if !ok {
		return
	}
	delete(r.commands, cmd.Name)
	for _, alias := range cmd.Aliases {
		delete(r.commands, alias)
	}
}

// Count returns the number of unique commands.
func (r *Registry) Count() int {
	return len(r.List())
}

// Has returns true if a command with the given name exists and is enabled.
func (r *Registry) Has(name string) bool {
	_, ok := r.Get(name)
	return ok
}

// Find searches for commands matching a query in name or description.
func (r *Registry) Find(query string) []*Command {
	query = strings.ToLower(query)
	var result []*Command
	for _, cmd := range r.List() {
		if strings.Contains(strings.ToLower(cmd.Name), query) ||
			strings.Contains(strings.ToLower(cmd.Description), query) {
			result = append(result, cmd)
		}
	}
	return result
}

// FormatList formats all commands for display.
func (r *Registry) FormatList() string {
	commands := r.List()
	var sb strings.Builder
	for _, cmd := range commands {
		aliases := ""
		if len(cmd.Aliases) > 0 {
			aliases = fmt.Sprintf(" (aliases: %s)", strings.Join(cmd.Aliases, ", "))
		}
		source := ""
		if cmd.Source != SourceBuiltin {
			source = fmt.Sprintf(" [%s]", cmd.Source)
		}
		sb.WriteString(fmt.Sprintf("/%s%s%s — %s\n", cmd.Name, aliases, source, cmd.Description))
	}
	return sb.String()
}

// RegisterBuiltinCommands registers core slash commands.
func RegisterBuiltinCommands(reg *Registry) {
	reg.Register(&Command{
		Name:        "help",
		Description: "Mostrar ajuda e comandos disponiveis",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "clear",
		Aliases:     []string{"cls"},
		Description: "Limpar o historico de conversa",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "exit",
		Aliases:     []string{"quit", "q"},
		Description: "Sair do programa",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "cost",
		Description: "Mostrar custo da sessao atual",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "model",
		Description: "Trocar o modelo de AI",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "status",
		Description: "Mostrar status do sistema",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "session",
		Aliases:     []string{"sessions"},
		Description: "Gerenciar sessoes de conversa",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "memory",
		Description: "Gerenciar memoria persistente",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "permissions",
		Aliases:     []string{"perm"},
		Description: "Gerenciar permissoes de ferramentas",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "config",
		Description: "Visualizar/alterar configuracao",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "doctor",
		Description: "Verificar diagnostico do sistema",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "compact",
		Description: "Compactar contexto da conversa",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "init",
		Description: "Inicializar projeto com .polypod.md",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "theme",
		Description: "Trocar tema visual",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
	reg.Register(&Command{
		Name:        "version",
		Aliases:     []string{"v"},
		Description: "Mostrar versao",
		Type:        CommandLocal,
		Source:      SourceBuiltin,
	})
}
