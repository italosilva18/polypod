package pqueue

import (
	"sync"
	"testing"
)

func TestQueue_PushPop_MinHeap(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })

	q.Push(5)
	q.Push(2)
	q.Push(8)
	q.Push(1)
	q.Push(3)

	want := []int{1, 2, 3, 5, 8}
	for i, w := range want {
		v, ok := q.Pop()
		if !ok {
			t.Fatalf("Pop %d returned !ok", i)
		}
		if v != w {
			t.Errorf("Pop %d = %d; want %d", i, v, w)
		}
	}

	if _, ok := q.Pop(); ok {
		t.Error("Pop on empty should return !ok")
	}
}

func TestQueue_PushPop_MaxHeap(t *testing.T) {
	q := New[int](func(a, b int) bool { return a > b })

	q.Push(5)
	q.Push(2)
	q.Push(8)
	q.Push(1)

	want := []int{8, 5, 2, 1}
	for i, w := range want {
		v, _ := q.Pop()
		if v != w {
			t.Errorf("Pop %d = %d; want %d", i, v, w)
		}
	}
}

func TestQueue_Peek(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })

	if _, ok := q.Peek(); ok {
		t.Error("Peek on empty should return !ok")
	}

	q.Push(5)
	q.Push(2)
	q.Push(8)

	v, ok := q.Peek()
	if !ok || v != 2 {
		t.Errorf("Peek = %v, %v; want 2, true", v, ok)
	}

	// Peek shouldn't remove
	if q.Len() != 3 {
		t.Errorf("Len after Peek = %d; want 3", q.Len())
	}
}

func TestQueue_Len(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })

	if q.Len() != 0 {
		t.Errorf("empty Len = %d; want 0", q.Len())
	}
	if !q.IsEmpty() {
		t.Error("empty IsEmpty should be true")
	}

	q.Push(1)
	q.Push(2)
	q.Push(3)

	if q.Len() != 3 {
		t.Errorf("Len = %d; want 3", q.Len())
	}
	if q.IsEmpty() {
		t.Error("non-empty IsEmpty should be false")
	}
}

func TestQueue_Clear(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	q.Push(1)
	q.Push(2)
	q.Push(3)

	q.Clear()

	if q.Len() != 0 {
		t.Errorf("Len after Clear = %d; want 0", q.Len())
	}
}

func TestQueue_Drain(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	q.Push(3)
	q.Push(1)
	q.Push(2)

	drained := q.Drain()

	if len(drained) != 3 {
		t.Errorf("Drain len = %d; want 3", len(drained))
	}

	// Should be in priority order
	want := []int{1, 2, 3}
	for i, v := range drained {
		if v != want[i] {
			t.Errorf("Drain[%d] = %d; want %d", i, v, want[i])
		}
	}

	if q.Len() != 0 {
		t.Errorf("Len after Drain = %d; want 0", q.Len())
	}
}

func TestQueue_Snapshot(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	q.Push(1)
	q.Push(2)
	q.Push(3)

	snap := q.Snapshot()
	if len(snap) != 3 {
		t.Errorf("Snapshot len = %d; want 3", len(snap))
	}

	// Snapshot should not affect the queue
	if q.Len() != 3 {
		t.Errorf("Len after Snapshot = %d; want 3", q.Len())
	}
}

func TestQueue_RemoveIf(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	for i := 1; i <= 10; i++ {
		q.Push(i)
	}

	removed := q.RemoveIf(func(v int) bool {
		return v%2 == 0
	})

	if removed != 5 {
		t.Errorf("RemoveIf removed = %d; want 5", removed)
	}
	if q.Len() != 5 {
		t.Errorf("Len after RemoveIf = %d; want 5", q.Len())
	}

	// All remaining should be odd
	for {
		v, ok := q.Pop()
		if !ok {
			break
		}
		if v%2 == 0 {
			t.Errorf("found even value %d after RemoveIf", v)
		}
	}
}

func TestQueue_Find(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	for i := 1; i <= 5; i++ {
		q.Push(i)
	}

	v, ok := q.Find(func(x int) bool {
		return x == 3
	})
	if !ok || v != 3 {
		t.Errorf("Find(3) = %v, %v; want 3, true", v, ok)
	}

	_, ok = q.Find(func(x int) bool {
		return x == 100
	})
	if ok {
		t.Error("Find(100) should return !ok")
	}
}

func TestQueue_ForEach(t *testing.T) {
	q := New[int](func(a, b int) bool { return a < b })
	q.Push(1)
	q.Push(2)
	q.Push(3)

	sum := 0
	q.ForEach(func(v int) {
		sum += v
	})

	if sum != 6 {
		t.Errorf("ForEach sum = %d; want 6", sum)
	}
}

func TestQueue_NewWithCapacity(t *testing.T) {
	q := NewWithCapacity[int](func(a, b int) bool { return a < b }, 100)

	for i := 0; i < 50; i++ {
		q.Push(i)
	}

	if q.Len() != 50 {
		t.Errorf("Len = %d; want 50", q.Len())
	}
}

func TestNewMinHeap(t *testing.T) {
	q := NewMinHeap[int]()
	q.Push(5)
	q.Push(2)
	q.Push(8)

	v, _ := q.Pop()
	if v != 2 {
		t.Errorf("MinHeap Pop = %d; want 2", v)
	}
}

func TestNewMaxHeap(t *testing.T) {
	q := NewMaxHeap[int]()
	q.Push(5)
	q.Push(2)
	q.Push(8)

	v, _ := q.Pop()
	if v != 8 {
		t.Errorf("MaxHeap Pop = %d; want 8", v)
	}
}

func TestNewPriorityMaxHeap(t *testing.T) {
	q := NewPriorityMaxHeap[string]()
	q.Push(WithPriority[string]{Value: "low", Priority: 1})
	q.Push(WithPriority[string]{Value: "high", Priority: 100})
	q.Push(WithPriority[string]{Value: "med", Priority: 50})

	item, _ := q.Pop()
	if item.Value != "high" {
		t.Errorf("Pop = %s; want high", item.Value)
	}
}

func TestQueue_Concurrent(t *testing.T) {
	q := NewMinHeap[int]()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				q.Push(start*50 + j)
			}
		}(i)
	}
	wg.Wait()

	if q.Len() != 500 {
		t.Errorf("Len after concurrent push = %d; want 500", q.Len())
	}

	drained := q.Drain()
	for i := 1; i < len(drained); i++ {
		if drained[i] < drained[i-1] {
			t.Errorf("not sorted: drained[%d]=%d < drained[%d]=%d",
				i, drained[i], i-1, drained[i-1])
			break
		}
	}
}
