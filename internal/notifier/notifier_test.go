package notifier

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type testBackend struct {
	calls int
	err   error
}

func (b *testBackend) Send(context.Context, Notification) error { b.calls++; return b.err }
func (*testBackend) Available() bool                            { return true }

func TestDebounceIsRetryableAndFailureDoesNotConsumeWindow(t *testing.T) {
	backend := &testBackend{err: errors.New("unavailable")}
	n := &Notifier{backend: backend, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), debounce: time.Hour}
	if err := n.Notify(context.Background(), "title", "body"); err == nil {
		t.Fatal("expected failure")
	}
	backend.err = nil
	if err := n.Notify(context.Background(), "title", "body"); err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), "title", "another"); err == nil {
		t.Fatal("debounced message reported as delivered")
	}
	if backend.calls != 2 {
		t.Fatal(backend.calls)
	}
}
