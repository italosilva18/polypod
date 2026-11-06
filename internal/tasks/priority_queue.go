package tasks

import (
	"sync"
	"time"

	"github.com/costa/polypod/internal/pqueue"
)

// PriorityTask wraps a task with an explicit priority score.
// Higher Priority runs first; ties broken by earlier Enqueued time.
type PriorityTask struct {
	ID        string
	Name      string
	Priority  int
	Enqueued  time.Time
	Payload   any
	Deadline  time.Time // zero = none
	OnExecute func() error
}

// PriorityTaskQueue is a thread-safe priority queue for tasks.
type PriorityTaskQueue struct {
	mu    sync.Mutex
	queue *pqueue.Queue[*PriorityTask]
}

// NewPriorityTaskQueue creates a new priority task queue.
func NewPriorityTaskQueue() *PriorityTaskQueue {
	return &PriorityTaskQueue{
		queue: pqueue.New[*PriorityTask](func(a, b *PriorityTask) bool {
			// Higher priority first
			if a.Priority != b.Priority {
				return a.Priority > b.Priority
			}
			// Tiebreak: earlier deadline first
			if !a.Deadline.IsZero() && !b.Deadline.IsZero() {
				if !a.Deadline.Equal(b.Deadline) {
					return a.Deadline.Before(b.Deadline)
				}
			} else if !a.Deadline.IsZero() {
				return true
			} else if !b.Deadline.IsZero() {
				return false
			}
			// Final tiebreak: earlier enqueue time
			return a.Enqueued.Before(b.Enqueued)
		}),
	}
}

// Enqueue adds a task to the queue.
func (q *PriorityTaskQueue) Enqueue(task *PriorityTask) {
	if task == nil {
		return
	}
	if task.Enqueued.IsZero() {
		task.Enqueued = time.Now()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue.Push(task)
}

// Dequeue removes and returns the highest-priority task.
// Returns nil, false if the queue is empty.
func (q *PriorityTaskQueue) Dequeue() (*PriorityTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queue.Pop()
}

// Peek returns the highest-priority task without removing it.
func (q *PriorityTaskQueue) Peek() (*PriorityTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queue.Peek()
}

// Len returns the number of queued tasks.
func (q *PriorityTaskQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queue.Len()
}

// IsEmpty returns true if the queue has no tasks.
func (q *PriorityTaskQueue) IsEmpty() bool {
	return q.Len() == 0
}

// DequeueExpired removes all tasks whose deadline has passed.
// Returns the expired tasks.
func (q *PriorityTaskQueue) DequeueExpired(now time.Time) []*PriorityTask {
	q.mu.Lock()
	defer q.mu.Unlock()

	var expired []*PriorityTask
	remaining := make([]*PriorityTask, 0)

	// Drain into slices
	for {
		task, ok := q.queue.Pop()
		if !ok {
			break
		}
		if !task.Deadline.IsZero() && task.Deadline.Before(now) {
			expired = append(expired, task)
		} else {
			remaining = append(remaining, task)
		}
	}

	// Requeue non-expired
	for _, t := range remaining {
		q.queue.Push(t)
	}

	return expired
}

// DequeueByID removes a specific task by ID.
func (q *PriorityTaskQueue) DequeueByID(id string) (*PriorityTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var found *PriorityTask
	remaining := make([]*PriorityTask, 0)

	for {
		task, ok := q.queue.Pop()
		if !ok {
			break
		}
		if found == nil && task.ID == id {
			found = task
		} else {
			remaining = append(remaining, task)
		}
	}

	for _, t := range remaining {
		q.queue.Push(t)
	}

	return found, found != nil
}

// Snapshot returns a copy of all queued tasks (not in priority order).
func (q *PriorityTaskQueue) Snapshot() []*PriorityTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queue.Snapshot()
}

// DrainAll pops all tasks in priority order.
func (q *PriorityTaskQueue) DrainAll() []*PriorityTask {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queue.Drain()
}

// Clear removes all tasks.
func (q *PriorityTaskQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queue.Clear()
}
