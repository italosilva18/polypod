package bloom

import (
	"fmt"
	"sync"
	"testing"
)

func TestFilter_AddContains(t *testing.T) {
	f := New(100, 0.01)

	items := []string{"apple", "banana", "cherry", "date"}
	for _, item := range items {
		f.AddString(item)
	}

	for _, item := range items {
		if !f.ContainsString(item) {
			t.Errorf("Contains(%q) should return true", item)
		}
	}
}

func TestFilter_NoFalseNegatives(t *testing.T) {
	f := New(1000, 0.01)

	for i := 0; i < 1000; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	// All added items must be reported as present
	for i := 0; i < 1000; i++ {
		if !f.ContainsString(fmt.Sprintf("item-%d", i)) {
			t.Errorf("Contains(item-%d) returned false negative", i)
		}
	}
}

func TestFilter_Count(t *testing.T) {
	f := New(100, 0.01)

	if f.Count() != 0 {
		t.Errorf("empty Count = %d; want 0", f.Count())
	}

	f.AddString("a")
	f.AddString("b")
	f.AddString("c")

	if f.Count() != 3 {
		t.Errorf("Count = %d; want 3", f.Count())
	}
}

func TestFilter_Reset(t *testing.T) {
	f := New(100, 0.01)
	f.AddString("foo")
	f.AddString("bar")

	f.Reset()

	if f.Count() != 0 {
		t.Errorf("Count after Reset = %d; want 0", f.Count())
	}
	if f.ContainsString("foo") {
		t.Error("Contains after Reset should return false")
	}
}

func TestFilter_Size(t *testing.T) {
	f := New(1000, 0.01)
	if f.Size() == 0 {
		t.Error("Size should be > 0")
	}
}

func TestFilter_HashCount(t *testing.T) {
	f := New(1000, 0.01)
	if f.HashCount() == 0 {
		t.Error("HashCount should be > 0")
	}
}

func TestFilter_FalsePositiveRate(t *testing.T) {
	f := New(10000, 0.01) // 1% target FP rate

	// Insert known items
	for i := 0; i < 10000; i++ {
		f.AddString(fmt.Sprintf("known-%d", i))
	}

	// Test unknown items
	falsePositives := 0
	total := 10000
	for i := 0; i < total; i++ {
		if f.ContainsString(fmt.Sprintf("unknown-%d", i)) {
			falsePositives++
		}
	}

	fpRate := float64(falsePositives) / float64(total)
	// Allow up to 5% (well above the 1% target) for the noise floor
	if fpRate > 0.05 {
		t.Errorf("FP rate = %f; want <= 0.05", fpRate)
	}
}

func TestFilter_EstimatedFPRate(t *testing.T) {
	f := New(100, 0.01)

	if f.EstimatedFPRate() != 0 {
		t.Errorf("empty EstimatedFPRate = %f; want 0", f.EstimatedFPRate())
	}

	for i := 0; i < 50; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	rate := f.EstimatedFPRate()
	if rate < 0 || rate > 1 {
		t.Errorf("EstimatedFPRate = %f; want 0-1", rate)
	}
}

func TestFilter_SetBitsRatio(t *testing.T) {
	f := New(1000, 0.01)

	if f.SetBitsRatio() != 0 {
		t.Errorf("empty SetBitsRatio = %f; want 0", f.SetBitsRatio())
	}

	for i := 0; i < 100; i++ {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	ratio := f.SetBitsRatio()
	if ratio < 0 || ratio > 1 {
		t.Errorf("SetBitsRatio = %f; want 0-1", ratio)
	}
}

func TestFilter_MarshalUnmarshal(t *testing.T) {
	f := New(100, 0.01)
	f.AddString("foo")
	f.AddString("bar")
	f.AddString("baz")

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	g := New(1, 0.01)
	if err := g.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}

	if g.Count() != f.Count() {
		t.Errorf("Count mismatch: %d vs %d", g.Count(), f.Count())
	}

	for _, item := range []string{"foo", "bar", "baz"} {
		if !g.ContainsString(item) {
			t.Errorf("Unmarshalled filter should contain %q", item)
		}
	}
}

func TestFilter_Merge(t *testing.T) {
	a := New(100, 0.01)
	b := New(100, 0.01)

	a.AddString("foo")
	a.AddString("bar")

	b.AddString("baz")
	b.AddString("qux")

	if err := a.Merge(b); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	for _, item := range []string{"foo", "bar", "baz", "qux"} {
		if !a.ContainsString(item) {
			t.Errorf("Merged filter should contain %q", item)
		}
	}
}

func TestFilter_NewWithParams(t *testing.T) {
	f := NewWithParams(1024, 5)

	if f.Size() != 1024 {
		t.Errorf("Size = %d; want 1024", f.Size())
	}
	if f.HashCount() != 5 {
		t.Errorf("HashCount = %d; want 5", f.HashCount())
	}

	f.AddString("test")
	if !f.ContainsString("test") {
		t.Error("Add+Contains should work")
	}
}

func TestFilter_Concurrent(t *testing.T) {
	f := New(10000, 0.01)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				f.AddString(fmt.Sprintf("worker-%d-item-%d", id, j))
			}
		}(i)
	}
	wg.Wait()

	if f.Count() != 1000 {
		t.Errorf("Count = %d; want 1000", f.Count())
	}
}
