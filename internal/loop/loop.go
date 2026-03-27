package loop

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task is a recurring in-session task.
type Task struct {
	ID       int
	Prompt   string
	Interval time.Duration
	Created  time.Time
	LastRun  time.Time
	RunCount int
	cancel   context.CancelFunc
}

// Manager manages in-session recurring tasks.
type Manager struct {
	mu     sync.Mutex
	tasks  map[int]*Task
	nextID int
	logger *slog.Logger
	onRun  func(prompt string) // callback to execute prompt
}

// NewManager creates a loop manager.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		tasks:  make(map[int]*Task),
		logger: logger,
	}
}

// SetCallback sets the function called when a loop task fires.
func (m *Manager) SetCallback(fn func(string)) {
	m.onRun = fn
}

// Add creates a new recurring task. Returns task ID.
// interval: "5m", "1h", "30s", etc.
func (m *Manager) Add(ctx context.Context, intervalStr, prompt string) (int, error) {
	interval, err := parseDuration(intervalStr)
	if err != nil {
		return 0, fmt.Errorf("intervalo invalido '%s': %w", intervalStr, err)
	}
	if interval < 10*time.Second {
		return 0, fmt.Errorf("intervalo minimo: 10s")
	}

	m.mu.Lock()
	m.nextID++
	id := m.nextID

	taskCtx, cancel := context.WithCancel(ctx)
	task := &Task{
		ID:       id,
		Prompt:   prompt,
		Interval: interval,
		Created:  time.Now(),
		cancel:   cancel,
	}
	m.tasks[id] = task
	m.mu.Unlock()

	// Limit to 20 concurrent loops
	if len(m.tasks) > 20 {
		cancel()
		delete(m.tasks, id)
		return 0, fmt.Errorf("limite de 20 loops atingido")
	}

	go m.run(taskCtx, task)

	m.logger.Info("loop criado", "id", id, "interval", interval, "prompt", prompt)
	return id, nil
}

func (m *Manager) run(ctx context.Context, task *Task) {
	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			task.LastRun = time.Now()
			task.RunCount++
			m.mu.Unlock()

			if m.onRun != nil {
				m.onRun(task.Prompt)
			}
		}
	}
}

// Stop cancels a loop task.
func (m *Manager) Stop(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[id]
	if !ok {
		return fmt.Errorf("loop %d nao encontrado", id)
	}
	task.cancel()
	delete(m.tasks, id)
	return nil
}

// StopAll cancels all loop tasks.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range m.tasks {
		task.cancel()
	}
	m.tasks = make(map[int]*Task)
}

// List returns formatted list of active loops.
func (m *Manager) List() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.tasks) == 0 {
		return "Nenhum loop ativo."
	}

	var sb strings.Builder
	sb.WriteString("## Loops ativos\n\n")
	for _, t := range m.tasks {
		lastRun := "nunca"
		if !t.LastRun.IsZero() {
			lastRun = t.LastRun.Format("15:04:05")
		}
		fmt.Fprintf(&sb, "- **#%d** a cada %s | execucoes: %d | ultima: %s\n  `%s`\n",
			t.ID, t.Interval, t.RunCount, lastRun, t.Prompt)
	}
	return sb.String()
}

// Count returns number of active loops.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tasks)
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	// Handle shorthand: "5m", "1h", "30s", "2d"
	if len(s) > 1 {
		numStr := s[:len(s)-1]
		unit := s[len(s)-1:]

		if n, err := strconv.Atoi(numStr); err == nil {
			switch unit {
			case "s":
				return time.Duration(n) * time.Second, nil
			case "m":
				return time.Duration(n) * time.Minute, nil
			case "h":
				return time.Duration(n) * time.Hour, nil
			case "d":
				return time.Duration(n) * 24 * time.Hour, nil
			}
		}
	}

	// Try standard Go duration
	return time.ParseDuration(s)
}
