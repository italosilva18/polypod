package taskqueue

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskTimeoutBecomesDurableFailure(t *testing.T) {
	queue, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	queue.SetTaskTimeout(15 * time.Millisecond)
	queue.SetHandler(func(ctx context.Context, task Task) (string, error) {
		<-ctx.Done()
		return "", nil
	})
	task, err := queue.Create(context.Background(), CreateInput{Prompt: "aguarde", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := queue.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("processed=%v err=%v", processed, err)
	}
	got, err := queue.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Failed || !strings.Contains(got.LastError, "deadline") {
		t.Fatalf("task status=%s error=%q", got.Status, got.LastError)
	}
}
