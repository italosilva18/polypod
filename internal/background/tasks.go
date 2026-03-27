package background

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Task is a background execution.
type Task struct {
	ID         int
	Command    string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string // "running", "done", "failed"
	OutputFile string
	ExitCode   int
	cancel     context.CancelFunc
}

// Manager manages background tasks.
type Manager struct {
	mu     sync.Mutex
	tasks  map[int]*Task
	nextID int
	logger *slog.Logger
}

// NewManager creates a background task manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		tasks:  make(map[int]*Task),
		logger: logger,
	}
}

// Run starts a command in the background. Returns task ID.
func (m *Manager) Run(command string) (int, error) {
	m.mu.Lock()
	m.nextID++
	id := m.nextID

	// Limit concurrent tasks
	if len(m.tasks) >= 10 {
		m.mu.Unlock()
		return 0, fmt.Errorf("limite de 10 tarefas em background atingido")
	}

	outputFile := fmt.Sprintf("/tmp/polypod_bg_%d.log", id)
	ctx, cancel := context.WithCancel(context.Background())

	task := &Task{
		ID:         id,
		Command:    command,
		StartedAt:  time.Now(),
		Status:     "running",
		OutputFile: outputFile,
		cancel:     cancel,
	}
	m.tasks[id] = task
	m.mu.Unlock()

	go m.execute(ctx, task)

	m.logger.Info("background task iniciada", "id", id, "command", command)
	return id, nil
}

func (m *Manager) execute(ctx context.Context, task *Task) {
	outFile, _ := os.Create(task.OutputFile)
	defer outFile.Close()

	cmd := exec.CommandContext(ctx, "bash", "-c", task.Command)
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	err := cmd.Run()

	m.mu.Lock()
	task.FinishedAt = time.Now()
	if err != nil {
		task.Status = "failed"
		if cmd.ProcessState != nil {
			task.ExitCode = cmd.ProcessState.ExitCode()
		}
	} else {
		task.Status = "done"
		task.ExitCode = 0
	}
	m.mu.Unlock()

	m.logger.Info("background task finalizada",
		"id", task.ID,
		"status", task.Status,
		"duration", time.Since(task.StartedAt),
	)
}

// Kill stops a background task.
func (m *Manager) Kill(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[id]
	if !ok {
		return fmt.Errorf("task %d nao encontrada", id)
	}
	if task.Status != "running" {
		return fmt.Errorf("task %d ja finalizada (%s)", id, task.Status)
	}

	task.cancel()
	task.Status = "killed"
	return nil
}

// GetOutput reads the output of a background task.
func (m *Manager) GetOutput(id int) (string, error) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("task %d nao encontrada", id)
	}

	data, err := os.ReadFile(task.OutputFile)
	if err != nil {
		return "", err
	}

	output := string(data)
	if len(output) > 50000 {
		output = output[len(output)-50000:]
		output = "... [truncado]\n" + output
	}
	return output, nil
}

// List returns all tasks formatted for display.
func (m *Manager) List() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.tasks) == 0 {
		return "Nenhuma tarefa em background."
	}

	var sb strings.Builder
	sb.WriteString("## Background Tasks\n\n")

	for _, t := range m.tasks {
		duration := time.Since(t.StartedAt)
		if !t.FinishedAt.IsZero() {
			duration = t.FinishedAt.Sub(t.StartedAt)
		}

		icon := "⏳"
		switch t.Status {
		case "done":
			icon = "✓"
		case "failed":
			icon = "✗"
		case "killed":
			icon = "⊘"
		}

		cmd := t.Command
		if len(cmd) > 50 {
			cmd = cmd[:50] + "..."
		}

		fmt.Fprintf(&sb, "%s **#%d** [%s] %s (%s)\n", icon, t.ID, t.Status, cmd, formatDuration(duration))
	}
	return sb.String()
}

// Cleanup removes finished tasks and their output files.
func (m *Manager) Cleanup() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for id, t := range m.tasks {
		if t.Status != "running" {
			os.Remove(t.OutputFile)
			delete(m.tasks, id)
			count++
		}
	}
	return count
}

// RunningCount returns number of running tasks.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, t := range m.tasks {
		if t.Status == "running" {
			count++
		}
	}
	return count
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}
