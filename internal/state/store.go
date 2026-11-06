package state

import (
	"sync"
)

// Store is a generic reactive store that holds state and notifies subscribers
// when the state changes.
// Migrated from Claude Code's state/store.ts
type Store[T any] struct {
	mu          sync.RWMutex
	value       T
	subscribers []func(T)
	onChange    func(T, T) // optional: called with old and new value
}

// NewStore creates a new store with the given initial value.
func NewStore[T any](initialValue T, onChange func(T, T)) *Store[T] {
	return &Store[T]{
		value:    initialValue,
		onChange: onChange,
	}
}

// Get returns the current state value.
func (s *Store[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Set updates the state value and notifies all subscribers.
func (s *Store[T]) Set(newValue T) {
	s.mu.Lock()
	oldValue := s.value
	s.value = newValue
	s.mu.Unlock()

	if s.onChange != nil {
		s.onChange(oldValue, newValue)
	}

	s.mu.RLock()
	subscribers := s.subscribers
	s.mu.RUnlock()

	for _, sub := range subscribers {
		sub(newValue)
	}
}

// Subscribe registers a function to be called whenever the state changes.
// Returns an unsubscribe function.
func (s *Store[T]) Subscribe(subscriber func(T)) func() {
	s.mu.Lock()
	id := len(s.subscribers)
	s.subscribers = append(s.subscribers, subscriber)
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if id < len(s.subscribers) {
			s.subscribers = append(s.subscribers[:id], s.subscribers[id+1:]...)
		}
	}
}

// replace is an internal helper to replace the state with a function result.
func (s *Store[T]) replace(updater func(T) T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldValue := s.value
	newValue := updater(s.value)
	s.value = newValue

	if s.onChange != nil {
		s.onChange(oldValue, newValue)
	}

	s.mu.RLock()
	subscribers := s.subscribers
	s.mu.RUnlock()

	for _, sub := range subscribers {
		sub(newValue)
	}
}

// Update applies an updater function to the current state.
func (s *Store[T]) Update(updater func(T) T) {
	s.replace(updater)
}
