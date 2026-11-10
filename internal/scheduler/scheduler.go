package scheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/evolution"
	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

const maxRoutineCommandOutputBytes = 64 << 10

// routineCommandOutput bounds command output before it enters the task state,
// audit journal or notification outbox. Canceling on overflow also stops a
// command that would otherwise keep producing discarded output until timeout.
type routineCommandOutput struct {
	mu        sync.Mutex
	data      bytes.Buffer
	cancel    context.CancelFunc
	truncated bool
}

func (w *routineCommandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := maxRoutineCommandOutputBytes - w.data.Len()
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = w.data.Write(p[:remaining])
		}
		w.truncated = true
		w.cancel()
		return len(p), nil
	}
	_, _ = w.data.Write(p)
	return len(p), nil
}

func (w *routineCommandOutput) snapshot() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String(), w.truncated
}

// Task is a scheduled job.
type Task struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Schedule         string             `json:"schedule"` // cron: "*/5 * * * *"
	TimeZone         string             `json:"time_zone,omitempty"`
	MisfirePolicy    string             `json:"misfire_policy,omitempty"` // run_once or skip
	Command          string             `json:"command"`
	Prompt           string             `json:"prompt"`
	Channel          string             `json:"channel"`
	UserID           string             `json:"user_id"`
	Enabled          bool               `json:"enabled"`
	LastRun          time.Time          `json:"last_run"`
	LastResult       string             `json:"last_result"`
	LastError        string             `json:"last_error"`
	LastStatus       string             `json:"last_status,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	RunCount         int                `json:"run_count"`
	FailureCount     int                `json:"failure_count"`
	LastDuration     string             `json:"last_duration,omitempty"`
	MaxRetries       int                `json:"max_retries,omitempty"`
	NextRetryAt      time.Time          `json:"next_retry_at,omitempty"`
	NextRunAt        time.Time          `json:"next_run_at,omitempty"`
	CurrentRunID     string             `json:"current_run_id,omitempty"`
	CurrentDueAt     time.Time          `json:"current_due_at,omitempty"`
	CurrentAttempt   int                `json:"current_attempt,omitempty"`
	CurrentStartedAt time.Time          `json:"current_started_at,omitempty"`
	History          []RunRecord        `json:"history,omitempty"`
	PendingNotices   []CompletionNotice `json:"pending_notices,omitempty"`
}

// CompletionNotice is persisted until the application's outbox confirms it.
type CompletionNotice struct {
	RunID         string    `json:"run_id"`
	Status        string    `json:"status"`
	Result        string    `json:"result,omitempty"`
	Error         string    `json:"error,omitempty"`
	FinishedAt    time.Time `json:"finished_at"`
	Attempts      int       `json:"attempts,omitempty"`
	NextAttemptAt time.Time `json:"next_attempt_at,omitempty"`
}

// RunRecord is the durable audit trail of one task attempt.
type RunRecord struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	ScheduledFor time.Time `json:"scheduled_for,omitempty"`
	Attempt      int       `json:"attempt"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Duration     string    `json:"duration"`
	Status       string    `json:"status"`
	Result       string    `json:"result,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type PromptRunner func(ctx context.Context, task *Task) (string, error)

// Scheduler runs tasks on cron schedules.
type Scheduler struct {
	mu     sync.RWMutex
	saveMu sync.Mutex
	tasks  map[string]*Task
	// baseline is the last committed JSON for each task seen by this process.
	// Save overlays only tasks changed since that snapshot.
	baseline     map[string][]byte
	dataFile     string
	quit         chan struct{}
	stopOnce     sync.Once
	logger       *slog.Logger
	onRun        func(task *Task, result string)
	onRunDurable func(task *Task, notice CompletionNotice) error
	promptRunner PromptRunner
	running      map[string]bool
	notifying    map[string]bool
	retryDelays  []time.Duration
}

// New creates a scheduler.
func New(dataFile string, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		tasks:     make(map[string]*Task),
		baseline:  make(map[string][]byte),
		running:   make(map[string]bool),
		notifying: make(map[string]bool),
		dataFile:  dataFile,
		quit:      make(chan struct{}),
		logger:    logger,
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
	s.mu.Lock()
	s.onRun = fn
	s.mu.Unlock()
}

// SetDurableCallback connects a completion notice to a durable application
// outbox. Failed deliveries remain pending and are retried after restart.
func (s *Scheduler) SetDurableCallback(fn func(task *Task, notice CompletionNotice) error) {
	s.mu.Lock()
	s.onRunDurable = fn
	s.mu.Unlock()
}

// Load reads tasks from the data file.
func (s *Scheduler) Load() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	stateLease, err := s.stateLease()
	if err != nil {
		return err
	}
	defer stateLease.Close()
	tasks, baseline, err := s.readSnapshot()
	if err != nil {
		return err
	}
	journal, journalErr := s.readRunJournalUnlocked()
	if journalErr != nil {
		s.logger.Warn("historico de execucoes indisponivel para reconciliacao", "error", journalErr)
		for _, task := range tasks {
			if task.CurrentRunID != "" {
				return fmt.Errorf("nao e seguro retomar rotina %s sem ler o journal de tentativas: %w", task.ID, journalErr)
			}
		}
	}
	latest := make(map[string]RunRecord)
	for _, record := range journal {
		if record.TaskID == "" || record.RunID == "" {
			continue
		}
		key := record.TaskID + ":" + record.RunID
		if old, ok := latest[key]; !ok || record.Attempt > old.Attempt || (record.Attempt == old.Attempt && record.FinishedAt.After(old.FinishedAt)) {
			latest[key] = record
		}
	}
	now := time.Now()
	recovered := false
	recoveredRecords := make([]RunRecord, 0)
	var runLeases []*fileLease
	defer func() {
		for _, lease := range runLeases {
			if err := lease.Close(); err != nil {
				s.logger.Warn("falha ao soltar lock de rotina", "error", err)
			}
		}
	}()
	s.mu.Lock()
	s.tasks = make(map[string]*Task, len(tasks))
	s.baseline = baseline
	for _, t := range tasks {
		if t == nil || t.ID == "" {
			continue
		}
		if t.CurrentRunID != "" {
			lease, acquired, leaseErr := s.taskLease(t.ID)
			if leaseErr != nil {
				s.mu.Unlock()
				return leaseErr
			}
			if !acquired {
				// Another process still owns the live attempt. It alone may
				// finalize or recover this run.
				s.tasks[t.ID] = t
				continue
			}
			runLeases = append(runLeases, lease)
		}
		if t.MisfirePolicy == "" {
			t.MisfirePolicy = "run_once"
			recovered = true
		}
		if t.CurrentRunID != "" {
			if record, ok := latest[t.ID+":"+t.CurrentRunID]; ok && record.Attempt == t.CurrentAttempt && (record.Status == "success" || record.Status == "failed") {
				if record.Status == "success" || t.CurrentAttempt > t.MaxRetries {
					t.LastRun = record.FinishedAt
					t.LastResult = record.Result
					t.LastError = record.Error
					t.LastStatus = record.Status
					t.LastDuration = record.FinishedAt.Sub(t.CurrentStartedAt).Round(time.Millisecond).String()
					t.RunCount++
					if record.Status == "failed" {
						t.FailureCount++
					} else {
						t.FailureCount = 0
					}
					t.PendingNotices = append(t.PendingNotices, CompletionNotice{RunID: t.CurrentRunID, Status: record.Status, Result: record.Result, Error: record.Error, FinishedAt: record.FinishedAt, NextAttemptAt: now})
					t.CurrentRunID = ""
					t.CurrentAttempt = 0
					t.CurrentStartedAt = time.Time{}
					t.CurrentDueAt = time.Time{}
					t.NextRetryAt = time.Time{}
					if strings.HasPrefix(t.Schedule, "@at ") {
						t.Enabled = false
						t.NextRunAt = time.Time{}
					}
				} else {
					t.LastStatus = "retrying"
					t.LastError = record.Error
					t.NextRetryAt = now
				}
				recovered = true
			}
		}
		if t.Enabled && t.CurrentRunID == "" && t.NextRunAt.IsZero() {
			base := t.CreatedAt
			if !t.LastRun.IsZero() {
				base = t.LastRun
			}
			if base.IsZero() {
				base = now
			}
			if next, nextErr := nextOccurrence(t.Schedule, base, t.TimeZone); nextErr == nil {
				t.NextRunAt = next
				recovered = true
			} else {
				s.logger.Warn("rotina carregada com horario invalido", "id", t.ID, "error", nextErr)
				t.Enabled = false
			}
		}
		if t.CurrentRunID != "" && t.LastStatus == "running" {
			if t.CurrentAttempt == 0 {
				t.LastStatus = "retrying"
				t.NextRetryAt = now
				recovered = true
				s.tasks[t.ID] = t
				continue
			}
			prior, exists := latest[t.ID+":"+t.CurrentRunID]
			if !exists || prior.Attempt != t.CurrentAttempt || prior.Status != "interrupted" {
				record := RunRecord{
					ID: fmt.Sprintf("%s_interrupted_%d", t.CurrentRunID, t.CurrentAttempt), TaskID: t.ID, RunID: t.CurrentRunID,
					ScheduledFor: t.CurrentDueAt, Attempt: t.CurrentAttempt,
					StartedAt: t.CurrentStartedAt, FinishedAt: now,
					Status: "interrupted", Error: "processo interrompido antes de confirmar resultado",
				}
				t.History = append(t.History, record)
				recoveredRecords = append(recoveredRecords, record)
			}
			if t.CurrentAttempt <= t.MaxRetries {
				t.LastStatus = "retrying"
				t.NextRetryAt = now
			} else {
				t.LastStatus = "failed"
				t.LastError = "execucao interrompida; resultado externo nao confirmado"
				if strings.HasPrefix(t.Schedule, "@at ") {
					t.Enabled = false
					t.NextRunAt = time.Time{}
				}
				t.PendingNotices = append(t.PendingNotices, CompletionNotice{
					RunID: t.CurrentRunID, Status: t.LastStatus, Error: t.LastError,
					FinishedAt: now, NextAttemptAt: now,
				})
				t.CurrentRunID = ""
				t.CurrentAttempt = 0
				t.CurrentStartedAt = time.Time{}
				t.CurrentDueAt = time.Time{}
				t.RunCount++
				t.FailureCount++
			}
			recovered = true
		}
		s.tasks[t.ID] = t
	}
	s.mu.Unlock()
	for _, record := range recoveredRecords {
		if err := s.writeRunRecordUnlocked(record); err != nil {
			return err
		}
	}
	if recovered {
		return s.saveUnderStateLock()
	}
	return nil
}

// Save persists tasks.
func (s *Scheduler) Save() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	lease, err := s.stateLease()
	if err != nil {
		return err
	}
	defer lease.Close()
	return s.saveUnderStateLock()
}

// Start begins the scheduler loop. A persisted due time lets it recover one
// missed occurrence after a restart rather than silently skipping it.
func (s *Scheduler) Start(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer s.Stop()

	s.logger.Info("scheduler iniciado", "tasks", len(s.tasks))
	s.tick(time.Now())

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
	if err := s.refresh(); err != nil {
		s.logger.Error("falha ao sincronizar rotinas entre processos", "error", err)
		return
	}
	s.mu.RLock()
	due := make([]*Task, 0)
	notices := make([]string, 0)
	for _, task := range s.tasks {
		if s.onRunDurable != nil && len(task.PendingNotices) > 0 && !task.PendingNotices[0].NextAttemptAt.After(now) && !s.notifying[task.ID] {
			notices = append(notices, task.ID)
		}
		if s.running[task.ID] {
			continue
		}
		if task.CurrentRunID != "" {
			if task.LastStatus == "running" || (task.LastStatus == "retrying" && !task.NextRetryAt.After(now)) {
				due = append(due, task)
			}
			continue
		}
		if task.Enabled && !task.NextRunAt.IsZero() && !task.NextRunAt.After(now) {
			due = append(due, task)
		}
	}
	s.mu.RUnlock()
	for _, task := range due {
		go s.runTaskAt(task, now, false)
	}
	for _, id := range notices {
		go s.deliverNotices(id)
	}
}

func (s *Scheduler) runTask(task *Task) {
	s.runTaskAt(task, time.Now(), true)
}

func (s *Scheduler) runTaskAt(task *Task, now time.Time, manual bool) {
	lease, acquired, err := s.taskLease(task.ID)
	if err != nil {
		s.logger.Error("falha ao adquirir dono da rotina", "id", task.ID, "error", err)
		return
	}
	if !acquired {
		return
	}
	defer func() {
		if err := lease.Close(); err != nil {
			s.logger.Warn("falha ao soltar dono da rotina", "id", task.ID, "error", err)
		}
	}()
	s.runTaskAtOwned(task, now, manual)
}

// runTaskAtOwned executes only while this process holds the per-task file
// lease. It refreshes the shared snapshot before checking whether the run is
// still due; a stale tick can therefore never claim an already completed run.
func (s *Scheduler) runTaskAtOwned(task *Task, now time.Time, manual bool) {
	select {
	case <-s.quit:
		return
	default:
	}
	if err := s.refresh(); err != nil {
		s.logger.Error("falha ao carregar rotina antes da execucao", "id", task.ID, "error", err)
		return
	}
	if err := s.recoverOrphanedRun(task.ID, now); err != nil {
		s.logger.Error("falha ao reconciliar execucao interrompida", "id", task.ID, "error", err)
		return
	}
	s.mu.Lock()
	// A queued tick must not execute a replacement definition from another
	// process. The next tick will pick up the new pointer and schedule it.
	current, ok := s.tasks[task.ID]
	if !ok || current != task {
		s.mu.Unlock()
		return
	}
	if s.running[task.ID] {
		s.mu.Unlock()
		return
	}
	select {
	case <-s.quit:
		s.mu.Unlock()
		return
	default:
	}
	if task.CurrentRunID != "" {
		if task.LastStatus != "retrying" || task.NextRetryAt.After(now) {
			s.mu.Unlock()
			return
		}
	} else if !manual && (!task.Enabled || task.NextRunAt.IsZero() || task.NextRunAt.After(now)) {
		s.mu.Unlock()
		return
	}
	if !manual && task.CurrentRunID == "" && task.MisfirePolicy == "skip" && now.Sub(task.NextRunAt) > time.Minute {
		missed := task.NextRunAt
		next, err := nextOccurrence(task.Schedule, now, task.TimeZone)
		if err != nil {
			task.Enabled = false
			task.LastError = err.Error()
		} else {
			task.NextRunAt = next
		}
		record := RunRecord{ID: fmt.Sprintf("skipped_%d", now.UnixNano()), TaskID: task.ID, ScheduledFor: missed, StartedAt: now, FinishedAt: now, Status: "skipped", Error: "horario perdido durante indisponibilidade"}
		s.mu.Unlock()
		if err := s.appendHistory(task, record); err != nil {
			s.logger.Error("falha ao persistir historico de horario perdido", "id", task.ID, "error", err)
		}
		if err := s.Save(); err != nil {
			s.logger.Error("falha ao salvar rotina ignorada", "id", task.ID, "error", err)
		}
		return
	}
	s.running[task.ID] = true
	if task.CurrentRunID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			delete(s.running, task.ID)
			s.mu.Unlock()
			return
		}
		due := now
		if !manual {
			due = task.NextRunAt
			next, err := nextOccurrence(task.Schedule, now, task.TimeZone)
			if err != nil {
				delete(s.running, task.ID)
				s.mu.Unlock()
				s.logger.Error("falha ao calcular proxima execucao", "id", task.ID, "error", err)
				return
			}
			task.NextRunAt = next
		}
		task.CurrentRunID = "run_" + hex.EncodeToString(b)
		task.CurrentDueAt = due
		task.CurrentStartedAt = now
		task.CurrentAttempt = 0
	}
	task.LastStatus = "retrying"
	if task.NextRetryAt.IsZero() {
		task.NextRetryAt = now
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, task.ID)
		s.mu.Unlock()
	}()

	if err := s.Save(); err != nil {
		s.logger.Error("rotina nao iniciou porque estado nao foi persistido", "id", task.ID, "error", err)
		return
	}
	s.logger.Info("executando tarefa", "id", task.ID, "name", task.Name, "run_id", task.CurrentRunID)
	var result, runErr string
	maxAttempts := task.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := task.CurrentAttempt + 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-s.quit:
			return
		default:
		}
		attemptStart := time.Now()
		s.mu.Lock()
		task.CurrentAttempt = attempt
		task.LastStatus = "running"
		task.NextRetryAt = time.Time{}
		attemptTask := *task
		s.mu.Unlock()
		if err := s.Save(); err != nil {
			s.logger.Error("tentativa nao iniciada porque estado nao foi persistido", "id", task.ID, "error", err)
			s.mu.Lock()
			task.CurrentAttempt = attempt - 1
			task.LastStatus = "retrying"
			task.NextRetryAt = time.Now()
			s.mu.Unlock()
			return
		}
		result, runErr = s.executeAttempt(&attemptTask)
		record := RunRecord{
			ID:           fmt.Sprintf("%s_attempt_%d", attemptTask.CurrentRunID, attempt),
			TaskID:       task.ID,
			RunID:        attemptTask.CurrentRunID,
			ScheduledFor: attemptTask.CurrentDueAt,
			Attempt:      attempt,
			StartedAt:    attemptStart,
			FinishedAt:   time.Now(),
			Duration:     time.Since(attemptStart).Round(time.Millisecond).String(),
			Status:       "success",
			Result:       result,
			Error:        runErr,
		}
		if runErr != "" {
			record.Status = "failed"
		}
		if err := s.appendHistory(task, record); err != nil {
			s.logger.Error("falha ao persistir historico da tentativa", "id", task.ID, "error", err)
		}
		if runErr != "" && attempt < maxAttempts {
			delay := s.retryDelay(attempt - 1)
			s.mu.Lock()
			task.NextRetryAt = time.Now().Add(delay)
			task.LastStatus = "retrying"
			task.LastError = runErr
			s.mu.Unlock()
			if err := s.Save(); err != nil {
				s.logger.Error("falha ao persistir retomada da rotina", "id", task.ID, "error", err)
				return
			}
			s.logger.Warn("rotina falhou; nova tentativa agendada", "id", task.ID, "attempt", attempt, "retry_in", delay, "error", runErr)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-s.quit:
				timer.Stop()
				return
			}
			continue
		}
		break
	}

	s.mu.Lock()
	task.LastRun = time.Now()
	task.LastResult = result
	task.LastError = runErr
	task.RunCount++
	task.LastDuration = time.Since(task.CurrentStartedAt).Round(time.Millisecond).String()
	task.NextRetryAt = time.Time{}
	if runErr == "" {
		task.LastStatus = "success"
		task.FailureCount = 0
	} else {
		task.LastStatus = "failed"
		task.FailureCount++
	}
	if s.onRunDurable != nil {
		task.PendingNotices = append(task.PendingNotices, CompletionNotice{
			RunID: task.CurrentRunID, Status: task.LastStatus, Result: result,
			Error: runErr, FinishedAt: task.LastRun, NextAttemptAt: task.LastRun,
		})
	}
	task.CurrentRunID = ""
	task.CurrentDueAt = time.Time{}
	task.CurrentAttempt = 0
	task.CurrentStartedAt = time.Time{}
	if strings.HasPrefix(task.Schedule, "@at ") {
		task.Enabled = false
	}
	completed := *task
	completed.History = append([]RunRecord(nil), task.History...)
	completed.PendingNotices = append([]CompletionNotice(nil), task.PendingNotices...)
	callback := s.onRun
	durableCallback := s.onRunDurable
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		s.logger.Error("falha ao salvar resultado da rotina", "id", task.ID, "error", err)
		return
	}
	if callback != nil {
		callback(&completed, result)
	}
	if durableCallback != nil {
		s.deliverNoticesOwned(task.ID)
	}
}

func (s *Scheduler) deliverNotices(id string) {
	select {
	case <-s.quit:
		return
	default:
	}
	lease, acquired, err := s.taskLease(id)
	if err != nil {
		s.logger.Error("falha ao adquirir entrega de notificacao", "id", id, "error", err)
		return
	}
	if !acquired {
		return
	}
	defer lease.Close()
	if err := s.refresh(); err != nil {
		s.logger.Error("falha ao carregar notificacoes", "id", id, "error", err)
		return
	}
	s.deliverNoticesOwned(id)
}

func (s *Scheduler) deliverNoticesOwned(id string) {
	s.mu.Lock()
	if s.notifying[id] || s.onRunDurable == nil {
		s.mu.Unlock()
		return
	}
	s.notifying[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.notifying, id)
		s.mu.Unlock()
	}()

	for {
		s.mu.RLock()
		task, ok := s.tasks[id]
		if !ok || len(task.PendingNotices) == 0 || task.PendingNotices[0].NextAttemptAt.After(time.Now()) {
			s.mu.RUnlock()
			return
		}
		notice := task.PendingNotices[0]
		snapshot := *task
		snapshot.History = append([]RunRecord(nil), task.History...)
		snapshot.PendingNotices = append([]CompletionNotice(nil), task.PendingNotices...)
		callback := s.onRunDurable
		s.mu.RUnlock()

		err := callback(&snapshot, notice)
		s.mu.Lock()
		// A callback may race with a routine edit, but the notice at the head
		// must still identify the same completed run before it is changed.
		task, ok = s.tasks[id]
		if !ok || len(task.PendingNotices) == 0 || task.PendingNotices[0].RunID != notice.RunID {
			s.mu.Unlock()
			return
		}
		if err == nil {
			task.PendingNotices = task.PendingNotices[1:]
		} else {
			task.PendingNotices[0].Attempts++
			task.PendingNotices[0].NextAttemptAt = time.Now().Add(s.retryDelayForNotice(task.PendingNotices[0].Attempts))
		}
		s.mu.Unlock()
		if saveErr := s.Save(); saveErr != nil {
			s.logger.Error("falha ao persistir entrega da rotina", "id", id, "error", saveErr)
			return
		}
		if err != nil {
			s.logger.Warn("notificacao da rotina sera repetida", "id", id, "run_id", notice.RunID, "error", err)
			return
		}
	}
}

func (s *Scheduler) retryDelayForNotice(attempt int) time.Duration {
	if attempt <= 1 {
		return 30 * time.Second
	}
	if attempt == 2 {
		return time.Minute
	}
	return 5 * time.Minute
}

func (s *Scheduler) executeAttempt(task *Task) (string, string) {
	if task.Prompt != "" {
		ctx, cancel := s.attemptContext(30 * time.Minute)
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

	ctx, cancel := s.attemptContext(5 * time.Minute)
	defer cancel()
	commandCtx, stopCommand := context.WithCancel(ctx)
	defer stopCommand()
	output := &routineCommandOutput{cancel: stopCommand}
	cmd := exec.CommandContext(commandCtx, "bash", "-c", task.Command)
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	raw, truncated := output.snapshot()
	result := strings.TrimSpace(raw)
	if truncated {
		result += "\n[Saída da rotina truncada após 64 KiB.]"
		if err != nil {
			return result, fmt.Sprintf("saída da rotina excedeu 64 KiB; comando interrompido: %v", err)
		}
		return result, "saída da rotina excedeu 64 KiB"
	}
	if err != nil {
		return result, err.Error()
	}
	return result, ""
}

func (s *Scheduler) attemptContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	done := make(chan struct{})
	go func() {
		select {
		case <-s.quit:
			cancel()
		case <-done:
		}
	}()
	return ctx, func() { close(done); cancel() }
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
func (s *Scheduler) Stop() { s.stopOnce.Do(func() { close(s.quit) }) }

// AddTask adds a new task.
func (s *Scheduler) AddTask(task *Task) error {
	if strings.TrimSpace(task.Name) == "" {
		return fmt.Errorf("nome da rotina e obrigatorio")
	}
	if _, err := parseSchedule(task.Schedule); err != nil {
		return fmt.Errorf("agendamento invalido: %w", err)
	}
	if task.MisfirePolicy == "" {
		task.MisfirePolicy = "run_once"
	}
	if task.MisfirePolicy != "run_once" && task.MisfirePolicy != "skip" {
		return fmt.Errorf("misfire_policy deve ser run_once ou skip")
	}
	if task.MaxRetries < 0 || task.MaxRetries > 5 {
		return fmt.Errorf("max_retries deve estar entre 0 e 5")
	}
	if strings.TrimSpace(task.Command) == "" && strings.TrimSpace(task.Prompt) == "" {
		return fmt.Errorf("a rotina precisa de prompt ou command")
	}
	now := time.Now()
	next, err := nextOccurrence(task.Schedule, now, task.TimeZone)
	if err != nil {
		return err
	}
	if next.IsZero() {
		return fmt.Errorf("horario unico ja passou: %s", task.Schedule)
	}
	if task.ID == "" {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		task.ID = "task_" + hex.EncodeToString(b)
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.NextRunAt = next
	task.Enabled = true
	lease, err := s.editLease(task.ID)
	if err != nil {
		return err
	}
	defer lease.Close()

	s.mu.Lock()
	if _, exists := s.tasks[task.ID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("rotina %s ja existe", task.ID)
	}
	s.tasks[task.ID] = task
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		// A failed create must not leave a runnable routine in memory when the
		// caller has been told that scheduling failed. Preserve any newer value
		// installed under the same ID by another operation.
		s.mu.Lock()
		if s.tasks[task.ID] == task {
			delete(s.tasks, task.ID)
		}
		s.mu.Unlock()
		if refreshErr := s.refresh(); refreshErr != nil {
			s.logger.Warn("falha ao recarregar rotina apos criacao recusada", "error", refreshErr)
		}
		return err
	}
	return nil
}

func validCron(expr string) bool {
	_, err := parseSchedule(expr)
	return err == nil
}

// RemoveTask removes a task.
func (s *Scheduler) RemoveTask(id string) error {
	lease, err := s.editLease(id)
	if err != nil {
		return err
	}
	defer lease.Close()
	s.mu.Lock()
	prior, exists := s.tasks[id]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	delete(s.tasks, id)
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		s.mu.Lock()
		s.tasks[id] = prior
		s.mu.Unlock()
		if refreshErr := s.refresh(); refreshErr != nil {
			s.logger.Warn("falha ao recarregar rotina apos remocao recusada", "id", id, "error", refreshErr)
		}
		return err
	}
	return nil
}

// ListTasks returns all tasks.
func (s *Scheduler) ListTasks() []*Task {
	if err := s.refresh(); err != nil {
		s.logger.Warn("lista de rotinas pode estar desatualizada", "error", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		copy := *t
		copy.History = append([]RunRecord(nil), t.History...)
		copy.PendingNotices = append([]CompletionNotice(nil), t.PendingNotices...)
		result = append(result, &copy)
	}
	return result
}

// GetTask returns a task by ID.
func (s *Scheduler) GetTask(id string) (*Task, bool) {
	if err := s.refresh(); err != nil {
		s.logger.Warn("rotina pode estar desatualizada", "id", id, "error", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	copy := *task
	copy.History = append([]RunRecord(nil), task.History...)
	copy.PendingNotices = append([]CompletionNotice(nil), task.PendingNotices...)
	return &copy, true
}

// RunNow executes a routine without changing its next scheduled occurrence.
func (s *Scheduler) RunNow(id string) (*Task, error) {
	select {
	case <-s.quit:
		return nil, fmt.Errorf("agendador encerrado")
	default:
	}
	lease, acquired, err := s.taskLease(id)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, fmt.Errorf("tarefa %s ja esta em execucao", id)
	}
	defer lease.Close()
	if err := s.refresh(); err != nil {
		return nil, err
	}
	if err := s.recoverOrphanedRun(id, time.Now()); err != nil {
		return nil, err
	}
	s.mu.RLock()
	task, ok := s.tasks[id]
	running := s.running[id]
	active := ok && task.CurrentRunID != ""
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tarefa %s nao encontrada", id)
	}
	if running || active {
		return nil, fmt.Errorf("tarefa %s ja esta em execucao", id)
	}
	s.runTaskAtOwned(task, time.Now(), true)
	result, ok := s.GetTask(id)
	if !ok {
		return nil, fmt.Errorf("tarefa %s removida durante execucao", id)
	}
	return result, nil
}

// SetEnabled enables or pauses a task and persists the change.
func (s *Scheduler) SetEnabled(id string, enabled bool) error {
	lease, err := s.editLease(id)
	if err != nil {
		return err
	}
	defer lease.Close()
	s.mu.Lock()
	task, ok := s.tasks[id]
	var prior *Task
	if ok {
		prior = cloneTask(task)
		if enabled && task.NextRunAt.IsZero() && task.CurrentRunID == "" {
			next, err := nextOccurrence(task.Schedule, time.Now(), task.TimeZone)
			if err != nil || next.IsZero() {
				s.mu.Unlock()
				return fmt.Errorf("rotina %s nao possui proxima execucao", id)
			}
			task.NextRunAt = next
		}
		task.Enabled = enabled
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("tarefa %s nao encontrada", id)
	}
	if err := s.Save(); err != nil {
		s.mu.Lock()
		*task = *prior
		s.mu.Unlock()
		if refreshErr := s.refresh(); refreshErr != nil {
			s.logger.Warn("falha ao recarregar rotina apos pausa recusada", "id", id, "error", refreshErr)
		}
		return err
	}
	return nil
}

// UpdateTask changes the editable definition of a routine. A running routine
// keeps its original definition until that execution finishes.
func (s *Scheduler) UpdateTask(id string, args map[string]string) error {
	lease, err := s.editLease(id)
	if err != nil {
		return err
	}
	defer lease.Close()
	s.mu.Lock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("tarefa %s nao encontrada", id)
	}
	if s.running[id] || task.CurrentRunID != "" {
		s.mu.Unlock()
		return fmt.Errorf("tarefa %s esta em execucao; tente novamente depois", id)
	}
	prior := cloneTask(task)
	updated := *task
	if v, exists := args["name"]; exists {
		updated.Name = strings.TrimSpace(v)
	}
	if v, exists := args["schedule"]; exists {
		updated.Schedule = strings.TrimSpace(v)
	}
	if v, exists := args["time_zone"]; exists {
		updated.TimeZone = strings.TrimSpace(v)
	}
	if v, exists := args["misfire_policy"]; exists {
		updated.MisfirePolicy = strings.TrimSpace(v)
	}
	if v, exists := args["prompt"]; exists {
		updated.Prompt = v
	}
	if v, exists := args["command"]; exists {
		updated.Command = v
	}
	if v, exists := args["max_retries"]; exists {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("max_retries deve ser inteiro")
		}
		updated.MaxRetries = parsed
	}
	if updated.Name == "" || (strings.TrimSpace(updated.Prompt) == "" && strings.TrimSpace(updated.Command) == "") {
		s.mu.Unlock()
		return fmt.Errorf("nome e prompt ou command sao obrigatorios")
	}
	if updated.MaxRetries < 0 || updated.MaxRetries > 5 {
		s.mu.Unlock()
		return fmt.Errorf("max_retries deve estar entre 0 e 5")
	}
	if updated.MisfirePolicy != "run_once" && updated.MisfirePolicy != "skip" {
		s.mu.Unlock()
		return fmt.Errorf("misfire_policy deve ser run_once ou skip")
	}
	next, err := nextOccurrence(updated.Schedule, time.Now(), updated.TimeZone)
	if err != nil || next.IsZero() {
		s.mu.Unlock()
		return fmt.Errorf("agendamento invalido ou expirado: %s", updated.Schedule)
	}
	updated.NextRunAt = next
	*task = updated
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		s.mu.Lock()
		*task = *prior
		s.mu.Unlock()
		if refreshErr := s.refresh(); refreshErr != nil {
			s.logger.Warn("falha ao recarregar rotina apos edicao recusada", "id", id, "error", refreshErr)
		}
		return err
	}
	return nil
}

// matchCron checks if a five-field cron expression matches the given time.
func matchCron(expr string, t time.Time) bool {
	spec, err := parseSchedule(expr)
	return err == nil && spec.matches(t)
}

func matchCronField(field string, value int) bool {
	parsed, err := parseCronField(field, 0, 59, false)
	return err == nil && parsed.values[value]
}

// RegisterSkills registers scheduler management skills.
func RegisterSkills(reg *skill.Registry, sched *Scheduler, verificationStores ...*evolution.Store) {
	var verificationStore *evolution.Store
	if len(verificationStores) > 0 {
		verificationStore = verificationStores[0]
	}
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
				if !t.NextRunAt.IsZero() {
					fmt.Fprintf(&sb, "  Proxima execucao: %s | fuso: %s | perda de horario: %s\n", t.NextRunAt.Format(time.RFC3339), t.TimeZone, t.MisfirePolicy)
				}
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
		Description: "Adicionar rotina agendada; verificação SHA-256 opcional exige prompt e e registrada antes da primeira execução",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"name":                 {Type: jsonschema.String, Description: "Nome humano e curto da rotina"},
				"schedule":             {Type: jsonschema.String, Description: "Cron de cinco campos com listas/intervalos/passos, @every 30m ou @at 2026-11-06T09:00:00-03:00"},
				"time_zone":            {Type: jsonschema.String, Description: "Fuso IANA para cron, por exemplo America/Sao_Paulo; vazio usa o fuso da maquina"},
				"misfire_policy":       {Type: jsonschema.String, Description: "run_once executa uma vez apos parada; skip ignora horarios perdidos"},
				"prompt":               {Type: jsonschema.String, Description: "Objetivo completo para despertar o agente, incluindo contexto, verificacao de sucesso e o que fazer em caso de erro"},
				"command":              {Type: jsonschema.String, Description: "Comando shell exato; use somente para automacoes mecanicas que nao exigem raciocinio"},
				"max_retries":          {Type: jsonschema.Integer, Description: "Numero de novas tentativas em caso de falha (padrao 3, maximo 5)"},
				"channel":              {Type: jsonschema.String, Description: "Canal de origem da rotina"},
				"user_id":              {Type: jsonschema.String, Description: "Identidade dona da rotina"},
				"verification_path":    {Type: jsonschema.String, Description: "Arquivo absoluto cujo SHA-256 deve provar o resultado; opcional com os dois campos abaixo"},
				"verification_sha256":  {Type: jsonschema.String, Description: "SHA-256 esperado do arquivo ao concluir a rotina"},
				"verification_pattern": {Type: jsonschema.String, Description: "Categoria estavel para aprender com resultados verificados"},
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
				Name:          args["name"],
				Schedule:      args["schedule"],
				TimeZone:      args["time_zone"],
				MisfirePolicy: args["misfire_policy"],
				Command:       args["command"],
				Prompt:        args["prompt"],
				Channel:       args["channel"],
				UserID:        args["user_id"],
				MaxRetries:    maxRetries,
			}
			verificationPath := strings.TrimSpace(args["verification_path"])
			verificationSHA := strings.TrimSpace(args["verification_sha256"])
			verificationPattern := strings.TrimSpace(args["verification_pattern"])
			planned := verificationPath != "" || verificationSHA != "" || verificationPattern != ""
			if planned {
				if verificationPath == "" || verificationSHA == "" || verificationPattern == "" {
					return "", fmt.Errorf("verification_path, verification_sha256 e verification_pattern devem ser fornecidos juntos")
				}
				if strings.TrimSpace(task.Prompt) == "" {
					return "", fmt.Errorf("verificacao planejada exige rotina com prompt; command isolado ainda nao tem captura de estado antes da execucao")
				}
				if verificationStore == nil {
					return "", fmt.Errorf("verificacao de rotina indisponivel")
				}
				// Validate the routine before registering the immutable plan. The ID
				// is generated first so the plan exists before AddTask can run it.
				if err := validateScheduledTaskForPlan(task); err != nil {
					return "", err
				}
				for attempt := 0; attempt < 5; attempt++ {
					idBytes := make([]byte, 16)
					if _, err := rand.Read(idBytes); err != nil {
						return "", fmt.Errorf("gerando ID da rotina: %w", err)
					}
					task.ID = "task_" + hex.EncodeToString(idBytes)
					if _, exists := sched.GetTask(task.ID); !exists {
						break
					}
					task.ID = ""
				}
				if task.ID == "" {
					return "", fmt.Errorf("nao foi possivel gerar ID unico da rotina")
				}
				_, err := verificationStore.RegisterVerificationPlan(context.Background(), evolution.VerificationPlanInput{
					SourceKind: "routine", SubjectID: task.ID, PatternKey: verificationPattern,
					Kind: evolution.FileSHA256Check, Path: verificationPath, ExpectedSHA256: verificationSHA,
				})
				if err != nil {
					return "", fmt.Errorf("registrando verificacao da rotina: %w", err)
				}
			}
			if err := sched.AddTask(task); err != nil {
				return "", err
			}
			return fmt.Sprintf("Tarefa '%s' agendada com ID %s (cron: %s)", task.Name, task.ID, task.Schedule), nil
		},
	})

	reg.Register(&skill.Skill{
		Name:        "scheduler_update",
		Description: "Editar uma rotina futura sem perder seu historico",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id":             {Type: jsonschema.String, Description: "ID da rotina"},
				"name":           {Type: jsonschema.String, Description: "Novo nome"},
				"schedule":       {Type: jsonschema.String, Description: "Novo cron, @every ou @at"},
				"time_zone":      {Type: jsonschema.String, Description: "Novo fuso IANA"},
				"misfire_policy": {Type: jsonschema.String, Description: "run_once ou skip"},
				"prompt":         {Type: jsonschema.String, Description: "Novo objetivo do agente"},
				"command":        {Type: jsonschema.String, Description: "Novo comando"},
				"max_retries":    {Type: jsonschema.Integer, Description: "0 a 5"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			if err := sched.UpdateTask(args["id"], args); err != nil {
				return "", err
			}
			return fmt.Sprintf("Rotina %s atualizada.", args["id"]), nil
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
			history, err := sched.History(task.ID)
			if err != nil {
				return "", err
			}
			if len(history) == 0 {
				return "A rotina ainda nao possui execucoes registradas.", nil
			}
			var sb strings.Builder
			for _, run := range history {
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
		Description: "Solicitar a execução imediata de uma rotina; consulte scheduler_history para acompanhar o resultado",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"id": {Type: jsonschema.String, Description: "ID da tarefa"},
			},
			Required: []string{"id"},
		},
		Execute: func(args map[string]string) (string, error) {
			id := strings.TrimSpace(args["id"])
			task, ok := sched.GetTask(id)
			if !ok {
				return "", fmt.Errorf("tarefa %s nao encontrada", id)
			}
			// A scheduled agent may call this skill while its own turn holds the
			// background agent gate. RunNow is synchronous, so running it here
			// would deadlock when the target routine also wakes an agent.
			go func() {
				if _, err := sched.RunNow(id); err != nil {
					sched.logger.Warn("execucao imediata da rotina falhou", "id", id, "error", err)
				}
			}()
			return fmt.Sprintf("Execucao da rotina '%s' solicitada. Consulte scheduler_history para o resultado.", task.Name), nil
		},
	})
}

func validateScheduledTaskForPlan(task *Task) error {
	if strings.TrimSpace(task.Name) == "" {
		return fmt.Errorf("nome da rotina e obrigatorio")
	}
	if _, err := parseSchedule(task.Schedule); err != nil {
		return fmt.Errorf("agendamento invalido: %w", err)
	}
	if task.MisfirePolicy != "" && task.MisfirePolicy != "run_once" && task.MisfirePolicy != "skip" {
		return fmt.Errorf("misfire_policy deve ser run_once ou skip")
	}
	if strings.TrimSpace(task.Command) == "" && strings.TrimSpace(task.Prompt) == "" {
		return fmt.Errorf("a rotina precisa de prompt ou command")
	}
	next, err := nextOccurrence(task.Schedule, time.Now(), task.TimeZone)
	if err != nil {
		return err
	}
	if next.IsZero() {
		return fmt.Errorf("horario unico ja passou: %s", task.Schedule)
	}
	return nil
}
