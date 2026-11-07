// Package taskqueue runs one-off agent tasks from a durable priority queue.
package taskqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Status string

const (
	Pending   Status = "pending"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Canceled  Status = "canceled"
)

var ErrNotFound = errors.New("tarefa nao encontrada")

type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Prompt      string `json:"prompt"`
	Status      Status `json:"status"`
	Priority    int    `json:"priority"`
	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"max_attempts"`
	Channel     string `json:"channel,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Result      string `json:"result,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	NextRunAt   int64  `json:"next_run_at"`
}

type Attempt struct {
	TaskID     string `json:"task_id"`
	Number     int    `json:"number"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Overview struct {
	Tasks          map[Status]int `json:"tasks"`
	PendingNotices int            `json:"pending_notices"`
}

// Overview returns exact task counts and terminal notices awaiting the
// downstream outbox. It is suitable for heartbeat and diagnostics.
func (q *Queue) Overview(ctx context.Context) (Overview, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return Overview{}, err
	}
	counts := make(map[Status]int)
	for rows.Next() {
		var status Status
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return Overview{}, err
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Overview{}, err
	}
	if err := rows.Close(); err != nil {
		return Overview{}, err
	}
	pending, err := q.PendingNotices(ctx)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Tasks: counts, PendingNotices: pending}, nil
}

// Notice is a durable terminal result. Key is stable across notification
// retries, so a receiving outbox can deduplicate ambiguous acknowledgements.
type Notice struct {
	Key               string `json:"key"`
	TaskID            string `json:"task_id"`
	Attempt           int    `json:"attempt"`
	Status            Status `json:"status"`
	Result            string `json:"result,omitempty"`
	Error             string `json:"error,omitempty"`
	Channel           string `json:"channel,omitempty"`
	Owner             string `json:"owner,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	Attempts          int    `json:"delivery_attempts"`
	NextRunAt         int64  `json:"next_delivery_at"`
	LastDeliveryError string `json:"last_delivery_error,omitempty"`
}

type CreateInput struct {
	ID          string
	Title       string
	Prompt      string
	Priority    int
	MaxAttempts int
	Channel     string
	Owner       string
	NextRunAt   time.Time
}

// NewTaskID allows callers to register related durable state before a task is
// published to the queue. Queue.Create still generates an ID when omitted.
func NewTaskID() string { return "task_" + uuid.NewString() }

// Handler must execute the complete agent turn. Task.ID and Attempts remain
// stable for audit and application-level deduplication of external effects.
type Handler func(ctx context.Context, task Task) (string, error)

type Notifier func(ctx context.Context, notice Notice) error

// Queue processes one task at a time so priority ordering is deterministic.
// SQLite owns state; the mutex only guards the worker lifecycle and handler.
type Queue struct {
	db          *sql.DB
	mu          sync.Mutex
	handler     Handler
	notifier    Notifier
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	wake        chan struct{}
	poll        time.Duration
	taskTimeout time.Duration
	logger      *slog.Logger
}

const schema = `
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=FULL;
CREATE TABLE IF NOT EXISTS tasks (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, prompt TEXT NOT NULL, status TEXT NOT NULL,
 priority INTEGER NOT NULL, attempts INTEGER NOT NULL, max_attempts INTEGER NOT NULL,
 channel TEXT NOT NULL, owner TEXT NOT NULL, result TEXT NOT NULL, last_error TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, next_run_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_ready_idx ON tasks(status,next_run_at,priority,created_at);
CREATE TABLE IF NOT EXISTS task_attempts (
 task_id TEXT NOT NULL REFERENCES tasks(id), number INTEGER NOT NULL,
 started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL, status TEXT NOT NULL,
 result TEXT NOT NULL, error TEXT NOT NULL,
 PRIMARY KEY(task_id,number)
);
CREATE TABLE IF NOT EXISTS task_notices (
 key TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), attempt INTEGER NOT NULL,
 status TEXT NOT NULL, result TEXT NOT NULL, error TEXT NOT NULL, channel TEXT NOT NULL,
 owner TEXT NOT NULL, created_at INTEGER NOT NULL, delivery_state TEXT NOT NULL,
 delivery_attempts INTEGER NOT NULL, next_delivery_at INTEGER NOT NULL,
 delivered_at INTEGER NOT NULL, last_delivery_error TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS task_notices_ready_idx ON task_notices(delivery_state,next_delivery_at,created_at);
`

// Open initializes the private database and recovers a task interrupted by a
// process crash. A repeated external effect remains possible; the handler is
// given a stable task ID and attempt number to reconcile it.
func Open(path string) (*Queue, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("caminho da fila obrigatorio")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criando fila: %w", err)
	}
	q := &Queue{db: db, wake: make(chan struct{}, 1), poll: time.Second,
		taskTimeout: 30 * time.Minute, logger: slog.Default()}
	if err := q.recoverInterrupted(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("recuperando tarefas: %w", err)
	}
	return q, nil
}

func (q *Queue) SetHandler(handler Handler) {
	q.mu.Lock()
	q.handler = handler
	q.mu.Unlock()
}

// SetTaskTimeout bounds a cooperative agent turn. It does not forcibly stop
// tools that ignore context cancellation or undo external effects.
func (q *Queue) SetTaskTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	q.mu.Lock()
	q.taskTimeout = timeout
	q.mu.Unlock()
}

// SetNotifier connects terminal results to an idempotent downstream outbox.
// A nil notifier leaves notices pending without rerunning their tasks.
func (q *Queue) SetNotifier(notifier Notifier) {
	q.mu.Lock()
	q.notifier = notifier
	q.mu.Unlock()
	q.Wake()
}

func (q *Queue) SetLogger(logger *slog.Logger) {
	if logger == nil {
		return
	}
	q.mu.Lock()
	q.logger = logger
	q.mu.Unlock()
}

func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cancel != nil {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	q.cancel = cancel
	q.wg.Add(1)
	go q.loop(workerCtx)
}

func (q *Queue) Stop() {
	q.mu.Lock()
	cancel := q.cancel
	q.cancel = nil
	q.mu.Unlock()
	if cancel != nil {
		cancel()
		q.wg.Wait()
	}
}

func (q *Queue) Close() error {
	q.Stop()
	return q.db.Close()
}

func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) Create(ctx context.Context, in CreateInput) (Task, error) {
	if in.ID == "" {
		in.ID = NewTaskID()
	} else {
		parsed, err := uuid.Parse(strings.TrimPrefix(in.ID, "task_"))
		if err != nil || in.ID != "task_"+parsed.String() {
			return Task{}, errors.New("id da tarefa deve ser task_ seguido de UUID canônico")
		}
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Title = strings.TrimSpace(in.Title)
	if in.Prompt == "" {
		return Task{}, errors.New("prompt da tarefa obrigatorio")
	}
	if in.Title == "" {
		in.Title = "Tarefa do agente"
	}
	if in.Priority < -100 || in.Priority > 100 {
		return Task{}, errors.New("prioridade deve estar entre -100 e 100")
	}
	if in.MaxAttempts == 0 {
		in.MaxAttempts = 3
	}
	if in.MaxAttempts < 1 || in.MaxAttempts > 20 {
		return Task{}, errors.New("max_attempts deve estar entre 1 e 20")
	}
	now := time.Now().UTC()
	next := in.NextRunAt
	if next.IsZero() || next.Before(now) {
		next = now
	}
	t := Task{ID: in.ID, Title: in.Title, Prompt: in.Prompt, Status: Pending,
		Priority: in.Priority, MaxAttempts: in.MaxAttempts, Channel: in.Channel, Owner: in.Owner,
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(), NextRunAt: next.UnixMilli()}
	_, err := q.db.ExecContext(ctx, `INSERT INTO tasks(id,title,prompt,status,priority,attempts,max_attempts,channel,owner,result,last_error,created_at,updated_at,next_run_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Title, t.Prompt, t.Status, t.Priority, 0, t.MaxAttempts, t.Channel, t.Owner, "", "", t.CreatedAt, t.UpdatedAt, t.NextRunAt)
	if err != nil {
		return Task{}, fmt.Errorf("criando tarefa: %w", err)
	}
	q.Wake()
	return t, nil
}

func (q *Queue) Get(ctx context.Context, id string) (Task, error) {
	row := q.db.QueryRowContext(ctx, `SELECT id,title,prompt,status,priority,attempts,max_attempts,channel,owner,result,last_error,created_at,updated_at,next_run_at FROM tasks WHERE id=?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

func (q *Queue) List(ctx context.Context, status Status, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT id,title,prompt,status,priority,attempts,max_attempts,channel,owner,result,last_error,created_at,updated_at,next_run_at FROM tasks`
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = q.db.QueryContext(ctx, query+` ORDER BY created_at DESC,id DESC LIMIT ?`, limit)
	} else {
		rows, err = q.db.QueryContext(ctx, query+` WHERE status=? ORDER BY created_at DESC,id DESC LIMIT ?`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (q *Queue) History(ctx context.Context, id string) ([]Attempt, error) {
	if _, err := q.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `SELECT task_id,number,started_at,finished_at,status,result,error FROM task_attempts WHERE task_id=? ORDER BY number`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Attempt, 0)
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.TaskID, &a.Number, &a.StartedAt, &a.FinishedAt, &a.Status, &a.Result, &a.Error); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (q *Queue) Cancel(ctx context.Context, id string) error {
	result, err := q.db.ExecContext(ctx, `UPDATE tasks SET status='canceled',updated_at=? WHERE id=? AND status IN ('pending','failed')`, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("tarefa nao pode ser cancelada ou nao existe")
	}
	return nil
}

// Retry explicitly grants more attempts to a terminal task without deleting
// its previous history. It never silently retries an ambiguous running task.
func (q *Queue) Retry(ctx context.Context, id string, additionalAttempts int) error {
	if additionalAttempts <= 0 || additionalAttempts > 20 {
		return errors.New("additional_attempts deve estar entre 1 e 20")
	}
	now := time.Now().UnixMilli()
	result, err := q.db.ExecContext(ctx, `UPDATE tasks SET status='pending',max_attempts=attempts+?,last_error='',updated_at=?,next_run_at=? WHERE id=? AND status IN ('failed','canceled')`, additionalAttempts, now, now, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("tarefa nao pode ser retomada ou nao existe")
	}
	q.Wake()
	return nil
}

func (q *Queue) loop(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(q.poll)
	defer ticker.Stop()
	for {
		for {
			notified, err := q.ProcessNextNotice(ctx)
			if err != nil {
				q.logCycleError(ctx, err)
				break
			}
			processed, err := q.ProcessNext(ctx)
			if err != nil {
				q.logCycleError(ctx, err)
				break
			}
			if !processed && !notified {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-q.wake:
		}
	}
}

func (q *Queue) logCycleError(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	q.mu.Lock()
	logger := q.logger
	q.mu.Unlock()
	logger.Error("fila de tarefas: ciclo falhou", "error", err)
}

// ProcessNext executes one due task. It returns infrastructure errors; handler
// failures are durably stored as task attempts and scheduled for retry.
func (q *Queue) ProcessNext(ctx context.Context) (bool, error) {
	q.mu.Lock()
	handler := q.handler
	timeout := q.taskTimeout
	q.mu.Unlock()
	if handler == nil {
		return false, errors.New("executor de tarefas indisponivel")
	}
	t, ok, err := q.claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	var result string
	var runErr error
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	result, runErr = handler(runCtx, t)
	if runCtx.Err() != nil && runErr == nil {
		runErr = runCtx.Err()
	}
	cancel()
	if err := q.finish(t, result, runErr); err != nil {
		return true, err
	}
	return true, nil
}

func (q *Queue) claim(ctx context.Context) (Task, bool, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT id,title,prompt,status,priority,attempts,max_attempts,channel,owner,result,last_error,created_at,updated_at,next_run_at FROM tasks WHERE status='pending' AND next_run_at<=? ORDER BY priority DESC,next_run_at ASC,created_at ASC,id ASC LIMIT 1`, time.Now().UnixMilli())
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	if err != nil {
		return Task{}, false, err
	}
	t.Attempts++
	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE tasks SET status='running',attempts=?,updated_at=? WHERE id=? AND status='pending'`, t.Attempts, now, t.ID)
	if err != nil {
		return Task{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return Task{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_attempts(task_id,number,started_at,finished_at,status,result,error) VALUES(?,?,?,?,?,?,?)`, t.ID, t.Attempts, now, 0, "running", "", ""); err != nil {
		return Task{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, false, err
	}
	t.Status, t.UpdatedAt = Running, now
	return t, true, nil
}

func (q *Queue) finish(t Task, result string, runErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	result = bounded(result, 64<<10)
	status := Succeeded
	lastError := ""
	next := t.NextRunAt
	if runErr != nil {
		lastError = bounded(runErr.Error(), 4<<10)
		if t.Attempts < t.MaxAttempts {
			status = Pending
			backoff := time.Duration(1<<min(t.Attempts, 8)) * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			next = time.Now().Add(backoff).UnixMilli()
		} else {
			status = Failed
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE task_attempts SET finished_at=?,status=?,result=?,error=? WHERE task_id=? AND number=?`, now, statusForAttempt(runErr), result, lastError, t.ID, t.Attempts)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errors.New("tentativa nao encontrada para concluir")
	}
	res, err = tx.ExecContext(ctx, `UPDATE tasks SET status=?,result=?,last_error=?,updated_at=?,next_run_at=? WHERE id=? AND status='running' AND attempts=?`, status, result, lastError, now, next, t.ID, t.Attempts)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return errors.New("tarefa nao encontrada para concluir")
	}
	if status == Succeeded || status == Failed {
		key := fmt.Sprintf("%s:terminal:%d", t.ID, t.Attempts)
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_notices(key,task_id,attempt,status,result,error,channel,owner,created_at,delivery_state,delivery_attempts,next_delivery_at,delivered_at,last_delivery_error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			key, t.ID, t.Attempts, status, result, lastError, t.Channel, t.Owner, now, "pending", 0, now, 0, ""); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	q.Wake()
	return nil
}

func (q *Queue) recoverInterrupted(ctx context.Context) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE task_attempts SET finished_at=?,status='interrupted',error='processo interrompido; efeito externo desconhecido' WHERE status='running'`, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_notices(key,task_id,attempt,status,result,error,channel,owner,created_at,delivery_state,delivery_attempts,next_delivery_at,delivered_at,last_delivery_error)
	 SELECT id || ':terminal:' || attempts,id,attempts,'failed','',
	 'processo interrompido; efeito externo desconhecido',channel,owner,?,'pending',0,?,0,''
	 FROM tasks WHERE status='running' AND attempts>=max_attempts`, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status=CASE WHEN attempts<max_attempts THEN 'pending' ELSE 'failed' END,last_error='processo interrompido; efeito externo desconhecido',updated_at=?,next_run_at=? WHERE status='running'`, now, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_notices SET delivery_state='pending',next_delivery_at=? WHERE delivery_state='delivering'`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func statusForAttempt(err error) string {
	if err != nil {
		return "failed"
	}
	return "succeeded"
}

func bounded(text string, size int) string {
	if len(text) <= size {
		return text
	}
	return strings.ToValidUTF8(text[:size], "")
}

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Title, &t.Prompt, &t.Status, &t.Priority, &t.Attempts, &t.MaxAttempts,
		&t.Channel, &t.Owner, &t.Result, &t.LastError, &t.CreatedAt, &t.UpdatedAt, &t.NextRunAt)
	return t, err
}
