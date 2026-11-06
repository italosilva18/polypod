// Package commands — batch21_tasks_test.go
//
// Smoke tests for the batch21 task-management commands. The commands are
// placeholders pending the internal/tasks wiring, so the assertions pin down:
//
//   - all four names are registered on the registry
//   - each command is CommandLocal / SourceBuiltin with a non-nil Execute
//   - each Execute returns the agreed placeholder string (with or without args)
//   - registration does NOT clobber the unrelated /tasks (plural) command
//     from batch2 (collision sanity check)
package commands

import (
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch21Ctx returns a CommandContext suitable for batch21 tests.
func newBatch21Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch21Tasks_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch21Tasks(reg)

	want := []string{"task-status", "task-logs", "task-restart", "schedule"}
	for _, name := range want {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q not registered", name)
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute handler", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
		if cmd.Description == "" {
			t.Errorf("command %q: empty description", name)
		}
	}
}

// TestRegisterBatch21Tasks_DoesNotClobberTasksPlural confirms that registering
// batch21 on a fresh registry leaves the /tasks (plural) name alone — batch21
// owns /task-status, /task-logs, /task-restart and /schedule, not /tasks.
func TestRegisterBatch21Tasks_DoesNotClobberTasksPlural(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch21Tasks(reg)

	if _, ok := reg.Get("tasks"); ok {
		t.Errorf("batch21 should NOT register %q (lives in batch2.go as SlashCommand)", "tasks")
	}
}

// ---------------------------------------------------------------------------
// Placeholder outputs
// ---------------------------------------------------------------------------

func TestCmdTaskStatusB21_Placeholder(t *testing.T) {
	res, err := cmdTaskStatusB21().Execute(nil, newBatch21Ctx("batch21-task-status-session"))
	if err != nil {
		t.Fatalf("cmdTaskStatusB21 error: %v", err)
	}
	if res == nil || res.Display != taskStatusPlaceholderB21 {
		t.Errorf("unexpected /task-status placeholder: got %q, want %q",
			displayOrNil(res), taskStatusPlaceholderB21)
	}
}

func TestCmdTaskLogsB21_Placeholder(t *testing.T) {
	res, err := cmdTaskLogsB21().Execute(
		[]string{"b1234abcd"},
		newBatch21Ctx("batch21-task-logs-session"),
	)
	if err != nil {
		t.Fatalf("cmdTaskLogsB21 error: %v", err)
	}
	if res == nil || res.Display != taskLogsPlaceholderB21 {
		t.Errorf("unexpected /task-logs placeholder: got %q, want %q",
			displayOrNil(res), taskLogsPlaceholderB21)
	}
}

func TestCmdTaskRestartB21_Placeholder(t *testing.T) {
	res, err := cmdTaskRestartB21().Execute(
		[]string{"a5678efgh"},
		newBatch21Ctx("batch21-task-restart-session"),
	)
	if err != nil {
		t.Fatalf("cmdTaskRestartB21 error: %v", err)
	}
	if res == nil || res.Display != taskRestartPlaceholderB21 {
		t.Errorf("unexpected /task-restart placeholder: got %q, want %q",
			displayOrNil(res), taskRestartPlaceholderB21)
	}
}

func TestCmdScheduleB21_Placeholder(t *testing.T) {
	res, err := cmdScheduleB21().Execute(
		[]string{"*/5", "*", "*", "*", "*"},
		newBatch21Ctx("batch21-schedule-session"),
	)
	if err != nil {
		t.Fatalf("cmdScheduleB21 error: %v", err)
	}
	if res == nil || res.Display != scheduleTaskPlaceholderB21 {
		t.Errorf("unexpected /schedule placeholder: got %q, want %q",
			displayOrNil(res), scheduleTaskPlaceholderB21)
	}
}

// displayOrNil safely extracts res.Display for test error messages.
func displayOrNil(res *command.CommandResult) string {
	if res == nil {
		return "<nil>"
	}
	return res.Display
}
