package tokenbudget

import (
	"sync"
	"testing"
)

func TestBudget_Basic(t *testing.T) {
	b := New(1000)

	if b.Limit() != 1000 {
		t.Errorf("Limit = %d; want 1000", b.Limit())
	}
	if b.Used() != 0 {
		t.Errorf("Used = %d; want 0", b.Used())
	}
	if b.Remaining() != 1000 {
		t.Errorf("Remaining = %d; want 1000", b.Remaining())
	}
}

func TestBudget_Use(t *testing.T) {
	b := New(1000)

	if !b.Use(300) {
		t.Error("Use(300) should succeed")
	}
	if b.Used() != 300 {
		t.Errorf("Used = %d; want 300", b.Used())
	}
	if b.Remaining() != 700 {
		t.Errorf("Remaining = %d; want 700", b.Remaining())
	}

	if !b.Use(700) {
		t.Error("Use(700) should succeed (exact fit)")
	}
	if b.Used() != 1000 {
		t.Errorf("Used = %d; want 1000", b.Used())
	}

	if b.Use(1) {
		t.Error("Use(1) should fail (over limit)")
	}
}

func TestBudget_ForceUse(t *testing.T) {
	b := New(100)

	used := b.ForceUse(150)
	if used != 150 {
		t.Errorf("ForceUse returned %d; want 150", used)
	}
	if b.Used() != 150 {
		t.Errorf("Used = %d; want 150", b.Used())
	}
	if !b.IsExceeded() {
		t.Error("IsExceeded should be true")
	}
}

func TestBudget_Reserve(t *testing.T) {
	b := New(1000)

	if !b.Reserve(500) {
		t.Error("Reserve(500) should succeed")
	}
	if b.Reserved() != 500 {
		t.Errorf("Reserved = %d; want 500", b.Reserved())
	}
	if b.Remaining() != 500 {
		t.Errorf("Remaining = %d; want 500", b.Remaining())
	}

	if b.Reserve(600) {
		t.Error("Reserve(600) should fail (exceeds remaining)")
	}

	if !b.Reserve(500) {
		t.Error("Reserve(500) should succeed (exact fit)")
	}
}

func TestBudget_Release(t *testing.T) {
	b := New(1000)
	b.Reserve(500)

	b.Release(200)
	if b.Reserved() != 300 {
		t.Errorf("Reserved after Release = %d; want 300", b.Reserved())
	}

	// Releasing more than reserved should clamp to 0
	b.Release(1000)
	if b.Reserved() != 0 {
		t.Errorf("Reserved after over-Release = %d; want 0", b.Reserved())
	}
}

func TestBudget_Commit(t *testing.T) {
	b := New(1000)
	b.Reserve(500)

	// Commit 300 of the 500 reserved
	b.Commit(500, 300)

	if b.Used() != 300 {
		t.Errorf("Used after Commit = %d; want 300", b.Used())
	}
	if b.Reserved() != 0 {
		t.Errorf("Reserved after Commit = %d; want 0", b.Reserved())
	}
}

func TestBudget_Reset(t *testing.T) {
	b := New(1000)
	b.Use(500)
	b.Reserve(200)

	b.Reset()

	if b.Used() != 0 {
		t.Errorf("Used after Reset = %d; want 0", b.Used())
	}
	if b.Reserved() != 0 {
		t.Errorf("Reserved after Reset = %d; want 0", b.Reserved())
	}
	// Limit stays
	if b.Limit() != 1000 {
		t.Errorf("Limit after Reset = %d; want 1000", b.Limit())
	}
}

func TestBudget_Percent(t *testing.T) {
	b := New(1000)

	if b.Percent() != 0 {
		t.Errorf("empty Percent = %f; want 0", b.Percent())
	}

	b.Use(500)
	if b.Percent() != 50.0 {
		t.Errorf("Percent at half = %f; want 50.0", b.Percent())
	}

	b.Reserve(250)
	if b.Percent() != 75.0 {
		t.Errorf("Percent with reservation = %f; want 75.0", b.Percent())
	}
}

func TestBudget_IsNearLimit(t *testing.T) {
	b := New(1000)
	b.Use(800)

	if !b.IsNearLimit(75) {
		t.Error("IsNearLimit(75) should be true at 80%")
	}
	if b.IsNearLimit(90) {
		t.Error("IsNearLimit(90) should be false at 80%")
	}
}

func TestBudget_IsExceeded(t *testing.T) {
	b := New(100)

	if b.IsExceeded() {
		t.Error("empty buffer should not be exceeded")
	}

	b.Use(100)
	if b.IsExceeded() {
		t.Error("at limit should not be exceeded")
	}

	b.ForceUse(1)
	if !b.IsExceeded() {
		t.Error("over limit should be exceeded")
	}
}

func TestBudget_SetLimit(t *testing.T) {
	b := New(100)
	b.SetLimit(500)

	if b.Limit() != 500 {
		t.Errorf("Limit = %d; want 500", b.Limit())
	}
}

func TestBudget_Snapshot(t *testing.T) {
	b := New(1000)
	b.Use(300)
	b.Reserve(200)

	s := b.Snapshot()
	if s.Used != 300 || s.Reserved != 200 || s.Limit != 1000 {
		t.Errorf("Snapshot = %+v", s)
	}
}

func TestBudget_Peak(t *testing.T) {
	b := New(1000)
	b.Use(500)
	b.Use(200) // 700 peak

	if b.Peak() != 700 {
		t.Errorf("Peak = %d; want 700", b.Peak())
	}
}

func TestBudget_Concurrent(t *testing.T) {
	b := New(10000)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Use(1)
			}
		}()
	}
	wg.Wait()

	if b.Used() != 1000 {
		t.Errorf("Used after concurrent = %d; want 1000", b.Used())
	}
}

func TestAggregator(t *testing.T) {
	a := NewAggregator()

	b1 := New(100)
	b2 := New(200)

	a.AddBudget("session1", b1)
	a.AddBudget("session2", b2)

	b1.Use(50)
	b2.Use(100)

	if a.TotalUsed() != 150 {
		t.Errorf("TotalUsed = %d; want 150", a.TotalUsed())
	}
	if a.TotalLimit() != 300 {
		t.Errorf("TotalLimit = %d; want 300", a.TotalLimit())
	}

	names := a.Names()
	if len(names) != 2 {
		t.Errorf("Names len = %d; want 2", len(names))
	}

	a.Remove("session1")
	if len(a.Names()) != 1 {
		t.Errorf("Names after Remove len = %d; want 1", len(a.Names()))
	}
}
