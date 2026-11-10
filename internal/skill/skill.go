package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"

	"github.com/costa/polypod/internal/types"
)

const (
	MaxOutputBytes = 10 * 1024 // 10KB
	ExecTimeout    = 30 * time.Second
)

// Skill defines a callable capability.
type Skill struct {
	Name        string
	Description string
	Origin      string // optional runtime source; used to avoid deleting unrelated skills
	Parameters  jsonschema.Definition
	Execute     func(args map[string]string) (string, error)
	// InputSchema and ExecuteResult support typed, multimodal external tools.
	InputSchema   map[string]any
	ExecuteResult func(context.Context, map[string]any) (types.ToolResult, error)
}

// FileSkillOrigin identifies the exact file that supplied a filesystem skill.
func FileSkillOrigin(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "file:" + filepath.Clean(path)
	}
	return "file:" + filepath.Clean(abs)
}

// PluginSkillOrigin keeps plugin ownership separate from ordinary filesystem
// skills, even when both refer to the same path during a manifest transition.
func PluginSkillOrigin(path string) string {
	return "plugin:" + strings.TrimPrefix(FileSkillOrigin(path), "file:")
}

// Registry holds all registered skills.
type Registry struct {
	mu                sync.RWMutex
	skills            map[string]*Skill
	patches           *PatchRegistry
	onChange          func(name string, exists bool)
	registrationGuard func(name string) error
}

// NewRegistry creates a registry with all built-in skills.
func NewRegistry() *Registry {
	r := &Registry{skills: make(map[string]*Skill)}
	r.registerBuiltins()
	return r
}

// SetPatchRegistry wires a PatchRegistry into the skill Registry so that
// patches are applied transparently on every Get and ToolDefinitions call.
func (r *Registry) SetPatchRegistry(pr *PatchRegistry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.patches = pr
}

// Get returns a skill by name, with any runtime patches applied.
func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	s, ok := r.skills[name]
	patches := r.patches
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if patches != nil {
		patched := ApplyPatches(*s, patches)
		return &patched, true
	}
	return s, ok
}

// List returns all skill names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]openai.Tool, 0, len(names))
	for _, name := range names {
		s, ok := r.skills[name]
		if !ok {
			continue
		}
		effective := s
		if r.patches != nil {
			patched := ApplyPatches(*s, r.patches)
			effective = &patched
		}
		var parameters any = effective.Parameters
		if effective.InputSchema != nil {
			parameters = effective.InputSchema
		}
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        effective.Name,
				Description: effective.Description,
				Parameters:  parameters,
			},
		})
	}
	return tools
}

// Execute runs a skill by name with JSON arguments.
func (r *Registry) Execute(name string, argsJSON string) (string, error) {
	result, err := r.ExecuteResult(name, argsJSON)
	return result.Content, err
}

// ExecuteResult preserves images and native JSON arguments for rich skills.
func (r *Registry) ExecuteResult(name string, argsJSON string) (types.ToolResult, error) {
	r.mu.RLock()
	s, ok := r.skills[name]
	r.mu.RUnlock()
	if !ok {
		return types.ToolResult{}, fmt.Errorf("skill desconhecida: %s", name)
	}
	var result types.ToolResult
	var err error
	if s.ExecuteResult != nil {
		var args map[string]any
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return result, fmt.Errorf("parsing args: %w", err)
		}
		result, err = s.ExecuteResult(context.Background(), args)
	} else {
		var args map[string]string
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return result, fmt.Errorf("parsing args: %w", err)
		}
		result.Content, err = s.Execute(args)
	}
	if err != nil {
		return types.ToolResult{Content: fmt.Sprintf("Erro: %v", err), IsError: true}, nil
	}
	result.Content = truncate(result.Content)
	return result, nil
}

// SetRegistrationGuard reserves names that ordinary loaders and plugins may
// not replace or remove. The guard runs while the registry lock is held.
func (r *Registry) SetRegistrationGuard(fn func(name string) error) {
	r.mu.Lock()
	r.registrationGuard = fn
	r.mu.Unlock()
}

// Register adds a skill to the registry and logs a rejected registration.
func (r *Registry) Register(s *Skill) {
	if err := r.TryRegister(s); err != nil {
		slog.Warn("skill registration rejected", "name", s.Name, "error", err)
	}
}

// TryRegister reports a registration denied by the configured guard.
func (r *Registry) TryRegister(s *Skill) error {
	return r.register(s, false)
}

// RegisterTrusted is for a file whose bytes and path have just been verified
// against the applied evolution draft. It bypasses only the name reservation.
func (r *Registry) RegisterTrusted(s *Skill) error {
	return r.register(s, true)
}

func (r *Registry) register(s *Skill, trusted bool) error {
	r.mu.Lock()
	if !trusted && r.registrationGuard != nil {
		if err := r.registrationGuard(s.Name); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	if !trusted {
		if existing, ok := r.skills[s.Name]; ok && existing.Origin != s.Origin &&
			(sourceOwnedSkill(existing.Origin) || sourceOwnedSkill(s.Origin)) {
			r.mu.Unlock()
			return fmt.Errorf("skill %q pertence a outra origem", s.Name)
		}
	}
	r.skills[s.Name] = s
	onChange := r.onChange
	r.mu.Unlock()
	if onChange != nil {
		onChange(s.Name, true)
	}
	return nil
}

// ReplacePluginSkills applies a plugin's complete desired skill set under one
// registry lock. current contains the exact origins last owned by that plugin.
// Name and registration-guard conflicts are checked before any change.
func (r *Registry) ReplacePluginSkills(current map[string]string, desired map[string]*Skill) error {
	r.mu.Lock()
	for name, s := range desired {
		if s == nil || s.Name != name || !strings.HasPrefix(s.Origin, "plugin:") {
			r.mu.Unlock()
			return fmt.Errorf("skill de plugin %q invalida", name)
		}
		if r.registrationGuard != nil {
			if err := r.registrationGuard(name); err != nil {
				r.mu.Unlock()
				return err
			}
		}
		if existing, ok := r.skills[name]; ok && existing.Origin != s.Origin {
			ownedOrigin, owned := current[name]
			if !owned || ownedOrigin != existing.Origin {
				r.mu.Unlock()
				return fmt.Errorf("skill %q pertence a outra origem", name)
			}
		}
	}
	for name, origin := range current {
		existing, ok := r.skills[name]
		if !ok || existing.Origin != origin {
			continue
		}
		if wanted, keep := desired[name]; keep && wanted.Origin == origin {
			continue
		}
		if r.registrationGuard != nil {
			if err := r.registrationGuard(name); err != nil {
				r.mu.Unlock()
				return err
			}
		}
	}
	changed := make(map[string]bool, len(current)+len(desired))
	for name, origin := range current {
		if existing, ok := r.skills[name]; ok && existing.Origin == origin {
			delete(r.skills, name)
			changed[name] = false
		}
	}
	for name, s := range desired {
		r.skills[name] = s
		changed[name] = true
	}
	onChange := r.onChange
	r.mu.Unlock()
	if onChange != nil {
		names := make([]string, 0, len(changed))
		for name := range changed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			onChange(name, changed[name])
		}
	}
	return nil
}

func sourceOwnedSkill(origin string) bool {
	return strings.HasPrefix(origin, "file:") || strings.HasPrefix(origin, "plugin:")
}

// Unregister removes a skill from the registry.
func (r *Registry) Unregister(name string) {
	if err := r.TryUnregister(name); err != nil {
		slog.Warn("skill removal rejected", "name", name, "error", err)
	}
}

// TryUnregister reports a removal denied by the configured guard.
func (r *Registry) TryUnregister(name string) error {
	return r.unregister(name, false, "")
}

// UnregisterTrusted removes a previously verified applied skill after its
// source file is deleted. Ordinary callers must use Unregister.
func (r *Registry) UnregisterTrusted(name string) error {
	return r.unregister(name, true, "")
}

// UnregisterIfOrigin atomically checks ownership before removing a file skill.
// The watcher passes trusted only for an applied file it has already verified.
func (r *Registry) UnregisterIfOrigin(name, origin string, trusted bool) error {
	if origin == "" {
		return fmt.Errorf("origem da skill é obrigatória")
	}
	return r.unregister(name, trusted, origin)
}

func (r *Registry) unregister(name string, trusted bool, expectedOrigin string) error {
	r.mu.Lock()
	if expectedOrigin != "" {
		current, ok := r.skills[name]
		if !ok || current.Origin != expectedOrigin {
			r.mu.Unlock()
			return fmt.Errorf("skill %q mudou de origem", name)
		}
	}
	if !trusted && r.registrationGuard != nil {
		if err := r.registrationGuard(name); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	delete(r.skills, name)
	onChange := r.onChange
	r.mu.Unlock()
	if onChange != nil {
		onChange(name, false)
	}
	return nil
}

// SetOnChange observes later skill registrations and removals. The callback
// runs without the registry lock, so it may safely read the registry to sync
// tool adapters. Register initial skills before installing the callback.
func (r *Registry) SetOnChange(fn func(name string, exists bool)) {
	r.mu.Lock()
	r.onChange = fn
	r.mu.Unlock()
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

	// Bundled prompt-template skills (code review, test generation,
	// documentation summarization, dependency audit). Each constructor
	// returns a *Skill that wraps a structured prompt template.
	r.Register(newCodeReviewSkill())
	r.Register(newTestGenSkill())
	r.Register(NewDocSummarySkill())
	r.Register(newDepAuditSkill())
}

func truncate(s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	return s[:MaxOutputBytes] + "\n... [saida truncada em 10KB]"
}
