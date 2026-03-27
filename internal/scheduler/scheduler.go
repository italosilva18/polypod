package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Task is a scheduled job.
type Task struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Schedule   string    `json:"schedule"` // cron: "*/5 * * * *"
	Command    string    `json:"command"`
	Prompt     string    `json:"prompt"`
	Channel    string    `json:"channel"`
	UserID     string    `json:"user_id"`
	Enabled    bool      `json:"enabled"`
	LastRun    time.Time `json:"last_run"`
	LastResult string    `json:"last_result"`
	LastError  string    `json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
}

// Scheduler runs tasks on cron schedules.
type Scheduler struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	dataFile string
	quit     chan struct{}
	logger   *slog.Logger
	onRun    func(task *Task, result string)
}

// New creates a scheduler.
func New(dataFile string, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:    make(map[string]*Task),
		dataFile: dataFile,
		quit:     make(chan struct{}),
		logger:   logger,
	}
}

// SetCallback sets a function called when a task runs.
func (s *Scheduler) SetCallback(fn func(task *Task, result string)) {
	s.onRun = fn
}

// Load reads tasks from the data file.
func (s *Scheduler) Load() error {
	data, err := os.ReadFile(s.dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var tasks []*Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tasks {
		s.tasks[t.ID] = t
	}
	return nil
}

// Save persists tasks.
func (s *Scheduler) Save() error {
	s.mu.RLock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.dataFile, data, 0644)
}

// Start begins the scheduler loop (checks every minute).
func (s *Scheduler) Start(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	s.logger.Info("scheduler iniciado", "tasks", len(s.tasks))

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.quit:
			return nil
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, task := range s.tasks {
		if !task.Enabled {
			continue
		}
		if matchCron(task.Schedule, now) {
			go s.runTask(task)
		}
	}
}

func (s *Scheduler) runTask(task *Task) {
	s.logger.Info("executando tarefa", "id", task.ID, "name", task.Name)

	var result string
	var runErr string

	if task.Command != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bash", "-c", task.Command)
		out, err := cmd.CombinedOutput()
		result = strings.TrimSpace(string(out))
		if err != nil {
			runErr = err.Error()
		}
	}

	s.mu.Lock()
	task.LastRun = time.Now()
	task.LastResult = result
	task.LastError = runErr
	s.mu.Unlock()

	if s.onRun != nil {
		s.onRun(task, result)
	}

	s.Save()
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() { close(s.quit) }

// AddTask adds a new task.
func (s *Scheduler) AddTask(task *Task) error {
	if task.ID == "" {
		b := make([]byte, 4)
		rand.Read(b)
		task.ID = "task_" + hex.EncodeToString(b)
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	task.Enabled = true

	s.mu.Lock()
	s.tasks[task.ID] = task
	s.mu.Unlock()
	return s.Save()
}

// RemoveTask removes a task.
func (s *Scheduler) RemoveTask(id string) error {
	s.mu.Lock()
	delete(s.tasks, id)
	s.mu.Unlock()
	return s.Save()
}

// ListTasks returns all tasks.
func (s *Scheduler) ListTasks() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		result = append(result, t)
	}
	return result
}

// matchCron checks if a cron expression matches the given time.
// Format: minute hour day month weekday (supports * and */N).
func matchCron(expr string, t time.Time) bool {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return false
	}
	values := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i, part := range parts {
		if !matchCronField(part, values[i]) {
			return false
		}
	}
	return true
}

func matchCronField(field string, value int) bool {
	if field == "*" {
		return true
	}
	if strings.HasPrefix(field, "*/") {
		n, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		if err != nil || n == 0 {
			return false
		}
		return value%n == 0
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return false
	}
	return value == n
}

// RegisterSkills registers scheduler management skills.
func RegisterSkills(reg *skill.Registry, sched *Scheduler) {
	reg.Register(&skill.Skill{
		Name:        "scheduler_list",
		Description: "Listar tarefas agendadas",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			tasks := sched.ListTasks()
			if len(tasks) == 0 {
				return "Nenhuma tarefa agendada.", nil
			}
			var sb strings.Builder
			for _, t := range tasks {
				status := "ativo"
				if !t.Enabled {
					status = "desativado"
				}
				fmt.Fprintf(&sb, "- **%s** [%s] %s `%s` (%s)\n", t.ID, status, t.Name, t.Schedule, t.Command)
				if !t.LastRun.IsZero() {
					fmt.Fprintf(&sb, "  Ultima execucao: %s\n", t.LastRun.Format("2006-01-02 15:04"))
				}
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "scheduler_add",
		Description: "Adicionar uma tarefa agendada",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":     {Type: jsonschema.String, Description: "Nome da tarefa"},
				"schedule": {Type: jsonschema.String, Description: "Expressao cron (ex: */5 * * * *)"},
				"command":  {Type: jsonschema.String, Description: "Comando shell a executar"},
			},
			Required: []string{"name", "schedule", "command"},
		},
		Execute: func(args map[string]string) (string, error) {
			task := &Task{
				Name:    args["name"],
				Schedule: args["schedule"],
				Command: args["command"],
				Channel: args["channel"],
				UserID:  args["user_id"],
			}
			if err := sched.AddTask(task); err != nil {
				return "", err
			}
			return fmt.Sprintf("Tarefa '%s' agendada com ID %s (cron: %s)", task.Name, task.ID, task.Schedule), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "scheduler_remove",
		Description: "Remover uma tarefa agendada",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id": {Type: jsonschema.String, Description: "ID da tarefa"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := sched.RemoveTask(args["id"]); err != nil {
				return "", err
			}
			return fmt.Sprintf("Tarefa %s removida.", args["id"]), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "scheduler_run",
		Description: "Executar uma tarefa imediatamente",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id": {Type: jsonschema.String, Description: "ID da tarefa"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			sched.mu.RLock()
			task, ok := sched.tasks[args["id"]]
			sched.mu.RUnlock()
			if !ok {
				return "", fmt.Errorf("tarefa %s nao encontrada", args["id"])
			}
			sched.runTask(task)
			return fmt.Sprintf("Tarefa '%s' executada. Resultado: %s", task.Name, task.LastResult), nil
		},
	})
}
