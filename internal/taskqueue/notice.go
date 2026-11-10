package taskqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	deliveryCtx, cancel := context.WithCancel(ctx)
	leaseDone := make(chan error, 1)
	go func() { leaseDone <- q.keepNoticeLease(deliveryCtx, notice, cancel) }()
	deliveryErr := notifier(deliveryCtx, notice)
	if deliveryCtx.Err() != nil && deliveryErr == nil {
		deliveryErr = deliveryCtx.Err()
	}
	cancel()
	if err := <-leaseDone; err != nil {
		return true, err
	}
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
	now := time.Now().UnixMilli()
	token := q.workerID + ":" + uuid.NewString()
	row := tx.QueryRowContext(ctx, `UPDATE task_notices SET delivery_state='delivering',delivery_attempts=delivery_attempts+1,
	 claim_token=?,lease_until=?
	 WHERE key=(SELECT key FROM task_notices WHERE delivery_state='pending' AND next_delivery_at<=?
	 ORDER BY created_at ASC,key ASC LIMIT 1) AND delivery_state='pending'
	 RETURNING key,task_id,attempt,status,result,error,channel,owner,created_at,delivery_attempts,next_delivery_at,last_delivery_error`,
		token, now+leaseDuration.Milliseconds(), now)
	notice, err := scanNotice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notice{}, false, nil
	}
	if err != nil {
		return Notice{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Notice{}, false, err
	}
	notice.claimToken = token
	return notice, true, nil
}

func (q *Queue) keepNoticeLease(ctx context.Context, notice Notice, cancel context.CancelFunc) error {
	ticker := time.NewTicker(leaseRenewal)
	defer ticker.Stop()
	leaseUntil := time.Now().Add(leaseDuration)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		now := time.Now().UnixMilli()
		res, err := q.db.ExecContext(ctx, `UPDATE task_notices SET lease_until=? WHERE key=? AND delivery_state='delivering' AND delivery_attempts=? AND claim_token=?`,
			now+leaseDuration.Milliseconds(), notice.Key, notice.Attempts, notice.claimToken)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if time.Now().After(leaseUntil) {
				cancel()
				return fmt.Errorf("renovando posse da notificacao %s: %w", notice.Key, err)
			}
			continue
		}
		n, err := res.RowsAffected()
		if err != nil || n != 1 {
			cancel()
			return ErrLeaseLost
		}
		leaseUntil = time.UnixMilli(now).Add(leaseDuration)
	}
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
	res, err := q.db.ExecContext(ctx, `UPDATE task_notices SET delivery_state=?,next_delivery_at=?,delivered_at=?,last_delivery_error=?,claim_token='',lease_until=0 WHERE key=? AND delivery_state='delivering' AND delivery_attempts=? AND claim_token=?`,
		state, next, deliveredAt, lastError, notice.Key, notice.Attempts, notice.claimToken)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("notificacao %s: %w", notice.Key, ErrLeaseLost)
	}
	return nil
}

func scanNotice(row rowScanner) (Notice, error) {
	var n Notice
	err := row.Scan(&n.Key, &n.TaskID, &n.Attempt, &n.Status, &n.Result, &n.Error,
		&n.Channel, &n.Owner, &n.CreatedAt, &n.Attempts, &n.NextRunAt, &n.LastDeliveryError)
	return n, err
}
