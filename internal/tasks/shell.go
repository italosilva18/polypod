package tasks

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Shell task migrated from Claude Code TypeScript source.
// Source: tasks/LocalShellTask/LocalShellTask.tsx, tasks/LocalShellTask/guards.ts

// ---------------------------------------------------------------------------
// LocalShellTask — run shell command as background task
// ---------------------------------------------------------------------------

// ShellTaskState extends TaskState with shell-specific fields.
type ShellTaskState struct {
	Command        string `json:"command"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	Interrupted    bool   `json:"interrupted"`
	IsBackgrounded bool   `json:"is_backgrounded"`
	// Internal fields (not serialized)
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	outputFile *os.File
	mu         sync.Mutex
	logger     *slog.Logger
}

// LocalShellTaskKiller implements TaskKiller for shell tasks.
type LocalShellTaskKiller struct {
	logger *slog.Logger
}

// NewLocalShellTaskKiller creates a new shell task killer.
func NewLocalShellTaskKiller(logger *slog.Logger) *LocalShellTaskKiller {
	return &LocalShellTaskKiller{logger: logger}
}

func (k *LocalShellTaskKiller) Name() string   { return "LocalShellTask" }
func (k *LocalShellTaskKiller) Type() TaskType { return TaskTypeShell }

// Kill stops a shell task by sending SIGTERM to the process group, then
// SIGKILL after a grace period.
func (k *LocalShellTaskKiller) Kill(taskID string, registry *TaskRegistry) error {
	registry.mu.Lock()
	_, ok := registry.tasks[taskID]
	if !ok {
		registry.mu.Unlock()
		return fmt.Errorf("shell task %s not found", taskID)
	}
	registry.mu.Unlock()

	// Retrieve the shell-specific state from the context map
	shellState := getShellState(taskID)
	if shellState == nil {
		// No process to kill — just mark terminal
		now := time.Now()
		registry.Update(taskID, func(t *TaskState) {
			t.Status = StatusKilled
			t.EndTime = &now
			t.Notified = true
			t.Error = "Execucao cancelada pelo usuario."
		})
		return nil
	}

	shellState.mu.Lock()
	defer shellState.mu.Unlock()

	if shellState.cancel != nil {
		shellState.cancel()
	}

	// Try graceful SIGTERM first
	if shellState.cmd != nil && shellState.cmd.Process != nil {
		_ = syscall.Kill(-shellState.cmd.Process.Pid, syscall.SIGTERM)

		// Give 5s grace then SIGKILL
		go func() {
			time.Sleep(5 * time.Second)
			if shellState.cmd != nil && shellState.cmd.Process != nil {
				_ = syscall.Kill(-shellState.cmd.Process.Pid, syscall.SIGKILL)
			}
		}()
	}

	now := time.Now()
	registry.Update(taskID, func(t *TaskState) {
		t.Status = StatusKilled
		t.EndTime = &now
		t.Notified = true
		t.Error = "Execucao cancelada pelo usuario."
	})

	k.logger.Info("shell task killed", "task_id", taskID)
	return nil
}

// ---------------------------------------------------------------------------
// Shell state tracking (process-local registry)
// ---------------------------------------------------------------------------

var (
	shellStates   = make(map[string]*ShellTaskState)
	shellStatesMu sync.Mutex
)

func setShellState(taskID string, state *ShellTaskState) {
	shellStatesMu.Lock()
	defer shellStatesMu.Unlock()
	shellStates[taskID] = state
}

func getShellState(taskID string) *ShellTaskState {
	shellStatesMu.Lock()
	defer shellStatesMu.Unlock()
	return shellStates[taskID]
}

func removeShellState(taskID string) {
	shellStatesMu.Lock()
	defer shellStatesMu.Unlock()
	delete(shellStates, taskID)
}

// ---------------------------------------------------------------------------
// SpawnShellTask — start a shell command as a background task
// ---------------------------------------------------------------------------

// SpawnShellTask starts a shell command as a background task and returns a handle.
func SpawnShellTask(
	registry *TaskRegistry,
	command string,
	description string,
	toolUseID string,
	logger *slog.Logger,
) (*TaskHandle, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// Create task state
	state := NewTaskState(TaskTypeShell, description)
	state.ToolUseID = toolUseID
	state.Status = StatusRunning
	state.input = command

	// Create output file
	outFile, err := registry.CreateOutputFile(TaskTypeShell, state.ID)
	if err != nil {
		return nil, fmt.Errorf("creating output file: %w", err)
	}
	state.OutputFile = outFile.Name()

	// Create command with process group for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		outFile.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout pipe

	shellState := &ShellTaskState{
		Command:        command,
		IsBackgrounded: true,
		cmd:            cmd,
		cancel:         cancel,
		outputFile:     outFile,
		logger:         logger,
	}
	setShellState(state.ID, shellState)

	// Start the command
	if err := cmd.Start(); err != nil {
		cancel()
		outFile.Close()
		removeShellState(state.ID)
		return nil, fmt.Errorf("starting command: %w", err)
	}
	state.ProcessID = cmd.Process.Pid

	// Register in task registry
	registry.Register(state)

	taskID := state.ID

	// Drain output fully before waiting, so the process cannot block on a full
	// pipe and its final bytes are present before terminal status is saved.
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(outFile, stdout)
		copyDone <- copyErr
	}()

	// Wait for completion in background
	go func() {
		copyErr := <-copyDone
		err := cmd.Wait()
		if copyErr != nil && err == nil {
			err = copyErr
		}

		shellState.mu.Lock()
		if shellState.outputFile != nil {
			_ = shellState.outputFile.Close()
			shellState.outputFile = nil
		}
		shellState.mu.Unlock()

		now := time.Now()

		exitCode := 0
		interrupted := false
		status := StatusCompleted

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
				if exitCode == -1 {
					interrupted = true
				}
			}
			if ctx.Err() != nil {
				status = StatusKilled
				interrupted = true
			} else {
				status = StatusFailed
			}
		}

		shellState.ExitCode = &exitCode
		shellState.Interrupted = interrupted

		registry.Update(taskID, func(t *TaskState) {
			if t.Status.IsTerminal() {
				t.ExitCode = &exitCode
				return // already killed; retain the confirmed process exit code
			}
			t.Status = status
			t.EndTime = &now
			t.ExitCode = &exitCode
			if err != nil {
				t.Error = err.Error()
			}
		})

		logger.Info("shell task completed",
			"task_id", taskID,
			"exit_code", exitCode,
			"status", status,
			"duration", time.Since(state.StartTime),
		)

		// Cleanup after a delay
		go func() {
			time.Sleep(5 * time.Minute)
			removeShellState(taskID)
		}()
	}()

	return &TaskHandle{
		TaskID: taskID,
		Cleanup: func() {
			cancel()
			removeShellState(taskID)
		},
	}, nil
}

// GetShellOutput reads the current output of a shell task.
func GetShellOutput(registry *TaskRegistry, taskID string, maxBytes int) (string, error) {
	state, ok := registry.Get(taskID)
	if !ok {
		return "", fmt.Errorf("task %s not found", taskID)
	}
	if state.Type != TaskTypeShell {
		return "", fmt.Errorf("task %s is not a shell task", taskID)
	}

	data, err := os.ReadFile(state.OutputFile)
	if err != nil {
		return "", fmt.Errorf("reading output: %w", err)
	}

	output := string(data)
	if maxBytes > 0 && len(output) > maxBytes {
		output = "... [truncated]\n" + output[len(output)-maxBytes:]
	}
	return output, nil
}

// StreamShellOutput opens the output file for live streaming.
func StreamShellOutput(registry *TaskRegistry, taskID string) (io.ReadCloser, error) {
	state, ok := registry.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	f, err := os.Open(state.OutputFile)
	if err != nil {
		return nil, fmt.Errorf("opening output file: %w", err)
	}
	return f, nil
}
