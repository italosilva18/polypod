package tasks

import (
	"bufio"
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
	state, ok := registry.tasks[taskID]
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
		state.Status = StatusKilled
		state.EndTime = &now
		state.Notified = true
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
	state.Status = StatusKilled
	state.EndTime = &now
	state.Notified = true

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
	// Create task state
	state := NewTaskState(TaskTypeShell, description)
	state.ToolUseID = toolUseID
	state.Status = StatusRunning

	// Create output file
	outputPath := fmt.Sprintf("/tmp/polypod_shell_%s.log", state.ID)
	state.OutputFile = outputPath

	outFile, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("creating output file: %w", err)
	}

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

	// Register in task registry
	registry.Register(state)

	taskID := state.ID

	// Stream output to file in background
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			line := scanner.Text() + "\n"
			shellState.mu.Lock()
			if shellState.outputFile != nil {
				_, _ = shellState.outputFile.WriteString(line)
			}
			shellState.mu.Unlock()
		}
	}()

	// Wait for completion in background
	go func() {
		err := cmd.Wait()

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
				return // already killed
			}
			t.Status = status
			t.EndTime = &now
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
