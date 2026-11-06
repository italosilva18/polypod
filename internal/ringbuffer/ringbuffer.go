// Package ringbuffer provides a generic fixed-size circular buffer.
package ringbuffer

import (
	"sync"
)

// Buffer is a thread-safe fixed-capacity circular buffer.
// When full, Push overwrites the oldest element.
type Buffer[T any] struct {
	mu       sync.Mutex
	data     []T
	capacity int
	head     int // next write position
	size     int // current number of elements
	zero     T
}

// New creates a new ring buffer with the given capacity.
// Panics if capacity <= 0.
func New[T any](capacity int) *Buffer[T] {
	if capacity <= 0 {
		panic("ringbuffer: capacity must be positive")
	}
	return &Buffer[T]{
		data:     make([]T, capacity),
		capacity: capacity,
	}
}

// Push adds a value to the buffer. If full, overwrites the oldest entry.
func (b *Buffer[T]) Push(v T) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data[b.head] = v
	b.head = (b.head + 1) % b.capacity
	if b.size < b.capacity {
		b.size++
	}
}

// PushAll adds multiple values to the buffer.
func (b *Buffer[T]) PushAll(values ...T) {
	for _, v := range values {
		b.Push(v)
	}
}

// Len returns the current number of elements.
func (b *Buffer[T]) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// Cap returns the capacity.
func (b *Buffer[T]) Cap() int {
	return b.capacity
}

// IsEmpty returns true if the buffer is empty.
func (b *Buffer[T]) IsEmpty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == 0
}

// IsFull returns true if the buffer is at capacity.
func (b *Buffer[T]) IsFull() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size == b.capacity
}

// Snapshot returns a copy of all current values in chronological order (oldest first).
func (b *Buffer[T]) Snapshot() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

// SnapshotReverse returns a copy in reverse chronological order (newest first).
func (b *Buffer[T]) SnapshotReverse() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.snapshotLocked()
	// Reverse in-place
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}

// Last returns the most recently pushed element.
func (b *Buffer[T]) Last() (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return b.zero, false
	}
	idx := (b.head - 1 + b.capacity) % b.capacity
	return b.data[idx], true
}

// First returns the oldest element still in the buffer.
func (b *Buffer[T]) First() (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return b.zero, false
	}
	idx := (b.head - b.size + b.capacity) % b.capacity
	return b.data[idx], true
}

// At returns the element at index i (0 = oldest, size-1 = newest).
func (b *Buffer[T]) At(i int) (T, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= b.size {
		return b.zero, false
	}
	startIdx := (b.head - b.size + b.capacity) % b.capacity
	idx := (startIdx + i) % b.capacity
	return b.data[idx], true
}

// Reset clears the buffer.
func (b *Buffer[T]) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Zero out the slots
	for i := range b.data {
		b.data[i] = b.zero
	}
	b.head = 0
	b.size = 0
}

// Resize changes the capacity. Preserves the most recent elements.
func (b *Buffer[T]) Resize(newCapacity int) {
	if newCapacity <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	snapshot := b.snapshotLocked()
	newData := make([]T, newCapacity)

	// Keep most recent elements
	start := 0
	if len(snapshot) > newCapacity {
		start = len(snapshot) - newCapacity
	}
	kept := snapshot[start:]
	copy(newData, kept)

	b.data = newData
	b.capacity = newCapacity
	b.size = len(kept)
	b.head = b.size % b.capacity
}

// ForEach iterates over all elements in chronological order.
func (b *Buffer[T]) ForEach(fn func(i int, v T)) {
	s := b.Snapshot()
	for i, v := range s {
		fn(i, v)
	}
}

// Filter returns a new slice containing elements matching the predicate.
func (b *Buffer[T]) Filter(predicate func(T) bool) []T {
	s := b.Snapshot()
	result := make([]T, 0, len(s))
	for _, v := range s {
		if predicate(v) {
			result = append(result, v)
		}
	}
	return result
}

// Count returns the number of elements matching the predicate.
func (b *Buffer[T]) Count(predicate func(T) bool) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	s := b.snapshotLocked()
	for _, v := range s {
		if predicate(v) {
			count++
		}
	}
	return count
}

// snapshotLocked returns a chronological snapshot. Must hold the lock.
func (b *Buffer[T]) snapshotLocked() []T {
	if b.size == 0 {
		return nil
	}
	result := make([]T, b.size)
	startIdx := (b.head - b.size + b.capacity) % b.capacity
	for i := 0; i < b.size; i++ {
		result[i] = b.data[(startIdx+i)%b.capacity]
	}
	return result
}

// Drain removes and returns all elements in chronological order.
func (b *Buffer[T]) Drain() []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := b.snapshotLocked()
	for i := range b.data {
		b.data[i] = b.zero
	}
	b.head = 0
	b.size = 0
	return result
}
