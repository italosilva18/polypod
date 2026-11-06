// Package commands — batch16_plan.go
//
// Batch 16 (planning / agent-inventory slash commands, wave 3 round 3).
//
// This batch introduces the new /plan placeholder command and ships
// batch-16 variants of /agents and /keybindings. Because batch1 already
// owns the classic /agents and /keybindings slash names, we intentionally
// do NOT re-register those two under batch16 — re-registering them would
// silently overwrite the batch1 handlers on the command.Registry and file
// collisions are considered catastrophic for the migration.
//
// The B16 helper functions (b16CmdAgents, b16CmdKeybindings) are still
// defined here so tests and future wiring agents can exercise the new
// behaviour (agent-package inventory + ~/.claude/keybindings.json probing)
// without disturbing the batch1 registrations.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch16Plan registers the batch-16 planning command onto the
// given registry.
//
// NOTE: /agents and /keybindings already live in batch1, so we deliberately
// do NOT re-register them here. This helper only registers the genuinely
// new /plan placeholder. The b16CmdAgents / b16CmdKeybindings helpers are
// still defined in this file (and exercised by the tests) so that future
// wiring agents can migrate them once batch1 is retired.
func RegisterBatch16Plan(reg *command.Registry) {
	reg.Register(b16CmdPlan())
}

// ---------------------------------------------------------------------------
// /plan — placeholder while orchestrator wiring lands
// ---------------------------------------------------------------------------

// b16PlanMsg is exported as a package-level variable so tests can assert on
// the user-visible placeholder text without duplicating the literal.
const b16PlanMsg = "entrando em modo de plano (pendente wiring do orchestrator)"

// b16CmdPlan returns the placeholder /plan command. The real implementation
// will delegate to internal/orchestrator once that package lands; for now
// it simply prints a stable Portuguese placeholder so the UI and docs can
// already reference /plan.
func b16CmdPlan() *command.Command {
	return &command.Command{
		Name:        "plan",
		Description: "Entrar em modo de plano (placeholder — wiring do orchestrator pendente)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(b16PlanMsg), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /agents (batch-16 variant, NOT registered — see RegisterBatch16Plan)
// ---------------------------------------------------------------------------

// b16StaticAgents is the fallback list used when no live agent.Registry is
// supplied. Kept as a package-level var so tests can reference it.
var b16StaticAgents = []string{
	"default",
	"verification-persona",
	"explore-persona",
	"plan-persona",
}

// b16ListAgents returns the sorted list of agent names to display. When
// reg is non-nil it uses the live registry; otherwise it falls back to the
// static list above. The result is always a fresh slice (callers can
// mutate it freely).
func b16ListAgents(reg *agent.Registry) []string {
	if reg != nil && reg.Count() > 0 {
		names := reg.List()
		sort.Strings(names)
		out := make([]string, len(names))
		copy(out, names)
		return out
	}
	out := make([]string, len(b16StaticAgents))
	copy(out, b16StaticAgents)
	sort.Strings(out)
	return out
}

// b16FormatAgents renders the agent list as a Portuguese user-facing block.
func b16FormatAgents(names []string) string {
	if len(names) == 0 {
		return "Nenhum agente disponível."
	}
	var sb strings.Builder
	sb.WriteString("Agentes disponíveis:\n")
	for _, n := range names {
		sb.WriteString("  - ")
		sb.WriteString(n)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// b16CmdAgents returns the batch-16 variant of /agents. It is intentionally
// NOT wired into RegisterBatch16Plan (batch1 already owns the slash name);
// it exists so tests and future wiring agents can exercise the new
// agent.Registry-aware behaviour.
func b16CmdAgents() *command.Command {
	return &command.Command{
		Name:        "agents",
		Description: "Listar agentes disponíveis (batch16 — não registrado para evitar colisão com batch1)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// No live registry is plumbed through CommandContext today,
			// so we fall back to the static list. Once wiring lands we
			// can swap this for the real agent.Registry.
			names := b16ListAgents(nil)
			return okResult(b16FormatAgents(names)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /keybindings (batch-16 variant, NOT registered — see RegisterBatch16Plan)
// ---------------------------------------------------------------------------

// b16NoKeybindingsMsg is the Portuguese fallback shown when no custom
// keybindings file is present on disk.
const b16NoKeybindingsMsg = "nenhuma keybinding customizada"

// b16KeybindingsPath returns the absolute path batch16 probes for custom
// keybindings. It honours $HOME so tests can redirect via t.Setenv.
func b16KeybindingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".claude", "keybindings.json")
}

// b16ReadKeybindings reads ~/.claude/keybindings.json if present. It
// returns (contents, true, nil) when the file exists and is readable,
// (nil, false, nil) when the file is simply absent, and (nil, false, err)
// for any other I/O failure.
func b16ReadKeybindings() ([]byte, bool, error) {
	path := b16KeybindingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

// b16CmdKeybindings returns the batch-16 variant of /keybindings. It is
// intentionally NOT wired into RegisterBatch16Plan; see the package
// comment for the rationale.
func b16CmdKeybindings() *command.Command {
	return &command.Command{
		Name:        "keybindings",
		Description: "Exibir keybindings customizadas (batch16 — não registrado para evitar colisão com batch1)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			data, ok, err := b16ReadKeybindings()
			if err != nil {
				return errResult(fmt.Sprintf("falha ao ler keybindings: %v", err)), nil
			}
			if !ok {
				return okResult(b16NoKeybindingsMsg), nil
			}
			trimmed := strings.TrimRight(string(data), "\n")
			if trimmed == "" {
				return okResult(b16NoKeybindingsMsg), nil
			}
			return okResult("Keybindings customizadas (" + b16KeybindingsPath() + "):\n" + trimmed), nil
		},
	}
}
