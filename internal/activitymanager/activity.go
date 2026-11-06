// Package activitymanager tracks user activity for idle detection and session
// management. It is a Go port of the original TypeScript activityManager.ts.
package activitymanager

import (
	"context"
	"sync"
	"time"
)

// Well-known activity event types.
const (
	TypeInput   = "input"
	TypeCommand = "command"
	TypeToolUse = "tool_use"
	TypeScroll  = "scroll"
)

// ────────────────────────────────────────────────────────────────────────────
// ActivityEvent
// ────────────────────────────────────────────────────────────────────────────

// ActivityEvent represents a single recorded activity event.
type ActivityEvent struct {
	Type      string
	Timestamp time.Time
}

// ────────────────────────────────────────────────────────────────────────────
// ActivityStats
// ────────────────────────────────────────────────────────────────────────────

// ActivityStats is a snapshot of the manager's accumulated metrics.
type ActivityStats struct {
	TotalEvents  int
	SessionStart time.Time
	LastActivity time.Time
	EventsByType map[string]int
}

// ────────────────────────────────────────────────────────────────────────────
// Manager
// ────────────────────────────────────────────────────────────────────────────

// Manager keeps an ordered list of activity events and provides idle-detection
// and session-level statistics. All methods are safe for concurrent use.
type Manager struct {
	mu            sync.Mutex
	events        []ActivityEvent
	lastActivity  time.Time
	idleThreshold time.Duration
	sessionStart  time.Time // zero value until the first RecordActivity call
	idleNotified  bool      // whether idle callbacks have already fired
	idleCallbacks []func()  // callbacks registered via OnIdle
}

// NewManager returns a Manager that considers the user idle after the given
// threshold elapses without any recorded activity.
func NewManager(idleThreshold time.Duration) *Manager {
	return &Manager{
		idleThreshold: idleThreshold,
	}
}

// RecordActivity registers an activity of the given type at the current time.
func (m *Manager) RecordActivity(eventType string) {
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	evt := ActivityEvent{
		Type:      eventType,
		Timestamp: now,
	}
	m.events = append(m.events, evt)
	m.lastActivity = now

	if m.sessionStart.IsZero() {
		m.sessionStart = now
	}

	// Reset idle notification so callbacks can fire again after new activity.
	m.idleNotified = false
}

// IsIdle reports whether the time elapsed since the last activity exceeds the
// configured idle threshold. If no activity has been recorded, it returns true.
func (m *Manager) IsIdle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastActivity.IsZero() {
		return true
	}
	return time.Since(m.lastActivity) > m.idleThreshold
}

// IdleDuration returns how long it has been since the last recorded activity.
// If no activity has been recorded, it returns 0.
func (m *Manager) IdleDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastActivity.IsZero() {
		return 0
	}
	return time.Since(m.lastActivity)
}

// SessionDuration returns the wall-clock time elapsed since the very first
// recorded activity. Returns 0 if no activity has been recorded.
func (m *Manager) SessionDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessionStart.IsZero() {
		return 0
	}
	return time.Since(m.sessionStart)
}

// OnIdle registers a callback that will be invoked when the user transitions
// to idle state. Multiple callbacks may be registered; they are all called
// when idle is detected. Callbacks fire exactly once per idle transition --
// new activity must be recorded before they can fire again.
func (m *Manager) OnIdle(callback func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idleCallbacks = append(m.idleCallbacks, callback)
}

// StartIdleWatcher starts a background goroutine that periodically checks
// whether the user has become idle. When the idle threshold is crossed, all
// registered OnIdle callbacks are invoked. The goroutine exits when ctx is
// cancelled.
func (m *Manager) StartIdleWatcher(ctx context.Context) {
	go func() {
		// Poll at 1/10 of the idle threshold for responsive detection,
		// but no faster than 50 ms and no slower than 1 s.
		poll := m.idleThreshold / 10
		if poll < 50*time.Millisecond {
			poll = 50 * time.Millisecond
		}
		if poll > time.Second {
			poll = time.Second
		}

		ticker := time.NewTicker(poll)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.Lock()
				idle := !m.lastActivity.IsZero() && time.Since(m.lastActivity) > m.idleThreshold
				alreadyNotified := m.idleNotified
				var callbacks []func()
				if idle && !alreadyNotified {
					m.idleNotified = true
					callbacks = make([]func(), len(m.idleCallbacks))
					copy(callbacks, m.idleCallbacks)
				}
				m.mu.Unlock()

				for _, cb := range callbacks {
					cb()
				}
			}
		}
	}()
}

// Stats returns a snapshot of the manager's accumulated metrics.
func (m *Manager) Stats() ActivityStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	byType := make(map[string]int, 4)
	for _, e := range m.events {
		byType[e.Type]++
	}

	return ActivityStats{
		TotalEvents:  len(m.events),
		SessionStart: m.sessionStart,
		LastActivity: m.lastActivity,
		EventsByType: byType,
	}
}
