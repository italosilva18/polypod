package messages

import (
	"sync"
)

// QueuePriority determines dequeue order.
type QueuePriority int

const (
	PriorityNow   QueuePriority = 0 // Highest priority, processed first.
	PriorityNext  QueuePriority = 1 // Default for user commands.
	PriorityLater QueuePriority = 2 // Default for task notifications.
)

// QueuedCommand represents a queued command with priority and metadata.
type QueuedCommand struct {
	Value    string        // The command text or content.
	Mode     string        // e.g. "prompt", "task-notification".
	Priority QueuePriority // Dequeue priority.
	IsMeta   bool          // True for system-generated commands.
	AgentID  string        // Agent ID, empty for main thread.
	Origin   string        // Source of the command (e.g., "user", "coordinator").
}

// MessageQueue is a priority-ordered, thread-safe command queue.
type MessageQueue struct {
	mu        sync.Mutex
	commands  []*QueuedCommand
	listeners []func() // Change notification callbacks.
}

// NewMessageQueue creates a new empty message queue.
func NewMessageQueue() *MessageQueue {
	return &MessageQueue{
		commands: make([]*QueuedCommand, 0, 16),
	}
}

// Subscribe registers a callback invoked on every queue mutation.
// Returns an unsubscribe function.
func (q *MessageQueue) Subscribe(fn func()) func() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.listeners = append(q.listeners, fn)
	idx := len(q.listeners) - 1
	return func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if idx < len(q.listeners) {
			q.listeners = append(q.listeners[:idx], q.listeners[idx+1:]...)
		}
	}
}

// Enqueue adds a command to the queue with default priority "next".
func (q *MessageQueue) Enqueue(cmd *QueuedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if cmd.Priority == 0 && cmd.Mode != "task-notification" {
		cmd.Priority = PriorityNext
	}
	q.commands = append(q.commands, cmd)
	q.notifyLocked()
}

// EnqueueNotification adds a notification with default priority "later".
func (q *MessageQueue) EnqueueNotification(cmd *QueuedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if cmd.Priority == 0 {
		cmd.Priority = PriorityLater
	}
	q.commands = append(q.commands, cmd)
	q.notifyLocked()
}

// Dequeue removes and returns the highest-priority command (lowest priority number).
// Returns nil if the queue is empty.
func (q *MessageQueue) Dequeue() *QueuedCommand {
	return q.DequeueFiltered(nil)
}

// DequeueFiltered removes and returns the highest-priority command matching the filter.
// If filter is nil, all commands are considered.
func (q *MessageQueue) DequeueFiltered(filter func(*QueuedCommand) bool) *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.commands) == 0 {
		return nil
	}

	bestIdx := -1
	bestPriority := QueuePriority(100)

	for i, cmd := range q.commands {
		if filter != nil && !filter(cmd) {
			continue
		}
		if cmd.Priority < bestPriority {
			bestIdx = i
			bestPriority = cmd.Priority
		}
	}

	if bestIdx == -1 {
		return nil
	}

	dequeued := q.commands[bestIdx]
	q.commands = append(q.commands[:bestIdx], q.commands[bestIdx+1:]...)
	q.notifyLocked()
	return dequeued
}

// Peek returns the highest-priority command without removing it.
func (q *MessageQueue) Peek() *QueuedCommand {
	return q.PeekFiltered(nil)
}

// PeekFiltered returns the highest-priority command matching the filter without removing it.
func (q *MessageQueue) PeekFiltered(filter func(*QueuedCommand) bool) *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.commands) == 0 {
		return nil
	}

	bestIdx := -1
	bestPriority := QueuePriority(100)

	for i, cmd := range q.commands {
		if filter != nil && !filter(cmd) {
			continue
		}
		if cmd.Priority < bestPriority {
			bestIdx = i
			bestPriority = cmd.Priority
		}
	}

	if bestIdx == -1 {
		return nil
	}
	return q.commands[bestIdx]
}

// DequeueAll removes and returns all commands.
func (q *MessageQueue) DequeueAll() []*QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.commands) == 0 {
		return nil
	}

	result := make([]*QueuedCommand, len(q.commands))
	copy(result, q.commands)
	q.commands = q.commands[:0]
	q.notifyLocked()
	return result
}

// DequeueAllMatching removes and returns all commands matching the predicate.
func (q *MessageQueue) DequeueAllMatching(predicate func(*QueuedCommand) bool) []*QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()

	var matched []*QueuedCommand
	var remaining []*QueuedCommand

	for _, cmd := range q.commands {
		if predicate(cmd) {
			matched = append(matched, cmd)
		} else {
			remaining = append(remaining, cmd)
		}
	}

	if len(matched) == 0 {
		return nil
	}

	q.commands = remaining
	q.notifyLocked()
	return matched
}

// Remove removes specific commands from the queue by pointer identity.
func (q *MessageQueue) Remove(toRemove []*QueuedCommand) {
	if len(toRemove) == 0 {
		return
	}

	removeSet := make(map[*QueuedCommand]struct{}, len(toRemove))
	for _, cmd := range toRemove {
		removeSet[cmd] = struct{}{}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	before := len(q.commands)
	remaining := q.commands[:0]
	for _, cmd := range q.commands {
		if _, found := removeSet[cmd]; !found {
			remaining = append(remaining, cmd)
		}
	}
	q.commands = remaining

	if len(q.commands) != before {
		q.notifyLocked()
	}
}

// Clear removes all commands from the queue.
func (q *MessageQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.commands) == 0 {
		return
	}
	q.commands = q.commands[:0]
	q.notifyLocked()
}

// Len returns the current queue length.
func (q *MessageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.commands)
}

// IsEmpty returns true if the queue has no commands.
func (q *MessageQueue) IsEmpty() bool {
	return q.Len() == 0
}

// Snapshot returns a copy of the current queue for read-only inspection.
func (q *MessageQueue) Snapshot() []*QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	snap := make([]*QueuedCommand, len(q.commands))
	copy(snap, q.commands)
	return snap
}

// GetByMaxPriority returns commands at or above (lower number) the given priority.
func (q *MessageQueue) GetByMaxPriority(maxPriority QueuePriority) []*QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()

	var result []*QueuedCommand
	for _, cmd := range q.commands {
		if cmd.Priority <= maxPriority {
			result = append(result, cmd)
		}
	}
	return result
}

// BatchProcess dequeues and processes multiple messages using the provided handler.
// Stops on the first error, returning the number of successfully processed messages.
func (q *MessageQueue) BatchProcess(maxCount int, handler func(*QueuedCommand) error) (int, error) {
	processed := 0
	for processed < maxCount {
		cmd := q.Dequeue()
		if cmd == nil {
			break
		}
		if err := handler(cmd); err != nil {
			// Re-enqueue the failed command.
			q.Enqueue(cmd)
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (q *MessageQueue) notifyLocked() {
	for _, fn := range q.listeners {
		fn()
	}
}
