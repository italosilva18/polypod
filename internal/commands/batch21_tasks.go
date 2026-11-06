// Package commands — batch21_tasks.go
//
// Task-management slash commands for rodada 3 wave 1 batch 21.
//
// Territory asked for /task-status, /task-logs, /task-restart and /schedule.
// A grep across internal/commands/ confirmed there are no collisions with
// those exact names — batch2.go owns /tasks (plural, via the legacy
// SlashCommand type in a different registry), which does not collide with
// these new hyphenated names registered on *command.Registry. So we keep the
// /task-* names as requested.
//
// The internal/tasks package does NOT currently expose a DefaultRegistry() or
// any global singleton; internal/tasks.TaskRegistry is always constructed by
// callers via NewTaskRegistry(). Because this batch does not own the wiring
// that would plumb the live registry into the slash-command layer, the four
// commands are placeholders that return the agreed "pending wiring" strings
// described in the territory brief. A later wiring agent can replace the
// Execute bodies with calls against a real *tasks.TaskRegistry passed in via
// command.CommandContext (or a package-level setter) without having to touch
// the command definitions themselves.
//
// Commands registered by RegisterBatch21Tasks:
//
//   - /task-status  [id]  — "task status pendente wiring"
//   - /task-logs    [id]  — "task logs pendente wiring"
//   - /task-restart [id]  — "task restart pendente wiring"
//   - /schedule     [cron] — "scheduled task pendente wiring"
//
// Identifiers are suffixed with "B21" to keep them isolated from the rest of
// the package.
package commands

import (
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Placeholder display strings
// ---------------------------------------------------------------------------
//
// These are kept as package-level constants so the tests can reference them
// without duplicating the literal strings. When the wiring agent lands, the
// constants themselves can be removed (or repurposed as fallback messages).

const (
	taskStatusPlaceholderB21   = "task status pendente wiring"
	taskLogsPlaceholderB21     = "task logs pendente wiring"
	taskRestartPlaceholderB21  = "task restart pendente wiring"
	scheduleTaskPlaceholderB21 = "scheduled task pendente wiring"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch21Tasks registers the batch21 task-management commands on the
// supplied registry. All four commands are CommandLocal / SourceBuiltin and
// return a fixed placeholder message until the real wiring against
// internal/tasks.TaskRegistry lands.
func RegisterBatch21Tasks(reg *command.Registry) {
	reg.Register(cmdTaskStatusB21())
	reg.Register(cmdTaskLogsB21())
	reg.Register(cmdTaskRestartB21())
	reg.Register(cmdScheduleB21())
}

// ---------------------------------------------------------------------------
// /task-status [id] — placeholder
// ---------------------------------------------------------------------------

func cmdTaskStatusB21() *command.Command {
	return &command.Command{
		Name:        "task-status",
		Description: "Mostrar status de uma tarefa em background (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(taskStatusPlaceholderB21), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /task-logs [id] — placeholder
// ---------------------------------------------------------------------------

func cmdTaskLogsB21() *command.Command {
	return &command.Command{
		Name:        "task-logs",
		Description: "Mostrar logs de uma tarefa em background (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(taskLogsPlaceholderB21), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /task-restart [id] — placeholder
// ---------------------------------------------------------------------------

func cmdTaskRestartB21() *command.Command {
	return &command.Command{
		Name:        "task-restart",
		Description: "Reiniciar uma tarefa em background (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(taskRestartPlaceholderB21), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /schedule [cron] — placeholder
// ---------------------------------------------------------------------------

func cmdScheduleB21() *command.Command {
	return &command.Command{
		Name:        "schedule",
		Description: "Agendar uma tarefa recorrente via cron (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(scheduleTaskPlaceholderB21), nil
		},
	}
}
