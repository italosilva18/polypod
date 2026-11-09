package tasks

import (
	"context"
	"sync"
)

// TaskEvent records a task entering a terminal state. State is a snapshot and
// can be read without holding the registry lock.
type TaskEvent struct {
	State TaskState
}

type taskSubscriber struct {
	mu    sync.Mutex
	queue []TaskEvent
	head  int
	wake  chan struct{}
	out   chan TaskEvent
}

func (s *taskSubscriber) enqueue(event TaskEvent) {
	s.mu.Lock()
	s.queue = append(s.queue, event)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *taskSubscriber) next() (TaskEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.head == len(s.queue) {
		return TaskEvent{}, false
	}
	event := s.queue[s.head]
	s.queue[s.head] = TaskEvent{}
	s.head++
	if s.head == len(s.queue) {
		s.queue = nil
		s.head = 0
	} else if s.head >= 128 && s.head*2 >= len(s.queue) {
		s.queue = append([]TaskEvent(nil), s.queue[s.head:]...)
		s.head = 0
	}
	return event, true
}

func (s *taskSubscriber) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		event, ok := s.next()
		if !ok {
			select {
			case <-s.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		select {
		case s.out <- event:
		case <-ctx.Done():
			return
		}
	}
}

// Subscribe streams terminal task transitions until ctx is canceled. Each
// subscriber has its own queue, so a TUI that stops reading cannot block task
// state changes. Cancel ctx when the caller stops consuming the channel.
func (r *TaskRegistry) Subscribe(ctx context.Context) <-chan TaskEvent {
	sub := &taskSubscriber{
		wake: make(chan struct{}, 1),
		out:  make(chan TaskEvent, 16),
	}
	r.mu.Lock()
	if r.subscribers == nil {
		r.subscribers = make(map[*taskSubscriber]struct{})
	}
	r.subscribers[sub] = struct{}{}
	r.mu.Unlock()

	go func() {
		sub.run(ctx)
		r.mu.Lock()
		delete(r.subscribers, sub)
		r.mu.Unlock()
		close(sub.out)
	}()
	return sub.out
}

// publishTerminalLocked is called with r.mu held. It never waits for the TUI.
func (r *TaskRegistry) publishTerminalLocked(state *TaskState) {
	event := TaskEvent{State: *snapshotTaskState(state)}
	for sub := range r.subscribers {
		sub.enqueue(event)
	}
}
