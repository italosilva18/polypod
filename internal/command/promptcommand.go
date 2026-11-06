package command

import (
	"fmt"
	"sort"
	"sync"
)

// PromptContext is the execution context passed to a PromptCommand's
// GetPromptFn when expanding the command into a prompt string.
//
// Unlike CommandContext (used by local commands), PromptContext is designed
// to carry everything a prompt template might need to interpolate shell
// output, environment variables, or session state into the expanded prompt.
//
// Additional extensibility is provided via Extras, so the dispatcher can
// plug new fields without breaking existing prompt commands.
type PromptContext struct {
	// Args is the raw argument string following the command name, e.g.
	// for `/review 123` the Args value is "123".
	Args string

	// CWD is the working directory the command was invoked from.
	CWD string

	// SessionID identifies the conversation session (used for logging,
	// plugin hooks, or telemetry on behalf of the expansion).
	SessionID string

	// Env is a snapshot of relevant environment variables. Callers may
	// pass os.Environ()-derived maps or a filtered subset.
	Env map[string]string

	// Extras is a free-form bag of additional context fields (e.g. a
	// handle to the app state, a tool context, or a plugin manifest).
	// Prompt commands should document the keys they read.
	Extras map[string]any
}

// PromptCommandResult is the output of expanding a PromptCommand.
//
// PromptText is the expanded content that will be injected into the
// conversation. InjectAsUser chooses whether it enters as a user turn
// (true, like the TS PromptCommand behaviour) or as a system-level
// instruction (false). Display is an optional short message echoed
// locally to the user before the expansion is sent to the model.
type PromptCommandResult struct {
	// PromptText is the expanded prompt body sent to the model.
	PromptText string

	// InjectAsUser is true when the expansion should be injected as a
	// user turn in the conversation. When false, callers should treat
	// the result as system context (e.g. an isMeta message).
	InjectAsUser bool

	// Display is an optional line echoed to the user locally so they
	// see what the command did before the model replies. If empty,
	// dispatchers should fall back to a generic "/<name> running..."
	// indicator.
	Display string
}

// PromptCommand is a slash command that, instead of executing local code
// and printing a result, expands to a prompt body that is sent to the
// model as the next turn of the conversation. Examples: `/review`,
// `/commit`, `/explain`.
//
// This mirrors the TS `PromptCommand` type but is kept as a dedicated Go
// struct (not folded into Command) so the dispatcher can statically
// distinguish the two execution paths.
type PromptCommand struct {
	// Name is the slash name without the leading "/". Required.
	Name string

	// Description is a short human-readable summary used by /help and
	// typeahead.
	Description string

	// Aliases are alternative names that resolve to this command.
	Aliases []string

	// GetPromptFn is called by Expand to build the prompt body. It
	// should be pure with respect to the conversation and only read
	// side-effects it needs (git status, file contents, etc). The
	// returned string is the verbatim prompt content.
	GetPromptFn func(ctx PromptContext) (string, error)

	// InjectAsUser controls the default injection mode for the result
	// of Expand. Most built-in prompt commands (/review, /commit) want
	// true. Set to false for commands that should land as system-level
	// context instead of a user turn.
	InjectAsUser bool

	// ProgressMessage is an optional short label shown while the model
	// is processing the expansion (e.g. "creating commit").
	ProgressMessage string
}

// Expand runs the command's GetPromptFn and wraps the result.
//
// It returns an error when the command has no GetPromptFn or when the
// function itself returns an error. The returned PromptCommandResult is
// never nil on success.
func (pc *PromptCommand) Expand(ctx PromptContext) (*PromptCommandResult, error) {
	if pc == nil {
		return nil, fmt.Errorf("promptcommand: Expand called on nil command")
	}
	if pc.GetPromptFn == nil {
		return nil, fmt.Errorf("promptcommand: command %q has no GetPromptFn", pc.Name)
	}
	text, err := pc.GetPromptFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("promptcommand: %s: %w", pc.Name, err)
	}
	display := pc.ProgressMessage
	if display == "" {
		display = "/" + pc.Name
	}
	return &PromptCommandResult{
		PromptText:   text,
		InjectAsUser: pc.InjectAsUser,
		Display:      display,
	}, nil
}

// PromptRegistry keeps track of all registered PromptCommand instances.
//
// It is intentionally separate from Registry (which stores *Command) so
// the dispatcher can look up the right execution path by name. Both
// registries may contain commands with distinct names; IsPromptCommandName
// is the helper used by the dispatcher to decide which one to query.
type PromptRegistry struct {
	mu       sync.RWMutex
	commands map[string]*PromptCommand // canonical name -> command
	aliases  map[string]string         // alias -> canonical name
}

// NewPromptRegistry returns an empty PromptRegistry.
func NewPromptRegistry() *PromptRegistry {
	return &PromptRegistry{
		commands: make(map[string]*PromptCommand),
		aliases:  make(map[string]string),
	}
}

// Register adds (or replaces) a PromptCommand in the registry. Existing
// aliases that collide with the new command are overwritten.
func (pr *PromptRegistry) Register(cmd *PromptCommand) {
	if cmd == nil || cmd.Name == "" {
		return
	}
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.commands[cmd.Name] = cmd
	for _, a := range cmd.Aliases {
		if a == "" {
			continue
		}
		pr.aliases[a] = cmd.Name
	}
}

// Get resolves a name or alias to a PromptCommand. Returns (nil, false)
// if the name does not exist in this registry.
func (pr *PromptRegistry) Get(name string) (*PromptCommand, bool) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if cmd, ok := pr.commands[name]; ok {
		return cmd, true
	}
	if canonical, ok := pr.aliases[name]; ok {
		if cmd, ok := pr.commands[canonical]; ok {
			return cmd, true
		}
	}
	return nil, false
}

// List returns all registered prompt commands sorted by name. The
// returned slice is a fresh copy — callers may mutate it freely.
func (pr *PromptRegistry) List() []*PromptCommand {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	out := make([]*PromptCommand, 0, len(pr.commands))
	for _, cmd := range pr.commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Aliases returns a snapshot of the alias -> canonical name map.
func (pr *PromptRegistry) Aliases() map[string]string {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	out := make(map[string]string, len(pr.aliases))
	for k, v := range pr.aliases {
		out[k] = v
	}
	return out
}

// Has reports whether a command with the given name or alias exists.
func (pr *PromptRegistry) Has(name string) bool {
	_, ok := pr.Get(name)
	return ok
}

// Count returns the number of canonical commands registered.
func (pr *PromptRegistry) Count() int {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return len(pr.commands)
}

// IsPromptCommandName is the helper the dispatcher uses to decide whether
// a parsed slash token should be routed to the PromptRegistry (and
// expanded into a model turn) rather than to the local Registry.
//
// It returns false for a nil registry so callers can pass a registry
// that has not been wired up yet without crashing.
func IsPromptCommandName(name string, pr *PromptRegistry) bool {
	if pr == nil || name == "" {
		return false
	}
	return pr.Has(name)
}
