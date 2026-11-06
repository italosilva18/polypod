package observability

import (
	"log/slog"
	"testing"
	"time"
)

func TestNewNotifier(t *testing.T) {
	n := NewNotifier()
	if n == nil {
		t.Fatal("NewNotifier returned nil")
	}
}

func TestFallbackNotifier_IsAvailable(t *testing.T) {
	n := &FallbackNotifier{}
	if !n.IsAvailable() {
		t.Error("FallbackNotifier should always be available")
	}
}

func TestFallbackNotifier_Notify(t *testing.T) {
	n := &FallbackNotifier{}
	err := n.Notify(Notification{
		Title:   "Test",
		Message: "Hello",
	})
	if err != nil {
		t.Errorf("FallbackNotifier.Notify: %v", err)
	}
}

func TestNotification_Construction(t *testing.T) {
	n := Notification{
		Title:   "title",
		Message: "message",
		Icon:    "info",
		Sound:   true,
		Urgent:  true,
	}
	if n.Title != "title" || n.Message != "message" {
		t.Errorf("Notification fields not set correctly: %+v", n)
	}
	if n.Icon != "info" || !n.Sound || !n.Urgent {
		t.Errorf("Notification fields not set correctly: %+v", n)
	}
}

func TestNewLogRing(t *testing.T) {
	r := NewLogRing(5)
	if r == nil {
		t.Fatal("NewLogRing returned nil")
	}
	if r.Capacity() != 5 {
		t.Errorf("Capacity = %d; want 5", r.Capacity())
	}
	if r.Len() != 0 {
		t.Errorf("Len = %d; want 0", r.Len())
	}
}

func TestLogRing_AppendTail(t *testing.T) {
	r := NewLogRing(5)

	for i := 0; i < 3; i++ {
		r.AppendSimple(slog.LevelInfo, "msg")
	}

	if r.Len() != 3 {
		t.Errorf("Len = %d; want 3", r.Len())
	}

	tail := r.Tail(2)
	if len(tail) != 2 {
		t.Errorf("Tail(2) len = %d; want 2", len(tail))
	}
}

func TestLogRing_TailEdgeCases(t *testing.T) {
	r := NewLogRing(5)
	r.AppendSimple(slog.LevelInfo, "a")
	r.AppendSimple(slog.LevelInfo, "b")
	r.AppendSimple(slog.LevelInfo, "c")

	all := r.Tail(0)
	if len(all) != 3 {
		t.Errorf("Tail(0) len = %d; want 3 (all)", len(all))
	}

	overflow := r.Tail(100)
	if len(overflow) != 3 {
		t.Errorf("Tail(100) len = %d; want 3 (all)", len(overflow))
	}
}

func TestLogRing_Append(t *testing.T) {
	r := NewLogRing(3)

	for i := 0; i < 5; i++ {
		r.Append(LogEntry{
			Time:    time.Now(),
			Level:   slog.LevelInfo,
			Message: "entry",
		})
	}

	if r.Len() != 3 {
		t.Errorf("Len = %d; want 3 (capacity)", r.Len())
	}
}

func TestLogRing_Snapshot(t *testing.T) {
	r := NewLogRing(5)
	r.AppendSimple(slog.LevelInfo, "a")
	r.AppendSimple(slog.LevelInfo, "b")

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Errorf("Snapshot len = %d; want 2", len(snap))
	}
	if snap[0].Message != "a" || snap[1].Message != "b" {
		t.Errorf("Snapshot order wrong: %+v", snap)
	}
}

func TestLogRing_Capacity(t *testing.T) {
	r := NewLogRing(10)
	if r.Capacity() != 10 {
		t.Errorf("Capacity = %d; want 10", r.Capacity())
	}
}

func TestLogRing_FilterByLevel(t *testing.T) {
	r := NewLogRing(10)
	r.AppendSimple(slog.LevelDebug, "debug1")
	r.AppendSimple(slog.LevelInfo, "info1")
	r.AppendSimple(slog.LevelWarn, "warn1")
	r.AppendSimple(slog.LevelError, "error1")

	warns := r.FilterByLevel(slog.LevelWarn)
	if len(warns) != 2 { // Warn and Error
		t.Errorf("FilterByLevel(Warn) = %d; want 2", len(warns))
	}
	for _, e := range warns {
		if e.Level < slog.LevelWarn {
			t.Errorf("FilterByLevel returned entry below threshold: %+v", e)
		}
	}
}

func TestLogRing_FilterByMessage(t *testing.T) {
	r := NewLogRing(10)
	r.AppendSimple(slog.LevelInfo, "user login")
	r.AppendSimple(slog.LevelInfo, "login failed")
	r.AppendSimple(slog.LevelInfo, "logout")

	results := r.FilterByMessage("login")
	if len(results) != 2 {
		t.Errorf("FilterByMessage len = %d; want 2", len(results))
	}
}

func TestLogRing_Reset(t *testing.T) {
	r := NewLogRing(5)
	r.AppendSimple(slog.LevelInfo, "msg")
	r.AppendSimple(slog.LevelInfo, "msg2")
	r.Reset()
	if r.Len() != 0 {
		t.Errorf("Len after Reset = %d; want 0", r.Len())
	}
	if len(r.Snapshot()) != 0 {
		t.Errorf("Snapshot after Reset non-empty")
	}
}
