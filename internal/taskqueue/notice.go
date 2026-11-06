package taskqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProcessNextNotice moves one terminal result to the downstream notifier.
// A failed or ambiguous delivery schedules only the notice for retry; the
// agent task is never executed again because of a notification failure.
func (q *Queue) ProcessNextNotice(ctx context.Context) (bool, error) {
	q.mu.Lock()
	notifier := q.notifier
	q.mu.Unlock()
	if notifier == nil {
		return false, nil
	}
	notice, ok, err := q.claimNotice(ctx)
	if err != nil || !ok {
		return false, err
	}
	deliveryErr := notifier(ctx, notice)
	if err := q.finishNotice(notice, deliveryErr); err != nil {
		return true, err
	}
	if deliveryErr != nil && ctx.Err() == nil {
		q.mu.Lock()
		logger := q.logger
		q.mu.Unlock()
		logger.Warn("notificacao de tarefa sera repetida", "key", notice.Key, "error", deliveryErr)
	}
	return true, nil
}

// PendingNotices counts terminal results not yet committed by the notifier.
func (q *Queue) PendingNotices(ctx context.Context) (int, error) {
	var count int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_notices WHERE delivery_state!='delivered'`).Scan(&count)
	return count, err
}

func (q *Queue) claimNotice(ctx context.Context) (Notice, bool, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Notice{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT key,task_id,attempt,status,result,error,channel,owner,created_at,delivery_attempts,next_delivery_at,last_delivery_error FROM task_notices WHERE delivery_state='pending' AND next_delivery_at<=? ORDER BY created_at ASC,key ASC LIMIT 1`, time.Now().UnixMilli())
	notice, err := scanNotice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notice{}, false, nil
	}
	if err != nil {
		return Notice{}, false, err
	}
	notice.Attempts++
	res, err := tx.ExecContext(ctx, `UPDATE task_notices SET delivery_state='delivering',delivery_attempts=? WHERE key=? AND delivery_state='pending'`, notice.Attempts, notice.Key)
	if err != nil {
		return Notice{}, false, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Notice{}, false, err
	} else if n != 1 {
		return Notice{}, false, errors.New("notificacao mudou durante claim")
	}
	if err := tx.Commit(); err != nil {
		return Notice{}, false, err
	}
	return notice, true, nil
}

func (q *Queue) finishNotice(notice Notice, deliveryErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().UnixMilli()
	state := "delivered"
	next := notice.NextRunAt
	deliveredAt := now
	lastError := ""
	if deliveryErr != nil {
		state = "pending"
		deliveredAt = 0
		backoff := time.Duration(1<<min(notice.Attempts, 8)) * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		next = time.Now().Add(backoff).UnixMilli()
		lastError = bounded(deliveryErr.Error(), 4<<10)
	}
	res, err := q.db.ExecContext(ctx, `UPDATE task_notices SET delivery_state=?,next_delivery_at=?,delivered_at=?,last_delivery_error=? WHERE key=? AND delivery_state='delivering' AND delivery_attempts=?`,
		state, next, deliveredAt, lastError, notice.Key, notice.Attempts)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("notificacao %s nao encontrada para concluir", notice.Key)
	}
	return nil
}

func scanNotice(row rowScanner) (Notice, error) {
	var n Notice
	err := row.Scan(&n.Key, &n.TaskID, &n.Attempt, &n.Status, &n.Result, &n.Error,
		&n.Channel, &n.Owner, &n.CreatedAt, &n.Attempts, &n.NextRunAt, &n.LastDeliveryError)
	return n, err
}
