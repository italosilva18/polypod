// Package commands — batch11_theme.go
//
// Slash commands for session-level appearance and reasoning preferences:
//
//   - /effort        — set reasoning effort (low|med|high)
//   - /output-style  — set output style (compact|verbose|minimal)
//   - /color         — set color preference (auto|on|off)
//   - /theme         — set theme preference (dark|light|auto)
//   - /good-claude   — toggle "safety focused" flag
//   - /passes        — set max reasoning passes (N)
//
// All settings are persisted to a per-session JSON file located at
// $XDG_STATE_HOME/polypod/sessions/<session-id>.json. If no session id
// is available via CommandContext, the command still echoes the parsed
// value but emits a Portuguese notice that the change was not persisted.
//
// Wiring: registered via RegisterBatch11Theme(reg). The command names
// declared here intentionally match earlier batches (batch1 also defines
// /effort, /color, /output-style, /passes). Because command.Registry's
// Register method overwrites by name, batch11 takes precedence whenever
// it is registered after the older batches — that is the expected
// migration path for these "theme/preference" commands.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch11Theme registers the six theme/preference slash commands.
func RegisterBatch11Theme(reg *command.Registry) {
	reg.Register(b11CmdEffort())
	reg.Register(b11CmdOutputStyle())
	reg.Register(b11CmdColor())
	reg.Register(b11CmdTheme())
	reg.Register(b11CmdGoodClaude())
	reg.Register(b11CmdPasses())
}

// ---------------------------------------------------------------------------
// Persistence helpers (XDG_STATE_HOME/polypod/sessions/<id>.json)
//
// These are intentionally kept private to batch11 to avoid colliding with
// batch1's readSessionJSON/writeSessionJSON helpers, which target a
// different directory layout (~/.polypod/sessions/).
// ---------------------------------------------------------------------------

// b11SessionsDir returns the directory where per-session JSON blobs live.
//
//	Linux:   $XDG_STATE_HOME/polypod/sessions   (default ~/.local/state/polypod/sessions)
//	macOS:   ~/Library/Application Support/polypod/state/sessions
//	Windows: %LOCALAPPDATA%\polypod\state\sessions
func b11SessionsDir() string {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "polypod", "state", "sessions")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "polypod", "state", "sessions")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Local", "polypod", "state", "sessions")
	default:
		if d := os.Getenv("XDG_STATE_HOME"); d != "" {
			return filepath.Join(d, "polypod", "sessions")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state", "polypod", "sessions")
	}
}

// b11SessionPath returns the JSON file for a given session id.
func b11SessionPath(sessionID string) string {
	return filepath.Join(b11SessionsDir(), sessionID+".json")
}

// b11ReadSession loads the per-session JSON blob, returning an empty map
// (and nil error) when the file does not yet exist.
func b11ReadSession(sessionID string) (map[string]any, error) {
	data, err := os.ReadFile(b11SessionPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	m := map[string]any{}
	if len(data) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// b11WriteSession persists the per-session JSON blob.
func b11WriteSession(sessionID string, m map[string]any) error {
	dir := filepath.Dir(b11SessionPath(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b11SessionPath(sessionID), data, 0o600)
}

// b11SetSessionField is a small helper that loads, mutates, and saves a
// single field. When sessionID is empty it returns ("", false, nil) so the
// caller can emit the standard "not persisted" notice.
func b11SetSessionField(sessionID, key string, value any) (path string, persisted bool, err error) {
	if sessionID == "" {
		return "", false, nil
	}
	m, rerr := b11ReadSession(sessionID)
	if rerr != nil {
		return "", false, rerr
	}
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	if werr := b11WriteSession(sessionID, m); werr != nil {
		return "", false, werr
	}
	return b11SessionPath(sessionID), true, nil
}

// b11NotPersistedMsg is the canonical Portuguese message returned when no
// session id is available. The exact wording matches the task spec.
const b11NotPersistedMsg = "session id not available, setting not persisted"

// b11OK builds a successful command result.
func b11OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// b11Err builds an error command result.
func b11Err(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "erro: " + msg}
}

// ---------------------------------------------------------------------------
// /effort — set reasoning effort
// ---------------------------------------------------------------------------

func b11CmdEffort() *command.Command {
	valid := map[string]string{
		"low":    "low",
		"med":    "med",
		"medium": "med",
		"high":   "high",
	}
	return &command.Command{
		Name:        "effort",
		Description: "Definir nivel de esforco de raciocinio (low|med|high)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b11Err("uso: /effort [low|med|high]"), nil
			}
			level, ok := valid[strings.ToLower(args[0])]
			if !ok {
				return b11Err(fmt.Sprintf("nivel invalido %q. Opcoes: low, med, high", args[0])), nil
			}
			_, persisted, err := b11SetSessionField(ctx.SessionID, "effort", level)
			if err != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", err)), nil
			}
			if !persisted {
				return b11OK(fmt.Sprintf("Esforco solicitado: %s. %s", level, b11NotPersistedMsg)), nil
			}
			return b11OK(fmt.Sprintf("Esforco de raciocinio definido para: %s", level)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /output-style — set output style preference
// ---------------------------------------------------------------------------

func b11CmdOutputStyle() *command.Command {
	valid := map[string]bool{
		"compact": true, "verbose": true, "minimal": true,
	}
	return &command.Command{
		Name:        "output-style",
		Description: "Definir estilo de saida (compact|verbose|minimal)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b11Err("uso: /output-style [compact|verbose|minimal]"), nil
			}
			style := strings.ToLower(args[0])
			if !valid[style] {
				return b11Err(fmt.Sprintf("estilo invalido %q. Opcoes: compact, verbose, minimal", args[0])), nil
			}
			_, persisted, err := b11SetSessionField(ctx.SessionID, "output_style", style)
			if err != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", err)), nil
			}
			if !persisted {
				return b11OK(fmt.Sprintf("Estilo solicitado: %s. %s", style, b11NotPersistedMsg)), nil
			}
			return b11OK(fmt.Sprintf("Estilo de saida definido para: %s", style)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /color — set color preference
// ---------------------------------------------------------------------------

func b11CmdColor() *command.Command {
	valid := map[string]bool{
		"auto": true, "on": true, "off": true,
	}
	return &command.Command{
		Name:        "color",
		Description: "Definir preferencia de cor (auto|on|off)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b11Err("uso: /color [auto|on|off]"), nil
			}
			mode := strings.ToLower(args[0])
			if !valid[mode] {
				return b11Err(fmt.Sprintf("preferencia invalida %q. Opcoes: auto, on, off", args[0])), nil
			}
			_, persisted, err := b11SetSessionField(ctx.SessionID, "color", mode)
			if err != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", err)), nil
			}
			if !persisted {
				return b11OK(fmt.Sprintf("Cor solicitada: %s. %s", mode, b11NotPersistedMsg)), nil
			}
			return b11OK(fmt.Sprintf("Preferencia de cor definida para: %s", mode)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /theme — set theme preference
// ---------------------------------------------------------------------------

func b11CmdTheme() *command.Command {
	valid := map[string]bool{
		"dark": true, "light": true, "auto": true,
	}
	return &command.Command{
		Name:        "theme",
		Description: "Definir tema (dark|light|auto)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b11Err("uso: /theme [dark|light|auto]"), nil
			}
			theme := strings.ToLower(args[0])
			if !valid[theme] {
				return b11Err(fmt.Sprintf("tema invalido %q. Opcoes: dark, light, auto", args[0])), nil
			}
			_, persisted, err := b11SetSessionField(ctx.SessionID, "theme", theme)
			if err != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", err)), nil
			}
			if !persisted {
				return b11OK(fmt.Sprintf("Tema solicitado: %s. %s", theme, b11NotPersistedMsg)), nil
			}
			return b11OK(fmt.Sprintf("Tema definido para: %s", theme)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /good-claude — toggle "safety focused" flag
// ---------------------------------------------------------------------------

func b11CmdGoodClaude() *command.Command {
	return &command.Command{
		Name:        "good-claude",
		Description: "Alternar modo focado em seguranca",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if ctx.SessionID == "" {
				// Echo the toggled state we *would* set (defaulting to true)
				// and warn that nothing was persisted.
				return b11OK(fmt.Sprintf("Modo seguranca solicitado: ativado. %s", b11NotPersistedMsg)), nil
			}
			m, err := b11ReadSession(ctx.SessionID)
			if err != nil {
				return b11Err(fmt.Sprintf("falha ao ler sessao: %v", err)), nil
			}
			if m == nil {
				m = map[string]any{}
			}
			cur, _ := m["safety_focused"].(bool)
			next := !cur
			m["safety_focused"] = next
			if err := b11WriteSession(ctx.SessionID, m); err != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", err)), nil
			}
			state := "desativado"
			if next {
				state = "ativado"
			}
			return b11OK(fmt.Sprintf("Modo focado em seguranca: %s", state)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /passes — set max reasoning passes
// ---------------------------------------------------------------------------

func b11CmdPasses() *command.Command {
	return &command.Command{
		Name:        "passes",
		Description: "Definir numero maximo de passes de raciocinio",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b11Err("uso: /passes [N]"), nil
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[0]))
			if err != nil || n <= 0 {
				return b11Err(fmt.Sprintf("valor invalido %q. Use um inteiro positivo.", args[0])), nil
			}
			_, persisted, perr := b11SetSessionField(ctx.SessionID, "max_passes", n)
			if perr != nil {
				return b11Err(fmt.Sprintf("falha ao salvar: %v", perr)), nil
			}
			if !persisted {
				return b11OK(fmt.Sprintf("Passes solicitados: %d. %s", n, b11NotPersistedMsg)), nil
			}
			return b11OK(fmt.Sprintf("Passes maximos definidos para: %d", n)), nil
		},
	}
}
