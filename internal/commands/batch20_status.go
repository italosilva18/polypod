// Package commands — batch20_status.go
// Batch 20: status / uptime / reload / version commands.
// Registered via RegisterBatch20Status. Function names use the B20 suffix
// to avoid collisions with batch1/batch2 helpers.
package commands

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/costa/polypod/internal/command"
)

// startTimeB20 records the process start time used for uptime reporting.
var startTimeB20 = time.Now()

// RegisterBatch20Status registers the batch 20 status-related slash commands.
func RegisterBatch20Status(reg *command.Registry) {
	reg.Register(cmdStatusB20())
	reg.Register(cmdUptimeB20())
	reg.Register(cmdReloadB20())
	reg.Register(cmdVersionB20())
}

// ---------------------------------------------------------------------------
// /status — prints session id, working dir, Go version and uptime.
// ---------------------------------------------------------------------------

func cmdStatusB20() *command.Command {
	return &command.Command{
		Name:        "status",
		Description: "Mostra o estado atual da sessão (id, diretório, Go, uptime)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			wd := ctx.WorkingDir
			if wd == "" {
				if cwd, err := os.Getwd(); err == nil {
					wd = cwd
				}
			}
			uptime := time.Since(startTimeB20).Round(time.Second)
			msg := fmt.Sprintf(
				"sessão: %s\ndiretório: %s\nGo: %s\nuptime: %s",
				ctx.SessionID, wd, runtime.Version(), uptime,
			)
			return &command.CommandResult{Display: msg}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// /uptime — prints only the uptime since package init.
// ---------------------------------------------------------------------------

func cmdUptimeB20() *command.Command {
	return &command.Command{
		Name:        "uptime",
		Description: "Mostra há quanto tempo o processo está ativo",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			uptime := time.Since(startTimeB20).Round(time.Second)
			return &command.CommandResult{
				Display: fmt.Sprintf("uptime: %s", uptime),
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// /reload — placeholder until config reload wiring lands.
// ---------------------------------------------------------------------------

func cmdReloadB20() *command.Command {
	return &command.Command{
		Name:        "reload",
		Description: "Recarrega a configuração (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return &command.CommandResult{
				Display: "recarga de config pendente wiring",
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// /version — prints the Go runtime version and the polypod build tag.
// ---------------------------------------------------------------------------

func cmdVersionB20() *command.Command {
	return &command.Command{
		Name:        "version",
		Description: "Mostra a versão do polypod e do runtime Go",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return &command.CommandResult{
				Display: fmt.Sprintf("%s polypod dev", runtime.Version()),
			}, nil
		},
	}
}
