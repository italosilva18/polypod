// Package commands — batch13_plugins.go
//
// Slash commands that expose plugin- and bridge-related lifecycle hooks
// as placeholders, while the real wiring is still pending:
//
//   - /plugin [list|info|enable|disable] [name]
//   - /reload-plugins
//   - /bridge
//   - /bridge-kick
//
// The only subcommand that performs real work today is `/plugin list`,
// which scans the on-disk plugin directory (~/.polypod/plugins) for
// subdirectories and reports their names. Every other entry point
// returns a Portuguese "pending wiring" notice so call sites can be
// exercised end-to-end.
//
// Layout resolution:
//   - If $XDG_CONFIG_HOME is set, plugins live at
//     $XDG_CONFIG_HOME/polypod/plugins.
//   - Otherwise the legacy ~/.polypod/plugins path is used.
//
// This mirrors how other batches honor XDG overrides for sandboxed
// testing (see batch11_theme.go).
//
// Wiring: registered via RegisterBatch13Plugins(reg). Command names are
// intentionally scoped (b13*) so they cannot collide with the batch2
// SlashCommand type in the same package.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch13Plugins registers the plugin and bridge placeholder
// slash commands on the provided command registry.
func RegisterBatch13Plugins(reg *command.Registry) {
	reg.Register(b13CmdPlugin())
	reg.Register(b13CmdReloadPlugins())
	reg.Register(b13CmdBridge())
	reg.Register(b13CmdBridgeKick())
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// b13PluginsDir returns the directory where polypod plugins are expected
// to live. When $XDG_CONFIG_HOME is set it wins; otherwise the legacy
// ~/.polypod/plugins path is returned. An empty string is returned when
// the home directory cannot be resolved and no XDG override is present.
func b13PluginsDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "polypod", "plugins")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".polypod", "plugins")
}

// b13ListPluginDirs scans b13PluginsDir() for immediate subdirectories
// and returns their names in sorted order. A missing directory is not
// an error — it yields an empty slice.
func b13ListPluginDirs() ([]string, error) {
	dir := b13PluginsDir()
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func b13OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

func b13Err(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "erro: " + msg}
}

// b13PendingWiringMsg is returned by placeholder commands that do not
// yet have a real implementation.
const b13PendingWiringMsg = "pendente: wiring ainda nao disponivel"

// ---------------------------------------------------------------------------
// /plugin — list, info, enable, disable
// ---------------------------------------------------------------------------

func b13CmdPlugin() *command.Command {
	return &command.Command{
		Name:        "plugin",
		Description: "Gerenciar plugins (list|info|enable|disable)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b13Err("uso: /plugin [list|info|enable|disable] [nome]"), nil
			}
			sub := strings.ToLower(args[0])
			rest := args[1:]

			switch sub {
			case "list":
				names, err := b13ListPluginDirs()
				if err != nil {
					return b13Err(fmt.Sprintf("falha ao listar plugins: %v", err)), nil
				}
				if len(names) == 0 {
					return b13OK(fmt.Sprintf("Nenhum plugin encontrado em %s", b13PluginsDir())), nil
				}
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("Plugins em %s:\n", b13PluginsDir()))
				for _, n := range names {
					sb.WriteString("  - ")
					sb.WriteString(n)
					sb.WriteString("\n")
				}
				return b13OK(strings.TrimRight(sb.String(), "\n")), nil

			case "info":
				if len(rest) == 0 {
					return b13Err("uso: /plugin info [nome]"), nil
				}
				return b13OK(fmt.Sprintf("plugin info %q: %s", rest[0], b13PendingWiringMsg)), nil

			case "enable":
				if len(rest) == 0 {
					return b13Err("uso: /plugin enable [nome]"), nil
				}
				return b13OK(fmt.Sprintf("plugin enable %q: %s", rest[0], b13PendingWiringMsg)), nil

			case "disable":
				if len(rest) == 0 {
					return b13Err("uso: /plugin disable [nome]"), nil
				}
				return b13OK(fmt.Sprintf("plugin disable %q: %s", rest[0], b13PendingWiringMsg)), nil

			default:
				return b13Err(fmt.Sprintf("subcomando invalido %q. Opcoes: list, info, enable, disable", args[0])), nil
			}
		},
	}
}

// ---------------------------------------------------------------------------
// /reload-plugins
// ---------------------------------------------------------------------------

func b13CmdReloadPlugins() *command.Command {
	return &command.Command{
		Name:        "reload-plugins",
		Description: "Recarregar plugins (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b13OK("plugin reload: " + b13PendingWiringMsg), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /bridge
// ---------------------------------------------------------------------------

func b13CmdBridge() *command.Command {
	return &command.Command{
		Name:        "bridge",
		Description: "Mostrar status da ponte (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b13OK("bridge status: " + b13PendingWiringMsg), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /bridge-kick
// ---------------------------------------------------------------------------

func b13CmdBridgeKick() *command.Command {
	return &command.Command{
		Name:        "bridge-kick",
		Description: "Desconectar ponte (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b13OK("bridge disconnect: " + b13PendingWiringMsg), nil
		},
	}
}
