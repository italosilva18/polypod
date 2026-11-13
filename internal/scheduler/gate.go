package scheduler

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const defaultGateTimeout = 15 * time.Second

// GateCondition controls when a scheduled task's pre-flight GateCommand allows
// the round to proceed (waking the LLM or main command) vs skipping at 0 token cost.
const (
	GateOnExitZero = "exit_zero" // proceed when exit code == 0 (default)
	GateOnNonEmpty = "non_empty" // proceed when exit code == 0 AND trimmed stdout != ""
)

// EvaluateGate executes task.GateCommand as a lightweight pre-flight check.
// When task.GateCommand is empty, it returns (true, "", nil).
func EvaluateGate(parent context.Context, task *Task) (passed bool, output string, err error) {
	if task == nil || strings.TrimSpace(task.GateCommand) == "" {
		return true, "", nil
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, defaultGateTimeout)
	defer cancel()

	buf := &routineCommandOutput{cancel: cancel}
	cmd := exec.CommandContext(ctx, "sh", "-c", task.GateCommand)
	cmd.Stdout = buf
	cmd.Stderr = buf
	runErr := cmd.Run()
	out, _ := buf.snapshot()
	trimmed := strings.TrimSpace(out)

	if ctx.Err() == context.DeadlineExceeded {
		return false, trimmed, ctx.Err()
	}

	mode := strings.ToLower(strings.TrimSpace(task.GateOn))
	switch mode {
	case GateOnNonEmpty:
		return runErr == nil && trimmed != "", trimmed, nil
	default:
		return runErr == nil, trimmed, nil
	}
}
