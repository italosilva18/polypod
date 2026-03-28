package budget

import (
	"log/slog"
	"testing"
)

func TestBudgetCheck(t *testing.T) {
	logger := slog.Default()
	mgr := NewManager(Limits{PerSession: 1000, PerDay: 5000}, logger)

	// Should allow initially
	allowed, _ := mgr.Check("user1")
	if !allowed {
		t.Fatal("should allow before limit")
	}

	// Record usage
	mgr.Record("user1", 500, 0.01)
	allowed, _ = mgr.Check("user1")
	if !allowed {
		t.Fatal("should allow at 50%")
	}

	// Hit session limit
	mgr.Record("user1", 600, 0.01)
	allowed, reason := mgr.Check("user1")
	if allowed {
		t.Fatal("should deny at session limit")
	}
	if reason == "" {
		t.Fatal("should have reason")
	}
}

func TestBudgetDowngrade(t *testing.T) {
	logger := slog.Default()
	mgr := NewManager(Limits{PerDay: 1000, AutoDowngrade: true}, logger)

	if mgr.ShouldDowngrade() {
		t.Fatal("should not downgrade initially")
	}

	mgr.Record("user1", 850, 0)
	if !mgr.ShouldDowngrade() {
		t.Fatal("should downgrade at 85%")
	}
}

func TestBudgetStatus(t *testing.T) {
	logger := slog.Default()
	mgr := NewManager(Limits{PerSession: 10000}, logger)
	mgr.Record("user1", 500, 0.01)

	status := mgr.Status()
	if status == "" {
		t.Fatal("status should not be empty")
	}
}
