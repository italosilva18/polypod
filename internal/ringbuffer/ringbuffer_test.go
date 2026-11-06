package ringbuffer

import (
	"sync"
	"testing"
)

func TestBuffer_PushPopBasic(t *testing.T) {
	b := New[int](5)

	b.Push(1)
	b.Push(2)
	b.Push(3)

	if b.Len() != 3 {
		t.Errorf("Len = %d; want 3", b.Len())
	}

	snap := b.Snapshot()
	want := []int{1, 2, 3}
	for i, v := range snap {
		if v != want[i] {
			t.Errorf("snap[%d] = %d; want %d", i, v, want[i])
		}
	}
}

func TestBuffer_OverwriteOldest(t *testing.T) {
	b := New[int](3)

	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	if b.Len() != 3 {
		t.Errorf("Len = %d; want 3", b.Len())
	}

	snap := b.Snapshot()
	want := []int{3, 4, 5}
	for i, v := range snap {
		if v != want[i] {
			t.Errorf("snap[%d] = %d; want %d", i, v, want[i])
		}
	}
}

func TestBuffer_First(t *testing.T) {
	b := New[int](3)

	if _, ok := b.First(); ok {
		t.Error("First on empty should return !ok")
	}

	b.Push(1)
	b.Push(2)
	b.Push(3)

	v, ok := b.First()
	if !ok || v != 1 {
		t.Errorf("First = %d, %v; want 1, true", v, ok)
	}

	// After overwrite, First should be the new oldest
	b.Push(4)
	v, _ = b.First()
	if v != 2 {
		t.Errorf("First after overwrite = %d; want 2", v)
	}
}

func TestBuffer_Last(t *testing.T) {
	b := New[int](3)

	if _, ok := b.Last(); ok {
		t.Error("Last on empty should return !ok")
	}

	b.Push(1)
	b.Push(2)
	b.Push(3)

	v, ok := b.Last()
	if !ok || v != 3 {
		t.Errorf("Last = %d, %v; want 3, true", v, ok)
	}

	b.Push(4)
	v, _ = b.Last()
	if v != 4 {
		t.Errorf("Last after push = %d; want 4", v)
	}
}

func TestBuffer_At(t *testing.T) {
	b := New[int](5)
	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	for i := 0; i < 5; i++ {
		v, ok := b.At(i)
		if !ok {
			t.Fatalf("At(%d) returned !ok", i)
		}
		if v != i+1 {
			t.Errorf("At(%d) = %d; want %d", i, v, i+1)
		}
	}

	if _, ok := b.At(-1); ok {
		t.Error("At(-1) should return !ok")
	}
	if _, ok := b.At(100); ok {
		t.Error("At(100) should return !ok")
	}
}

func TestBuffer_Reset(t *testing.T) {
	b := New[int](5)
	b.Push(1)
	b.Push(2)
	b.Push(3)

	b.Reset()

	if b.Len() != 0 {
		t.Errorf("Len after Reset = %d; want 0", b.Len())
	}
	if !b.IsEmpty() {
		t.Error("IsEmpty after Reset should be true")
	}
}

func TestBuffer_IsFull(t *testing.T) {
	b := New[int](3)

	if b.IsFull() {
		t.Error("empty buffer should not be full")
	}

	b.Push(1)
	b.Push(2)
	if b.IsFull() {
		t.Error("buffer with 2/3 should not be full")
	}

	b.Push(3)
	if !b.IsFull() {
		t.Error("buffer with 3/3 should be full")
	}

	b.Push(4) // overwrite
	if !b.IsFull() {
		t.Error("buffer should still be full after overwrite")
	}
}

func TestBuffer_Cap(t *testing.T) {
	b := New[int](10)
	if b.Cap() != 10 {
		t.Errorf("Cap = %d; want 10", b.Cap())
	}
}

func TestBuffer_SnapshotReverse(t *testing.T) {
	b := New[int](5)
	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	rev := b.SnapshotReverse()
	want := []int{5, 4, 3, 2, 1}
	for i, v := range rev {
		if v != want[i] {
			t.Errorf("rev[%d] = %d; want %d", i, v, want[i])
		}
	}
}

func TestBuffer_PushAll(t *testing.T) {
	b := New[int](5)
	b.PushAll(1, 2, 3, 4, 5)

	if b.Len() != 5 {
		t.Errorf("Len = %d; want 5", b.Len())
	}
}

func TestBuffer_Filter(t *testing.T) {
	b := New[int](10)
	for i := 1; i <= 10; i++ {
		b.Push(i)
	}

	even := b.Filter(func(v int) bool {
		return v%2 == 0
	})

	if len(even) != 5 {
		t.Errorf("Filter len = %d; want 5", len(even))
	}
}

func TestBuffer_Count(t *testing.T) {
	b := New[int](10)
	for i := 1; i <= 10; i++ {
		b.Push(i)
	}

	count := b.Count(func(v int) bool {
		return v > 5
	})
	if count != 5 {
		t.Errorf("Count > 5 = %d; want 5", count)
	}
}

func TestBuffer_Resize(t *testing.T) {
	b := New[int](5)
	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	b.Resize(3)

	// Should keep most recent 3
	if b.Len() != 3 {
		t.Errorf("Len after Resize = %d; want 3", b.Len())
	}

	snap := b.Snapshot()
	want := []int{3, 4, 5}
	for i, v := range snap {
		if v != want[i] {
			t.Errorf("snap[%d] after resize = %d; want %d", i, v, want[i])
		}
	}
}

func TestBuffer_Drain(t *testing.T) {
	b := New[int](5)
	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	drained := b.Drain()

	if len(drained) != 5 {
		t.Errorf("Drain len = %d; want 5", len(drained))
	}
	if b.Len() != 0 {
		t.Errorf("Len after Drain = %d; want 0", b.Len())
	}
}

func TestBuffer_ForEach(t *testing.T) {
	b := New[int](5)
	for i := 1; i <= 5; i++ {
		b.Push(i)
	}

	sum := 0
	b.ForEach(func(i int, v int) {
		sum += v
	})

	if sum != 15 {
		t.Errorf("ForEach sum = %d; want 15", sum)
	}
}

func TestBuffer_Concurrent(t *testing.T) {
	b := New[int](100)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Push(id*100 + j)
			}
		}(i)
	}
	wg.Wait()

	if b.Len() != 100 {
		t.Errorf("Len after concurrent push = %d; want 100", b.Len())
	}
}
