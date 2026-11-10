// Package taskplan stores planned work separately from running background tasks.
package taskplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Status string

const (
	Pending    Status = "pending"
	InProgress Status = "in_progress"
	Completed  Status = "completed"
)

var (
	ErrNotFound           = errors.New("tarefa planejada não encontrada")
	ErrInvalidInput       = errors.New("dados da tarefa planejada inválidos")
	ErrDependencyNotFound = errors.New("dependência não encontrada")
	ErrCycle              = errors.New("dependência criaria um ciclo")
	ErrBlocked            = errors.New("tarefa bloqueada por dependências abertas")
	ErrClaimed            = errors.New("tarefa atribuída a outro agente")
	ErrCompleted          = errors.New("tarefa já concluída")
)

// Task is a durable plan item. It does not start a process or an agent.
type Task struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	Owner       string     `json:"owner,omitempty"`
	BlockedBy   []string   `json:"blocked_by"`
	Blocks      []string   `json:"blocks"`
	Execution   *Execution `json:"execution,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CreateInput struct {
	Subject     string
	Description string
	Owner       string
	BlockedBy   []string
}

// UpdateInput uses pointers so empty strings and an empty dependency list can
// explicitly clear an owner, description, or set of dependencies.
type UpdateInput struct {
	Subject     *string
	Description *string
	Status      *Status
	Owner       *string
	BlockedBy   *[]string
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS plan_tasks (
 id TEXT PRIMARY KEY,
 subject TEXT NOT NULL,
 description TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('pending','in_progress','completed')),
 owner TEXT,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS plan_dependencies (
 task_id TEXT NOT NULL REFERENCES plan_tasks(id) ON DELETE CASCADE,
 depends_on TEXT NOT NULL REFERENCES plan_tasks(id) ON DELETE RESTRICT,
 PRIMARY KEY(task_id,depends_on),
 CHECK(task_id <> depends_on)
);
CREATE INDEX IF NOT EXISTS plan_dependencies_reverse_idx ON plan_dependencies(depends_on,task_id);
CREATE TABLE IF NOT EXISTS plan_runs (
 plan_id TEXT PRIMARY KEY REFERENCES plan_tasks(id) ON DELETE CASCADE,
 queue_id TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL,
 prompt TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('dispatching','pending','running','succeeded','failed','canceled')),
 attempts INTEGER NOT NULL DEFAULT 0,
 queue_updated_at INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS plan_runs_queue_idx ON plan_runs(queue_id);
CREATE TABLE IF NOT EXISTS plan_write_lock (id INTEGER PRIMARY KEY CHECK(id=1), version INTEGER NOT NULL);
INSERT INTO plan_write_lock(id,version) VALUES(1,0) ON CONFLICT(id) DO NOTHING;
`

// Open creates a private SQLite store below dataDir. A dedicated 0700 directory
// also protects SQLite's WAL and SHM files when the service umask is permissive.
// A write-lock row serializes validation and mutation across CLI processes.
func Open(dataDir string) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("%w: diretório de dados obrigatório", ErrInvalidInput)
	}
	path, err := prepareDatabaseFile(dataDir)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configurando banco do plano: %w", err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criando tabelas do plano: %w", err)
	}
	if err := secureAuxiliaryFiles(dataDir); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func validStatus(status Status) bool {
	return status == Pending || status == InProgress || status == Completed
}

func (s *Store) beginWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE plan_write_lock SET version=version+1 WHERE id=1"); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) Create(ctx context.Context, in CreateInput) (Task, error) {
	in.Subject = strings.TrimSpace(in.Subject)
	in.Owner = strings.TrimSpace(in.Owner)
	if in.Subject == "" {
		return Task{}, fmt.Errorf("%w: subject é obrigatório", ErrInvalidInput)
	}
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	id := "plan_" + uuid.NewString()
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO plan_tasks(id,subject,description,status,owner,created_at,updated_at)
 VALUES(?,?,?,?,?,?,?)`, id, in.Subject, in.Description, Pending, nullableOwner(in.Owner), now, now); err != nil {
		return Task{}, err
	}
	if err := replaceDependencies(ctx, tx, id, in.BlockedBy); err != nil {
		return Task{}, err
	}
	task, err := readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) Update(ctx context.Context, id string, in UpdateInput) (Task, error) {
	if in.Subject == nil && in.Description == nil && in.Status == nil && in.Owner == nil && in.BlockedBy == nil {
		return Task{}, fmt.Errorf("%w: nenhuma alteração informada", ErrInvalidInput)
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
	if task.Execution != nil {
		return Task{}, ErrExecutionReserved
	}
	previousStatus, previousOwner := task.Status, task.Owner
	if in.Subject != nil {
		task.Subject = strings.TrimSpace(*in.Subject)
		if task.Subject == "" {
			return Task{}, fmt.Errorf("%w: subject é obrigatório", ErrInvalidInput)
		}
	}
	if in.Description != nil {
		task.Description = *in.Description
	}
	if in.Status != nil {
		if !validStatus(*in.Status) {
			return Task{}, fmt.Errorf("%w: status desconhecido", ErrInvalidInput)
		}
		task.Status = *in.Status
	}
	if in.Owner != nil {
		task.Owner = strings.TrimSpace(*in.Owner)
	}
	if previousStatus == Completed && task.Status != Completed {
		return Task{}, ErrCompleted
	}
	if previousStatus == InProgress && previousOwner != task.Owner && task.Status != Pending {
		return Task{}, fmt.Errorf("%w: devolva a tarefa a pending antes de trocar o owner", ErrClaimed)
	}
	if in.BlockedBy != nil {
		if err := replaceDependencies(ctx, tx, id, *in.BlockedBy); err != nil {
			return Task{}, err
		}
	}
	if task.Status == InProgress && task.Owner == "" {
		return Task{}, fmt.Errorf("%w: tarefa em andamento precisa de owner", ErrInvalidInput)
	}
	if task.Status != Pending {
		blocked, err := unresolvedDependencies(ctx, tx, id)
		if err != nil {
			return Task{}, err
		}
		if len(blocked) > 0 {
			return Task{}, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(blocked, ", "))
		}
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE plan_tasks SET subject=?,description=?,status=?,owner=?,updated_at=? WHERE id=?`,
		task.Subject, task.Description, task.Status, nullableOwner(task.Owner), now, id); err != nil {
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

// Claim atomically checks ownership and blockers, then marks the plan item in
// progress. One owner may hold multiple plan items. It does not run an agent.
func (s *Store) Claim(ctx context.Context, id, owner string) (Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, fmt.Errorf("%w: owner é obrigatório", ErrInvalidInput)
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
	if task.Status == Completed {
		return Task{}, ErrCompleted
	}
	if task.Owner != "" && task.Owner != owner {
		return Task{}, ErrClaimed
	}
	blocked, err := unresolvedDependencies(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if len(blocked) > 0 {
		return Task{}, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(blocked, ", "))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plan_tasks SET owner=?,status=?,updated_at=? WHERE id=?`,
		owner, InProgress, time.Now().UTC().UnixMilli(), id); err != nil {
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

func (s *Store) Get(ctx context.Context, id string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := readTask(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) List(ctx context.Context, status Status, limit, offset int) ([]Task, error) {
	if status != "" && !validStatus(status) {
		return nil, fmt.Errorf("%w: status desconhecido", ErrInvalidInput)
	}
	if offset < 0 {
		return nil, fmt.Errorf("%w: offset precisa ser positivo", ErrInvalidInput)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := "SELECT id FROM plan_tasks"
	args := make([]any, 0, 3)
	if status != "" {
		query += " WHERE status=?"
		args = append(args, status)
	}
	query += " ORDER BY created_at ASC,id ASC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	tasks := make([]Task, 0, len(ids))
	for _, id := range ids {
		task, err := readTask(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tasks, nil
}

type taskReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readTask(ctx context.Context, q taskReader, id string) (Task, error) {
	var task Task
	var owner sql.NullString
	var createdAt, updatedAt int64
	err := q.QueryRowContext(ctx, `SELECT id,subject,description,status,owner,created_at,updated_at FROM plan_tasks WHERE id=?`, id).
		Scan(&task.ID, &task.Subject, &task.Description, &task.Status, &owner, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	task.Owner = owner.String
	task.CreatedAt = time.UnixMilli(createdAt).UTC()
	task.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	if task.BlockedBy, err = relationIDs(ctx, q, `SELECT depends_on FROM plan_dependencies WHERE task_id=? ORDER BY depends_on`, id); err != nil {
		return Task{}, err
	}
	if task.Blocks, err = relationIDs(ctx, q, `SELECT task_id FROM plan_dependencies WHERE depends_on=? ORDER BY task_id`, id); err != nil {
		return Task{}, err
	}
	if task.Execution, err = readExecution(ctx, q, id); err != nil {
		return Task{}, err
	}
	return task, nil
}

func relationIDs(ctx context.Context, q taskReader, query, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var related string
		if err := rows.Scan(&related); err != nil {
			return nil, err
		}
		ids = append(ids, related)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func replaceDependencies(ctx context.Context, tx *sql.Tx, id string, dependencies []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM plan_dependencies WHERE task_id=?", id); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(dependencies))
	for _, raw := range dependencies {
		dependency := strings.TrimSpace(raw)
		if dependency == "" || dependency == id {
			return fmt.Errorf("%w: dependência vazia ou da própria tarefa", ErrInvalidInput)
		}
		if _, duplicate := seen[dependency]; duplicate {
			return fmt.Errorf("%w: dependência repetida %s", ErrInvalidInput, dependency)
		}
		seen[dependency] = struct{}{}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM plan_tasks WHERE id=?)", dependency).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: %s", ErrDependencyNotFound, dependency)
		}
		var cycle bool
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (
 SELECT depends_on FROM plan_dependencies WHERE task_id=?
 UNION
 SELECT d.depends_on FROM plan_dependencies d JOIN reachable r ON d.task_id=r.id
 ) SELECT EXISTS(SELECT 1 FROM reachable WHERE id=?)`, dependency, id).Scan(&cycle); err != nil {
			return err
		}
		if cycle {
			return fmt.Errorf("%w: %s", ErrCycle, dependency)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO plan_dependencies(task_id,depends_on) VALUES(?,?)", id, dependency); err != nil {
			return err
		}
	}
	return nil
}

func unresolvedDependencies(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
	return relationIDs(ctx, tx, `SELECT d.depends_on FROM plan_dependencies d
 JOIN plan_tasks blocker ON blocker.id=d.depends_on
 WHERE d.task_id=? AND blocker.status<>'completed' ORDER BY d.depends_on`, id)
}

func nullableOwner(owner string) any {
	if owner == "" {
		return nil
	}
	return owner
}
