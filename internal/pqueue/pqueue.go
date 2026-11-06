// Package pqueue provides a generic priority queue backed by container/heap.
package pqueue

import (
	"container/heap"
	"sync"
)

// Queue is a generic priority queue.
// Less function determines priority: if less(a, b) returns true, a has higher priority.
type Queue[T any] struct {
	mu    sync.Mutex
	items *pqItems[T]
	less  func(a, b T) bool
}

// New creates a new priority queue.
func New[T any](less func(a, b T) bool) *Queue[T] {
	items := &pqItems[T]{
		data: make([]T, 0, 32),
		less: less,
	}
	heap.Init(items)
	return &Queue[T]{
		items: items,
		less:  less,
	}
}

// NewWithCapacity creates a new priority queue with initial capacity.
func NewWithCapacity[T any](less func(a, b T) bool, capacity int) *Queue[T] {
	items := &pqItems[T]{
		data: make([]T, 0, capacity),
		less: less,
	}
	heap.Init(items)
	return &Queue[T]{
		items: items,
		less:  less,
	}
}

// Push adds an item to the queue.
func (q *Queue[T]) Push(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(q.items, v)
}

// Pop removes and returns the highest-priority item.
func (q *Queue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.items.Len() == 0 {
		var zero T
		return zero, false
	}
	v := heap.Pop(q.items).(T)
	return v, true
}

// Peek returns (but does not remove) the highest-priority item.
func (q *Queue[T]) Peek() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.items.Len() == 0 {
		var zero T
		return zero, false
	}
	return q.items.data[0], true
}

// Len returns the current size.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items.Len()
}

// IsEmpty returns true if the queue has no items.
func (q *Queue[T]) IsEmpty() bool {
	return q.Len() == 0
}

// Clear removes all items from the queue.
func (q *Queue[T]) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items.data = q.items.data[:0]
}

// Snapshot returns a copy of all items (not in priority order).
func (q *Queue[T]) Snapshot() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]T, len(q.items.data))
	copy(result, q.items.data)
	return result
}

// Drain pops all items in priority order.
func (q *Queue[T]) Drain() []T {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := make([]T, 0, q.items.Len())
	for q.items.Len() > 0 {
		v := heap.Pop(q.items).(T)
		result = append(result, v)
	}
	return result
}

// Update re-heaps after an in-place modification of an item.
// The caller is responsible for knowing the index; this is mostly
// useful if you track index via a wrapper type.
func (q *Queue[T]) Update(idx int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if idx < 0 || idx >= q.items.Len() {
		return
	}
	heap.Fix(q.items, idx)
}

// RemoveIf removes all items matching the predicate. Returns removed count.
func (q *Queue[T]) RemoveIf(predicate func(T) bool) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Collect survivors
	survivors := q.items.data[:0]
	removed := 0
	for _, item := range q.items.data {
		if predicate(item) {
			removed++
			continue
		}
		survivors = append(survivors, item)
	}
	q.items.data = survivors
	heap.Init(q.items)
	return removed
}

// Find returns the first item matching the predicate.
func (q *Queue[T]) Find(predicate func(T) bool) (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, item := range q.items.data {
		if predicate(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

// ForEach calls fn on each item (iteration order is not guaranteed).
func (q *Queue[T]) ForEach(fn func(T)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.items.data {
		fn(item)
	}
}

// ====== Internal heap.Interface implementation ======

type pqItems[T any] struct {
	data []T
	less func(a, b T) bool
}

func (p *pqItems[T]) Len() int {
	return len(p.data)
}

func (p *pqItems[T]) Less(i, j int) bool {
	return p.less(p.data[i], p.data[j])
}

func (p *pqItems[T]) Swap(i, j int) {
	p.data[i], p.data[j] = p.data[j], p.data[i]
}

func (p *pqItems[T]) Push(x any) {
	p.data = append(p.data, x.(T))
}

func (p *pqItems[T]) Pop() any {
	old := p.data
	n := len(old)
	if n == 0 {
		var zero T
		return zero
	}
	x := old[n-1]
	p.data = old[:n-1]
	return x
}

// ====== Helper constructors ======

// NewMinHeap creates a min-heap over an ordered type.
func NewMinHeap[T Ordered]() *Queue[T] {
	return New[T](func(a, b T) bool {
		return a < b
	})
}

// NewMaxHeap creates a max-heap over an ordered type.
func NewMaxHeap[T Ordered]() *Queue[T] {
	return New[T](func(a, b T) bool {
		return a > b
	})
}

// Ordered is a type constraint for orderable types.
type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64 |
		~string
}

// WithPriority wraps a value with an explicit priority.
type WithPriority[V any] struct {
	Value    V
	Priority int
}

// NewPriorityMinHeap creates a queue of WithPriority items (lower priority = higher importance).
func NewPriorityMinHeap[V any]() *Queue[WithPriority[V]] {
	return New[WithPriority[V]](func(a, b WithPriority[V]) bool {
		return a.Priority < b.Priority
	})
}

// NewPriorityMaxHeap creates a queue of WithPriority items (higher priority = higher importance).
func NewPriorityMaxHeap[V any]() *Queue[WithPriority[V]] {
	return New[WithPriority[V]](func(a, b WithPriority[V]) bool {
		return a.Priority > b.Priority
	})
}
