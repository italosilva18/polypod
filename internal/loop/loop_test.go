package loop

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestManager() *Manager {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewManager(logger)
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"2h", 2 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"  10s  ", 10 * time.Second, false},
		{"1H", 1 * time.Hour, false}, // lowercased
		{"500ms", 500 * time.Millisecond, false},
		{"garbage", 0, true},
		{"", 0, true},
	}

	for _, tc := range cases {
		got, err := parseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) expected error, got nil", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestManagerAddRejectsInvalidInterval(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	if _, err := m.Add(context.Background(), "lol", "hi"); err == nil {
		t.Error("expected error for invalid duration, got nil")
	}
}

func TestManagerAddRejectsShortInterval(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	// Less than the 10s minimum
	if _, err := m.Add(context.Background(), "5s", "hi"); err == nil {
		t.Error("expected error for interval below 10s, got nil")
	}
	if _, err := m.Add(context.Background(), "1s", "hi"); err == nil {
		t.Error("expected error for 1s interval, got nil")
	}
}

func TestManagerAddAndCount(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	if got := m.Count(); got != 0 {
		t.Errorf("initial Count = %d, want 0", got)
	}

	id1, err := m.Add(context.Background(), "10s", "first")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if id1 != 1 {
		t.Errorf("first id = %d, want 1", id1)
	}

	id2, err := m.Add(context.Background(), "1m", "second")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if id2 != 2 {
		t.Errorf("second id = %d, want 2", id2)
	}

	if got := m.Count(); got != 2 {
		t.Errorf("Count after 2 adds = %d, want 2", got)
	}
}

func TestManagerStop(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	id, err := m.Add(context.Background(), "10s", "bye")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	if err := m.Stop(id); err != nil {
		t.Errorf("Stop(%d) unexpected error: %v", id, err)
	}
	if got := m.Count(); got != 0 {
		t.Errorf("Count after Stop = %d, want 0", got)
	}

	// Stopping an unknown id should error.
	if err := m.Stop(9999); err == nil {
		t.Error("expected error stopping unknown id, got nil")
	}
}

func TestManagerStopAll(t *testing.T) {
	m := newTestManager()

	for i := 0; i < 3; i++ {
		if _, err := m.Add(context.Background(), "30s", "x"); err != nil {
			t.Fatalf("Add returned error: %v", err)
		}
	}
	if got := m.Count(); got != 3 {
		t.Fatalf("Count before StopAll = %d, want 3", got)
	}

	m.StopAll()

	if got := m.Count(); got != 0 {
		t.Errorf("Count after StopAll = %d, want 0", got)
	}
}

func TestManagerList(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	if got := m.List(); !strings.Contains(got, "Nenhum loop ativo") {
		t.Errorf("empty List = %q, want mention of 'Nenhum loop ativo'", got)
	}

	if _, err := m.Add(context.Background(), "10s", "ping servers"); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	out := m.List()
	if !strings.Contains(out, "Loops ativos") {
		t.Errorf("List output missing header: %q", out)
	}
	if !strings.Contains(out, "ping servers") {
		t.Errorf("List output missing prompt: %q", out)
	}
	if !strings.Contains(out, "#1") {
		t.Errorf("List output missing id marker '#1': %q", out)
	}
}

func TestManagerCallbackFires(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	var calls atomic.Int32
	var gotPrompt atomic.Value
	done := make(chan struct{}, 1)

	m.SetCallback(func(p string) {
		gotPrompt.Store(p)
		if calls.Add(1) == 1 {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})

	// Bypass the 10s minimum by constructing a task directly.
	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{
		ID:       42,
		Prompt:   "heartbeat",
		Interval: 20 * time.Millisecond,
		Created:  time.Now(),
		cancel:   cancel,
	}

	m.mu.Lock()
	m.tasks[task.ID] = task
	m.mu.Unlock()

	go m.run(ctx, task)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("callback did not fire within 2s")
	}

	// Stop the loop via Stop to remove from map.
	if err := m.Stop(task.ID); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	if p, _ := gotPrompt.Load().(string); p != "heartbeat" {
		t.Errorf("callback prompt = %q, want %q", p, "heartbeat")
	}

	m.mu.Lock()
	runCount := task.RunCount
	lastRun := task.LastRun
	m.mu.Unlock()

	if runCount < 1 {
		t.Errorf("task.RunCount = %d, want >= 1", runCount)
	}
	if lastRun.IsZero() {
		t.Error("task.LastRun not updated")
	}
}

func TestManagerConcurrentAddStop(t *testing.T) {
	m := newTestManager()
	defer m.StopAll()

	var wg sync.WaitGroup
	ids := make(chan int, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := m.Add(context.Background(), "15s", "concurrent")
			if err != nil {
				t.Errorf("Add error: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	if got := m.Count(); got != 10 {
		t.Errorf("Count = %d, want 10", got)
	}

	for id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := m.Stop(id); err != nil {
				t.Errorf("Stop(%d) error: %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	if got := m.Count(); got != 0 {
		t.Errorf("Count after concurrent stop = %d, want 0", got)
	}
}
