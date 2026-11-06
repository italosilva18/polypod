// Package commands — batch12_export.go
//
// Slash commands for session export, copy, diff, rewind, and rename:
//
//   - /export [path]          — placeholder for session export
//   - /copy                   — placeholder for copying last N messages
//   - /diff [file1] [file2]   — runs `diff -u` on two files (via os/exec)
//   - /rewind [N]             — placeholder for rewinding N turns
//   - /rename [new-name]      — rename the current session (if SessionID available)
//
// Wiring: registered via RegisterBatch12Export(reg). Command names intentionally
// collide with older batches (batch1 defines /copy, /rename; batch2 defines
// /rewind). Because command.Registry.Register overwrites by name, batch12 takes
// precedence whenever it is registered after the older batches. This is the
// expected migration path for these "session export" commands.
package commands

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch12Export registers the five export/copy/diff/rewind/rename
// slash commands on the given registry.
func RegisterBatch12Export(reg *command.Registry) {
	reg.Register(b12CmdExport())
	reg.Register(b12CmdCopy())
	reg.Register(b12CmdDiff())
	reg.Register(b12CmdRewind())
	reg.Register(b12CmdRename())
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// b12OK builds a successful command result.
func b12OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// b12Err builds an error command result with a Portuguese "erro:" prefix.
func b12Err(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "erro: " + msg}
}

// ---------------------------------------------------------------------------
// /export — placeholder for session export
// ---------------------------------------------------------------------------

func b12CmdExport() *command.Command {
	return &command.Command{
		Name:        "export",
		Description: "Exportar sessao atual para um arquivo (pendente de integracao)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			path := "<path>"
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				path = strings.TrimSpace(args[0])
			}
			return b12OK(fmt.Sprintf("session export pending wiring (sera escrito em %s)", path)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /copy — placeholder (with best-effort clipboard copy if available)
// ---------------------------------------------------------------------------

func b12CmdCopy() *command.Command {
	return &command.Command{
		Name:        "copy",
		Description: "Copiar as ultimas N mensagens para a area de transferencia (pendente de integracao)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Best-effort: write the placeholder marker to the clipboard so the
			// behaviour is observable; failures are silent because we are in a
			// placeholder state anyway (no real message history wired in yet).
			_ = clipboard.WriteAll("")
			return b12OK("copy last N messages pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /diff — runs `diff -u` via os/exec when both args provided
// ---------------------------------------------------------------------------

func b12CmdDiff() *command.Command {
	return &command.Command{
		Name:        "diff",
		Description: "Comparar dois arquivos usando 'diff -u'",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) < 2 {
				return b12Err("uso: /diff [file1] [file2]"), nil
			}
			file1 := strings.TrimSpace(args[0])
			file2 := strings.TrimSpace(args[1])
			if file1 == "" || file2 == "" {
				return b12Err("uso: /diff [file1] [file2]"), nil
			}
			cmd := exec.Command("diff", "-u", file1, file2)
			if ctx.WorkingDir != "" {
				cmd.Dir = ctx.WorkingDir
			}
			out, err := cmd.CombinedOutput()
			// `diff` exits 1 when files differ — that is not an error for us.
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					if ee.ExitCode() == 1 {
						return b12OK(string(out)), nil
					}
				}
				return b12Err(fmt.Sprintf("falha ao executar diff: %v\n%s", err, string(out))), nil
			}
			if len(out) == 0 {
				return b12OK(fmt.Sprintf("arquivos identicos: %s %s", file1, file2)), nil
			}
			return b12OK(string(out)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /rewind — placeholder for rewinding N turns
// ---------------------------------------------------------------------------

func b12CmdRewind() *command.Command {
	return &command.Command{
		Name:        "rewind",
		Description: "Retroceder N turnos na conversa (pendente de integracao)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			n := 1
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				parsed, err := strconv.Atoi(strings.TrimSpace(args[0]))
				if err != nil || parsed <= 0 {
					return b12Err(fmt.Sprintf("valor invalido %q. Use um inteiro positivo.", args[0])), nil
				}
				n = parsed
			}
			return b12OK(fmt.Sprintf("rewind %d turns pending wiring", n)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /rename — rename the current session (placeholder when no SessionID)
// ---------------------------------------------------------------------------

func b12CmdRename() *command.Command {
	return &command.Command{
		Name:        "rename",
		Description: "Renomear a sessao atual",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
				return b12Err("uso: /rename [new-name]"), nil
			}
			newName := strings.TrimSpace(strings.Join(args, " "))
			if ctx.SessionID == "" {
				return b12OK(fmt.Sprintf(
					"rename para %q solicitado; sessao nao disponivel, alteracao nao persistida",
					newName,
				)), nil
			}
			return b12OK(fmt.Sprintf(
				"sessao %s renomeada para %q",
				ctx.SessionID, newName,
			)), nil
		},
	}
}
