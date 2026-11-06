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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Task is a scheduled job.
type Task struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Schedule     string      `json:"schedule"` // cron: "*/5 * * * *"
	Command      string      `json:"command"`
	Prompt       string      `json:"prompt"`
	Channel      string      `json:"channel"`
	UserID       string      `json:"user_id"`
	Enabled      bool        `json:"enabled"`
	LastRun      time.Time   `json:"last_run"`
	LastResult   string      `json:"last_result"`
	LastError    string      `json:"last_error"`
	LastStatus   string      `json:"last_status,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	RunCount     int         `json:"run_count"`
	FailureCount int         `json:"failure_count"`
	LastDuration string      `json:"last_duration,omitempty"`
	MaxRetries   int         `json:"max_retries,omitempty"`
	NextRetryAt  time.Time   `json:"next_retry_at,omitempty"`
	History      []RunRecord `json:"history,omitempty"`
}

// RunRecord is the durable audit trail of one task attempt.
type RunRecord struct {
	ID         string    `json:"id"`
	Attempt    int       `json:"attempt"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Duration   string    `json:"duration"`
	Status     string    `json:"status"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type PromptRunner func(ctx context.Context, task *Task) (string, error)

// Scheduler runs tasks on cron schedules.
type Scheduler struct {
	mu           sync.RWMutex
	saveMu       sync.Mutex
	tasks        map[string]*Task
	dataFile     string
	quit         chan struct{}
	logger       *slog.Logger
	onRun        func(task *Task, result string)
	promptRunner PromptRunner
	running      map[string]bool
	retryDelays  []time.Duration
}

// New creates a scheduler.
func New(dataFile string, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:    make(map[string]*Task),
		running:  make(map[string]bool),
		dataFile: dataFile,
		quit:     make(chan struct{}),
		logger:   logger,
		retryDelays: []time.Duration{
			30 * time.Second,
			time.Minute,
			5 * time.Minute,
		},
	}
}

// SetPromptRunner connects scheduled human-language routines to the complete
// Polypod agent loop. Command-only jobs continue to execute directly.
func (s *Scheduler) SetPromptRunner(fn PromptRunner) {
	s.mu.Lock()
	s.promptRunner = fn
	s.mu.Unlock()
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
		if t.MaxRetries == 0 {
			t.MaxRetries = 3
		}
		s.tasks[t.ID] = t
	}
	return nil
}

// Save persists tasks.
func (s *Scheduler) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.RLock()
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.dataFile), 0o700); err != nil {
		return fmt.Errorf("criando diretorio do agendador: %w", err)
	}
	tmp := s.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("gravando estado temporario do agendador: %w", err)
	}
	if err := os.Rename(tmp, s.dataFile); err != nil {
		return fmt.Errorf("publicando estado do agendador: %w", err)
	}
	return nil
}

// Start begins the scheduler loop (checks every minute).
func (s *Scheduler) Start(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	s.logger.Info("scheduler iniciado", "tasks", len(s.tasks))
	s.resumePendingRetries(ctx)

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
	s.mu.Lock()
	if s.running[task.ID] {
		s.mu.Unlock()
		s.logger.Warn("tarefa ja esta em execucao", "id", task.ID, "name", task.Name)
		return
	}
	s.running[task.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, task.ID)
		s.mu.Unlock()
	}()

	s.logger.Info("executando tarefa", "id", task.ID, "name", task.Name)
	started := time.Now()

	var result, runErr string
	maxAttempts := task.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptStart := time.Now()
		s.mu.Lock()
		task.NextRetryAt = time.Time{}
		s.mu.Unlock()
		result, runErr = s.executeAttempt(task)
		record := RunRecord{
			ID:         fmt.Sprintf("run_%d_%d", attemptStart.UnixNano(), attempt),
			Attempt:    attempt,
			StartedAt:  attemptStart,
			FinishedAt: time.Now(),
			Duration:   time.Since(attemptStart).Round(time.Millisecond).String(),
			Status:     "success",
			Result:     result,
			Error:      runErr,
		}
		if runErr != "" {
			record.Status = "failed"
		}
		s.appendHistory(task, record)
		if saveErr := s.Save(); saveErr != nil {
			s.logger.Warn("falha ao persistir tentativa da rotina", "id", task.ID, "error", saveErr)
		}
		if runErr == "" || attempt == maxAttempts {
			break
		}
		delay := s.retryDelay(attempt - 1)
		s.mu.Lock()
		task.NextRetryAt = time.Now().Add(delay)
		task.LastStatus = "retrying"
		task.LastError = runErr
		s.mu.Unlock()
		if saveErr := s.Save(); saveErr != nil {
			s.logger.Warn("falha ao persistir retomada da rotina", "id", task.ID, "error", saveErr)
		}
		s.logger.Warn("rotina falhou; nova tentativa agendada", "id", task.ID, "attempt", attempt, "retry_in", delay, "error", runErr)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-s.quit:
			timer.Stop()
			return
		}
	}

	s.mu.Lock()
	task.LastRun = time.Now()
	task.LastResult = result
	task.LastError = runErr
	task.RunCount++
	task.LastDuration = time.Since(started).Round(time.Millisecond).String()
	task.NextRetryAt = time.Time{}
	if runErr == "" {
		task.LastStatus = "success"
		task.FailureCount = 0
	} else {
		task.LastStatus = "failed"
		task.FailureCount++
	}
	s.mu.Unlock()

	if s.onRun != nil {
		s.onRun(task, result)
	}

	if err := s.Save(); err != nil {
		s.logger.Error("falha ao salvar resultado da rotina", "id", task.ID, "error", err)
	}
}

func (s *Scheduler) resumePendingRetries(ctx context.Context) {
	type pendingRetry struct {
		task *Task
		at   time.Time
	}
	s.mu.RLock()
	tasks := make([]pendingRetry, 0)
	for _, task := range s.tasks {
		if task.Enabled && !task.NextRetryAt.IsZero() {
			tasks = append(tasks, pendingRetry{task: task, at: task.NextRetryAt})
		}
	}
	s.mu.RUnlock()
	for _, pending := range tasks {
		go func(t *Task, retryAt time.Time) {
			delay := time.Until(retryAt)
			if delay < 0 {
				delay = 0
			}
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-s.quit:
				return
			case <-timer.C:
				s.runTask(t)
			}
		}(pending.task, pending.at)
	}
}

func (s *Scheduler) executeAttempt(task *Task) (string, string) {
	if task.Prompt != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.mu.RLock()
		runner := s.promptRunner
		s.mu.RUnlock()
		if runner == nil {
			return "", "executor de rotinas do agente ainda nao esta conectado"
		}
		result, err := runner(ctx, task)
		if err != nil {
			return result, err.Error()
		}
		return result, ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", task.Command)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return result, err.Error()
	}
	return result, ""
}

func (s *Scheduler) appendHistory(task *Task, record RunRecord) {
	s.mu.Lock()
	task.History = append(task.History, record)
	if len(task.History) > 50 {
		task.History = append([]RunRecord(nil), task.History[len(task.History)-50:]...)
	}
	s.mu.Unlock()
}

func (s *Scheduler) retryDelay(index int) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.retryDelays) == 0 {
		return 0
	}
	if index >= len(s.retryDelays) {
		index = len(s.retryDelays) - 1
	}
	return s.retryDelays[index]
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() { close(s.quit) }

// AddTask adds a new task.
func (s *Scheduler) AddTask(task *Task) error {
	if strings.TrimSpace(task.Name) == "" {
		return fmt.Errorf("nome da rotina e obrigatorio")
	}
	if !validCron(task.Schedule) {
		return fmt.Errorf("expressao cron invalida: %s", task.Schedule)
	}
	if strings.TrimSpace(task.Command) == "" && strings.TrimSpace(task.Prompt) == "" {
		return fmt.Errorf("a rotina precisa de prompt ou command")
	}
	if task.ID == "" {
		b := make([]byte, 4)
		rand.Read(b)
		task.ID = "task_" + hex.EncodeToString(b)
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.MaxRetries == 0 {
		task.MaxRetries = 3
	}
	task.Enabled = true

	s.mu.Lock()
	s.tasks[task.ID] = task
	s.mu.Unlock()
	return s.Save()
}

func validCron(expr string) bool {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		if part == "*" {
			continue
		}
		if strings.HasPrefix(part, "*/") {
			n, err := strconv.Atoi(strings.TrimPrefix(part, "*/"))
			if err != nil || n <= 0 {
				return false
			}
			continue
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
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

// GetTask returns a task by ID.
func (s *Scheduler) GetTask(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	return task, ok
}

// SetEnabled enables or pauses a task and persists the change.
func (s *Scheduler) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	task, ok := s.tasks[id]
	if ok {
		task.Enabled = enabled
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("tarefa %s nao encontrada", id)
	}
	return s.Save()
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
				action := t.Command
				if t.Prompt != "" {
					action = "agente: " + t.Prompt
				}
				fmt.Fprintf(&sb, "- **%s** [%s] %s `%s` (%s)\n", t.ID, status, t.Name, t.Schedule, action)
				if !t.LastRun.IsZero() {
					fmt.Fprintf(&sb, "  Ultima execucao: %s | status: %s | duracao: %s | total: %d\n", t.LastRun.Format("2006-01-02 15:04"), t.LastStatus, t.LastDuration, t.RunCount)
					if t.LastError != "" {
						fmt.Fprintf(&sb, "  Ultimo erro: %s\n", t.LastError)
					}
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
				"name":        {Type: jsonschema.String, Description: "Nome humano e curto da rotina"},
				"schedule":    {Type: jsonschema.String, Description: "Expressao cron no horario local America/Sao_Paulo (ex: 0 9 * * 1-5 nao e aceito; use campos simples ou */N)"},
				"prompt":      {Type: jsonschema.String, Description: "Objetivo completo para despertar o agente, incluindo contexto, verificacao de sucesso e o que fazer em caso de erro"},
				"command":     {Type: jsonschema.String, Description: "Comando shell exato; use somente para automacoes mecanicas que nao exigem raciocinio"},
				"max_retries": {Type: jsonschema.Integer, Description: "Numero de novas tentativas em caso de falha (padrao 3, maximo 5)"},
			},
			Required: []string{"name", "schedule"},
		},
		Execute: func(args map[string]string) (string, error) {
			maxRetries := 3
			if raw := strings.TrimSpace(args["max_retries"]); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil || parsed < 0 || parsed > 5 {
					return "", fmt.Errorf("max_retries deve ser um inteiro entre 0 e 5")
				}
				maxRetries = parsed
			}
			task := &Task{
				Name:       args["name"],
				Schedule:   args["schedule"],
				Command:    args["command"],
				Prompt:     args["prompt"],
				Channel:    args["channel"],
				UserID:     args["user_id"],
				MaxRetries: maxRetries,
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
		Name:        "scheduler_history",
		Description: "Consultar o historico duravel de tentativas e resultados de uma rotina",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id": {Type: jsonschema.String, Description: "ID da tarefa"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			task, ok := sched.GetTask(args["id"])
			if !ok {
				return "", fmt.Errorf("tarefa %s nao encontrada", args["id"])
			}
			if len(task.History) == 0 {
				return "A rotina ainda nao possui execucoes registradas.", nil
			}
			var sb strings.Builder
			for _, run := range task.History {
				fmt.Fprintf(&sb, "- %s tentativa %d: %s (%s)", run.StartedAt.Format("2006-01-02 15:04:05"), run.Attempt, run.Status, run.Duration)
				if run.Error != "" {
					fmt.Fprintf(&sb, " | erro: %s", run.Error)
				}
				if run.Result != "" {
					fmt.Fprintf(&sb, " | resultado: %s", run.Result)
				}
				sb.WriteByte('\n')
			}
			return sb.String(), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "scheduler_toggle",
		Description: "Ativar ou pausar uma rotina existente",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id":      {Type: jsonschema.String, Description: "ID da tarefa"},
				"enabled": {Type: jsonschema.Boolean, Description: "true para ativar; false para pausar"},
			},
			Required: []string{"id", "enabled"},
		},
		Execute: func(args map[string]string) (string, error) {
			enabled, err := strconv.ParseBool(args["enabled"])
			if err != nil {
				return "", fmt.Errorf("enabled deve ser true ou false")
			}
			if err := sched.SetEnabled(args["id"], enabled); err != nil {
				return "", err
			}
			return fmt.Sprintf("Rotina %s atualizada: enabled=%t", args["id"], enabled), nil
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
			task, ok := sched.GetTask(args["id"])
			if !ok {
				return "", fmt.Errorf("tarefa %s nao encontrada", args["id"])
			}
			sched.runTask(task)
			return fmt.Sprintf("Tarefa '%s' executada. Resultado: %s", task.Name, task.LastResult), nil
		},
	})
}
