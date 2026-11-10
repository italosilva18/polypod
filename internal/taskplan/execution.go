package taskplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/taskqueue"
	"github.com/google/uuid"
)

var (
	ErrExecutionReserved = errors.New("execução do plano já reservada; o plano não pode ser alterado")
	ErrExecutionNotFound = errors.New("execução do plano não encontrada")
	ErrExecutionConflict = errors.New("execução da fila diverge da reserva do plano")
)

// Execution links a plan item to one durable queue task. The queue owns the
// actual agent turn and its retry history; this snapshot is for plan readers.
type Execution struct {
	QueueID        string    `json:"queue_id"`
	Title          string    `json:"title"`
	Prompt         string    `json:"prompt"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	QueueUpdatedAt int64     `json:"-"`
	Result         string    `json:"result,omitempty"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func queueIDForPlan(id string) (string, error) {
	if !strings.HasPrefix(id, "plan_") {
		return "", fmt.Errorf("%w: ID de plano inválido", ErrInvalidInput)
	}
	parsed, err := uuid.Parse(strings.TrimPrefix(id, "plan_"))
	if err != nil || id != "plan_"+parsed.String() {
		return "", fmt.Errorf("%w: ID de plano inválido", ErrInvalidInput)
	}
	return "task_" + parsed.String(), nil
}

func readExecution(ctx context.Context, q taskReader, id string) (*Execution, error) {
	var execution Execution
	var createdAt, updatedAt int64
	err := q.QueryRowContext(ctx, `SELECT queue_id,title,prompt,status,attempts,queue_updated_at,result,error,created_at,updated_at
 FROM plan_runs WHERE plan_id=?`, id).Scan(&execution.QueueID, &execution.Title, &execution.Prompt,
		&execution.Status, &execution.Attempts, &execution.QueueUpdatedAt, &execution.Result, &execution.Error, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	execution.CreatedAt = time.UnixMilli(createdAt).UTC()
	execution.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return &execution, nil
}

// ReserveExecution records the user's decision to run a plan before publishing
// a queue task. Its deterministic queue ID closes the crash window between the
// two SQLite databases: a retry can find or create exactly that queue task.
func (s *Store) ReserveExecution(ctx context.Context, id, owner string) (Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, fmt.Errorf("%w: owner é obrigatório", ErrInvalidInput)
	}
	queueID, err := queueIDForPlan(id)
	if err != nil {
		return Task{}, err
	}
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Owner != "" && task.Owner != owner {
		return Task{}, ErrClaimed
	}
	if task.Execution != nil {
		if task.Execution.QueueID != queueID {
			return Task{}, ErrExecutionConflict
		}
		if err := tx.Commit(); err != nil {
			return Task{}, err
		}
		return task, nil
	}
	if task.Status == Completed {
		return Task{}, ErrCompleted
	}
	blocked, err := unresolvedDependencies(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if len(blocked) != 0 {
		return Task{}, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(blocked, ", "))
	}
	prompt := strings.TrimSpace(task.Description)
	if prompt == "" {
		prompt = task.Subject
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE plan_tasks SET owner=?,status=?,updated_at=? WHERE id=?`,
		owner, InProgress, now, id); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plan_runs
 (plan_id,queue_id,title,prompt,status,attempts,result,error,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)`, id, queueID, task.Subject, prompt, "dispatching", 0, "", "", now, now); err != nil {
		return Task{}, err
	}
	task, err = readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// SyncExecution copies an observed queue state into the plan. It is safe to
// repeat after an ambiguous notification acknowledgement or process restart.
func (s *Store) SyncExecution(ctx context.Context, queued taskqueue.Task) (Task, error) {
	switch queued.Status {
	case taskqueue.Pending, taskqueue.Running, taskqueue.Succeeded, taskqueue.Failed, taskqueue.Canceled:
	default:
		return Task{}, fmt.Errorf("%w: estado da fila desconhecido", ErrInvalidInput)
	}
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT plan_id FROM plan_runs WHERE queue_id=?`, queued.ID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrExecutionNotFound
		}
		return Task{}, err
	}
	task, err := readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Execution == nil || task.Execution.QueueID != queued.ID || queued.Channel != "plan" ||
		queued.Owner != task.Owner || queued.Title != task.Execution.Title || queued.Prompt != task.Execution.Prompt {
		return Task{}, ErrExecutionConflict
	}
	// Notifications and queue creation may carry an earlier snapshot than a
	// concurrent worker. Never move a completed plan backwards.
	if queued.UpdatedAt < task.Execution.QueueUpdatedAt ||
		(task.Status == Completed && queued.Status != taskqueue.Succeeded) {
		if err := tx.Commit(); err != nil {
			return Task{}, err
		}
		return task, nil
	}
	if task.Execution.Status == string(queued.Status) && task.Execution.Attempts == queued.Attempts &&
		task.Execution.Result == queued.Result && task.Execution.Error == queued.LastError &&
		task.Execution.QueueUpdatedAt == queued.UpdatedAt &&
		(queued.Status != taskqueue.Succeeded || task.Status == Completed) {
		if err := tx.Commit(); err != nil {
			return Task{}, err
		}
		return task, nil
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE plan_runs SET status=?,attempts=?,queue_updated_at=?,result=?,error=?,updated_at=? WHERE plan_id=?`,
		queued.Status, queued.Attempts, queued.UpdatedAt, queued.Result, queued.LastError, now, id); err != nil {
		return Task{}, err
	}
	if queued.Status == taskqueue.Succeeded && task.Status != Completed {
		if _, err := tx.ExecContext(ctx, `UPDATE plan_tasks SET status=?,updated_at=? WHERE id=?`, Completed, now, id); err != nil {
			return Task{}, err
		}
	}
	task, err = readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

// PublishExecution creates the reserved queue task or observes the one already
// created by another process. It never publishes a second task for a plan.
func (s *Store) PublishExecution(ctx context.Context, queue *taskqueue.Queue, planned Task) (Task, error) {
	if queue == nil || planned.Execution == nil {
		return Task{}, fmt.Errorf("%w: fila ou reserva indisponível", ErrInvalidInput)
	}
	queued, err := queue.Get(ctx, planned.Execution.QueueID)
	if errors.Is(err, taskqueue.ErrNotFound) {
		if planned.Execution.Status != "dispatching" {
			return Task{}, ErrExecutionConflict
		}
		queued, err = queue.Create(ctx, taskqueue.CreateInput{
			ID: planned.Execution.QueueID, Title: planned.Execution.Title, Prompt: planned.Execution.Prompt,
			Channel: "plan", Owner: planned.Owner, MaxAttempts: 3,
		})
		if err != nil {
			createErr := err
			// A concurrent process may have published the same deterministic ID.
			queued, err = queue.Get(ctx, planned.Execution.QueueID)
			if err != nil {
				return Task{}, fmt.Errorf("reserva mantida para nova tentativa: %w", errors.Join(createErr, err))
			}
		}
	}
	if err != nil {
		return Task{}, fmt.Errorf("reserva mantida para nova tentativa: %w", err)
	}
	return s.SyncExecution(ctx, queued)
}

// ReconcileExecutions heals the crash window between reserving a plan and
// writing its queue task. It also imports terminal outcomes after a restart.
// The caller should run it before starting the queue worker.
func (s *Store) ReconcileExecutions(ctx context.Context, queue *taskqueue.Queue) error {
	if queue == nil {
		return fmt.Errorf("%w: fila indisponível", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT plan_id FROM plan_runs ORDER BY created_at,plan_id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		planned, err := s.Get(ctx, id)
		if err == nil {
			_, err = s.PublishExecution(ctx, queue, planned)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("plano %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}
