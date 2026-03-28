package circuitbreaker

import (
	"testing"
	"time"
)

func TestBreakerInitialState(t *testing.T) {
	b := New()
	if b.GetState() != StateClosed {
		t.Fatalf("initial state should be Closed, got %s", b.GetState())
	}
	if !b.Allow() {
		t.Fatal("should allow in Closed state")
	}
}

func TestBreakerTripsOpen(t *testing.T) {
	b := New()
	// 3 failures should trip to Open
	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()

	if b.GetState() != StateOpen {
		t.Fatalf("should be Open after 3 failures, got %s", b.GetState())
	}
	if b.Allow() {
		t.Fatal("should NOT allow in Open state")
	}
}

func TestBreakerRecovery(t *testing.T) {
	b := NewWithConfig(2, 50*time.Millisecond, 1)
	b.RecordFailure()
	b.RecordFailure()

	if b.GetState() != StateOpen {
		t.Fatal("should be Open")
	}

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Should transition to HalfOpen
	if !b.Allow() {
		t.Fatal("should allow in HalfOpen")
	}
	if b.GetState() != StateHalfOpen {
		t.Fatalf("should be HalfOpen, got %s", b.GetState())
	}

	// Success should close
	b.RecordSuccess()
	if b.GetState() != StateClosed {
		t.Fatalf("should be Closed after success in HalfOpen, got %s", b.GetState())
	}
}

func TestBreakerReset(t *testing.T) {
	b := New()
	b.RecordFailure()
	b.RecordFailure()
	b.RecordFailure()
	b.Reset()

	if b.GetState() != StateClosed {
		t.Fatal("Reset should return to Closed")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	b1 := r.Get("provider-a")
	b2 := r.Get("provider-a")
	if b1 != b2 {
		t.Fatal("same provider should return same breaker")
	}

	b3 := r.Get("provider-b")
	if b1 == b3 {
		t.Fatal("different providers should return different breakers")
	}

	stats := r.Stats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 breakers, got %d", len(stats))
	}
}
