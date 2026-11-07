package taskqueue

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPriorityOrderHistoryAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	low, err := q.Create(ctx, CreateInput{Title: "low", Prompt: "task low", Priority: -5})
	if err != nil {
		t.Fatal(err)
	}
	high, err := q.Create(ctx, CreateInput{Title: "high", Prompt: "task high", Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	q.SetHandler(func(_ context.Context, task Task) (string, error) {
		order = append(order, task.ID)
		return "done", nil
	})
	for range 2 {
		processed, err := q.ProcessNext(ctx)
		if err != nil || !processed {
			t.Fatalf("process: processed=%v, err=%v", processed, err)
		}
	}
	if !reflect.DeepEqual(order, []string{high.ID, low.ID}) {
		t.Fatalf("priority order = %v", order)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	task, err := reopened.Get(ctx, high.ID)
	if err != nil || task.Status != Succeeded || task.Result != "done" || task.Attempts != 1 {
		t.Fatalf("reopened task=%+v, err=%v", task, err)
	}
	history, err := reopened.History(ctx, high.ID)
	if err != nil || len(history) != 1 || history[0].Status != "succeeded" {
		t.Fatalf("history=%+v, err=%v", history, err)
	}
}

func TestCreateAcceptsPreallocatedCanonicalTaskID(t *testing.T) {
	q, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	id := NewTaskID()
	task, err := q.Create(ctx, CreateInput{ID: id, Prompt: "write verified report"})
	if err != nil || task.ID != id {
		t.Fatalf("preallocated task ID lost: %+v err=%v", task, err)
	}
	if _, err := q.Create(ctx, CreateInput{ID: "task_invalid", Prompt: "bad id"}); err == nil {
		t.Fatal("noncanonical supplied ID was accepted")
	}
	if _, err := q.Create(ctx, CreateInput{ID: id, Prompt: "duplicate"}); err == nil {
		t.Fatal("duplicate supplied ID was accepted")
	}
}

func TestCrashRecoveryKeepsAttemptAndExplicitRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := q.Create(ctx, CreateInput{Prompt: "recover me", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.claim(ctx)
	if err != nil || !ok || claimed.Attempts != 1 {
		t.Fatalf("claim=%+v, ok=%v, err=%v", claimed, ok, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	state, err := recovered.Get(ctx, task.ID)
	if err != nil || state.Status != Pending || state.Attempts != 1 {
		t.Fatalf("recovered task=%+v, err=%v", state, err)
	}
	history, err := recovered.History(ctx, task.ID)
	if err != nil || len(history) != 1 || history[0].Status != "interrupted" {
		t.Fatalf("interrupted history=%+v, err=%v", history, err)
	}
	recovered.SetHandler(func(context.Context, Task) (string, error) { return "", errors.New("temporary failure") })
	processed, err := recovered.ProcessNext(ctx)
	if err != nil || !processed {
		t.Fatalf("recovered attempt: processed=%v, err=%v", processed, err)
	}
	state, err = recovered.Get(ctx, task.ID)
	if err != nil || state.Status != Failed || state.Attempts != 2 {
		t.Fatalf("terminal task=%+v, err=%v", state, err)
	}
	if err := recovered.Retry(ctx, task.ID, 1); err != nil {
		t.Fatal(err)
	}
	recovered.SetHandler(func(context.Context, Task) (string, error) { return "fixed", nil })
	processed, err = recovered.ProcessNext(ctx)
	if err != nil || !processed {
		t.Fatalf("manual retry: processed=%v, err=%v", processed, err)
	}
	state, err = recovered.Get(ctx, task.ID)
	if err != nil || state.Status != Succeeded || state.Attempts != 3 || state.Result != "fixed" {
		t.Fatalf("retried task=%+v, err=%v", state, err)
	}
	history, err = recovered.History(ctx, task.ID)
	if err != nil || len(history) != 3 || history[2].Status != "succeeded" {
		t.Fatalf("history after retry=%+v, err=%v", history, err)
	}
}

func TestMissingHandlerDoesNotSpendAttempt(t *testing.T) {
	q, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := context.Background()
	task, err := q.Create(ctx, CreateInput{Prompt: "wait for handler"})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := q.ProcessNext(ctx); err == nil || processed {
		t.Fatalf("missing handler should preserve task: processed=%v, err=%v", processed, err)
	}
	state, err := q.Get(ctx, task.ID)
	if err != nil || state.Status != Pending || state.Attempts != 0 {
		t.Fatalf("task changed without handler: %+v, err=%v", state, err)
	}
}

func TestNotificationFailureRetriesOnlyNoticeWithStableKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := q.Create(ctx, CreateInput{Prompt: "complete once", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	q.SetHandler(func(context.Context, Task) (string, error) { runs++; return "finished", nil })
	if processed, err := q.ProcessNext(ctx); err != nil || !processed {
		t.Fatalf("run=%v, err=%v", processed, err)
	}
	var keys []string
	q.SetNotifier(func(_ context.Context, notice Notice) error {
		keys = append(keys, notice.Key)
		return errors.New("downstream temporarily unavailable")
	})
	if processed, err := q.ProcessNextNotice(ctx); err != nil || !processed {
		t.Fatalf("first notice=%v, err=%v", processed, err)
	}
	count, err := q.PendingNotices(ctx)
	if err != nil || count != 1 {
		t.Fatalf("pending notices=%d, err=%v", count, err)
	}
	state, err := q.Get(ctx, task.ID)
	if err != nil || state.Status != Succeeded || state.Attempts != 1 || runs != 1 {
		t.Fatalf("notice failure re-ran task: %+v, runs=%d, err=%v", state, runs, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.db.Exec(`UPDATE task_notices SET next_delivery_at=0 WHERE delivery_state='pending'`); err != nil {
		t.Fatal(err)
	}
	reopened.SetHandler(func(context.Context, Task) (string, error) { runs++; return "duplicate", nil })
	reopened.SetNotifier(func(_ context.Context, notice Notice) error {
		keys = append(keys, notice.Key)
		return nil
	})
	if processed, err := reopened.ProcessNextNotice(ctx); err != nil || !processed {
		t.Fatalf("retried notice=%v, err=%v", processed, err)
	}
	if count, err := reopened.PendingNotices(ctx); err != nil || count != 0 {
		t.Fatalf("pending notices after delivery=%d, err=%v", count, err)
	}
	if processed, err := reopened.ProcessNext(ctx); err != nil || processed || runs != 1 {
		t.Fatalf("task re-executed on notice retry: processed=%v, runs=%d, err=%v", processed, runs, err)
	}
	if len(keys) != 2 || keys[0] != keys[1] || keys[0] != task.ID+":terminal:1" {
		t.Fatalf("notification key changed: %v", keys)
	}
}

func TestNotificationClaimRecoveredAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = q.Create(ctx, CreateInput{Prompt: "once"})
	if err != nil {
		t.Fatal(err)
	}
	q.SetHandler(func(context.Context, Task) (string, error) { return "ok", nil })
	if _, err := q.ProcessNext(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.claimNotice(ctx)
	if err != nil || !ok {
		t.Fatalf("claim notice=%+v, ok=%v, err=%v", claimed, ok, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, ok, err := reopened.claimNotice(ctx)
	if err != nil || !ok || recovered.Key != claimed.Key || recovered.Attempts != claimed.Attempts+1 {
		t.Fatalf("recovered notice=%+v, ok=%v, err=%v", recovered, ok, err)
	}
}

func TestTerminalCrashCreatesFailureNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := q.Create(ctx, CreateInput{Prompt: "one attempt", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.claim(ctx); err != nil || !ok {
		t.Fatalf("claim: ok=%v, err=%v", ok, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state, err := reopened.Get(ctx, task.ID)
	if err != nil || state.Status != Failed {
		t.Fatalf("terminal crash state=%+v, err=%v", state, err)
	}
	if count, err := reopened.PendingNotices(ctx); err != nil || count != 1 {
		t.Fatalf("failure notice count=%d, err=%v", count, err)
	}
	var notified Notice
	reopened.SetNotifier(func(_ context.Context, notice Notice) error { notified = notice; return nil })
	if processed, err := reopened.ProcessNextNotice(ctx); err != nil || !processed {
		t.Fatalf("failure notice: processed=%v, err=%v", processed, err)
	}
	if notified.Status != Failed || notified.Key != task.ID+":terminal:1" {
		t.Fatalf("wrong failure notice: %+v", notified)
	}
}
