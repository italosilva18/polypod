package session

import (
	"log/slog"
	"sync"
	"time"
)

// ActivityReason describes why activity was started.
type ActivityReason string

const (
	ActivityAPICall  ActivityReason = "api_call"
	ActivityToolExec ActivityReason = "tool_exec"
	ActivityPrompt   ActivityReason = "prompt_submitted"
	ActivityMessage  ActivityReason = "message_received"
)

// ActivityEvent records a single activity occurrence.
type ActivityEvent struct {
	Reason    ActivityReason `json:"reason"`
	Timestamp time.Time      `json:"timestamp"`
}

// ActivityManager tracks user activity timestamps and manages heartbeat/idle
// detection using a refcount-based approach. When the refcount transitions
// from 0→1, a periodic heartbeat timer fires. When it drops to 0, an idle
// timer starts.
type ActivityManager struct {
	mu sync.Mutex

	// Heartbeat callback — called periodically while active.
	callback func()

	// Refcount and per-reason counts.
	refcount      int
	activeReasons map[ActivityReason]int

	// Timestamps.
	oldestActivityStart time.Time
	lastActivityTime    time.Time

	// Timers.
	heartbeatInterval time.Duration
	idleTimeout       time.Duration
	heartbeatTicker   *time.Ticker
	heartbeatStop     chan struct{}
	idleTimer         *time.Timer

	// Activity history for analytics.
	history    []ActivityEvent
	maxHistory int

	logger *slog.Logger
}

// ActivityManagerConfig configures the activity manager.
type ActivityManagerConfig struct {
	HeartbeatInterval time.Duration
	IdleTimeout       time.Duration
	MaxHistory        int
	Logger            *slog.Logger
}

// NewActivityManager creates an ActivityManager with the given configuration.
func NewActivityManager(cfg ActivityManagerConfig) *ActivityManager {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.MaxHistory <= 0 {
		cfg.MaxHistory = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &ActivityManager{
		activeReasons:     make(map[ActivityReason]int),
		heartbeatInterval: cfg.HeartbeatInterval,
		idleTimeout:       cfg.IdleTimeout,
		history:           make([]ActivityEvent, 0, cfg.MaxHistory),
		maxHistory:        cfg.MaxHistory,
		logger:            cfg.Logger,
	}
}

// RegisterCallback sets the heartbeat callback. If activity is already in
// progress, the heartbeat timer starts immediately.
func (am *ActivityManager) RegisterCallback(cb func()) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.callback = cb
	if am.refcount > 0 && am.heartbeatTicker == nil {
		am.startHeartbeat()
	}
}

// UnregisterCallback removes the heartbeat callback and stops all timers.
func (am *ActivityManager) UnregisterCallback() {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.callback = nil
	am.stopHeartbeat()
	am.stopIdleTimer()
}

// StartActivity increments the activity refcount. When transitioning from
// 0→1 and a callback is registered, a periodic heartbeat timer starts.
func (am *ActivityManager) StartActivity(reason ActivityReason) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.refcount++
	am.activeReasons[reason]++
	am.lastActivityTime = time.Now()

	am.recordEvent(reason)

	if am.refcount == 1 {
		am.oldestActivityStart = time.Now()
		am.stopIdleTimer()
		if am.callback != nil && am.heartbeatTicker == nil {
			am.startHeartbeat()
		}
	}
}

// StopActivity decrements the activity refcount. When it reaches 0, the
// heartbeat timer stops and an idle timer starts.
func (am *ActivityManager) StopActivity(reason ActivityReason) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.refcount > 0 {
		am.refcount--
	}
	n := am.activeReasons[reason] - 1
	if n > 0 {
		am.activeReasons[reason] = n
	} else {
		delete(am.activeReasons, reason)
	}

	if am.refcount == 0 {
		am.stopHeartbeat()
		am.startIdleTimerLocked()
	}
}

// SendSignal fires the callback once (outside the heartbeat cycle).
func (am *ActivityManager) SendSignal() {
	am.mu.Lock()
	cb := am.callback
	am.mu.Unlock()

	if cb != nil {
		cb()
	}
}

// IsActive returns true if there are in-flight activity holders (refcount > 0).
func (am *ActivityManager) IsActive() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.refcount > 0
}

// IsIdle returns true if no activity is in progress and the idle timeout
// has elapsed since the last activity.
func (am *ActivityManager) IsIdle() bool {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.refcount > 0 {
		return false
	}
	if am.lastActivityTime.IsZero() {
		return true
	}
	return time.Since(am.lastActivityTime) >= am.idleTimeout
}

// IsTrackingActive returns true if a heartbeat callback is registered.
func (am *ActivityManager) IsTrackingActive() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.callback != nil
}

// Refcount returns the current activity refcount.
func (am *ActivityManager) Refcount() int {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.refcount
}

// ActiveReasons returns a copy of the per-reason refcounts.
func (am *ActivityManager) ActiveReasons() map[ActivityReason]int {
	am.mu.Lock()
	defer am.mu.Unlock()
	out := make(map[ActivityReason]int, len(am.activeReasons))
	for k, v := range am.activeReasons {
		out[k] = v
	}
	return out
}

// LastActivityTime returns the timestamp of the most recent activity start.
func (am *ActivityManager) LastActivityTime() time.Time {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.lastActivityTime
}

// OldestActivityDuration returns how long the oldest in-flight activity has
// been running. Returns 0 if nothing is active.
func (am *ActivityManager) OldestActivityDuration() time.Duration {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.refcount == 0 || am.oldestActivityStart.IsZero() {
		return 0
	}
	return time.Since(am.oldestActivityStart)
}

// History returns a copy of the recorded activity events.
func (am *ActivityManager) History() []ActivityEvent {
	am.mu.Lock()
	defer am.mu.Unlock()
	out := make([]ActivityEvent, len(am.history))
	copy(out, am.history)
	return out
}

// Shutdown stops all timers and clears state.
func (am *ActivityManager) Shutdown() {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.logger.Info("session_activity_at_shutdown",
		"refcount", am.refcount,
		"active_reasons", am.activeReasons,
	)
	am.stopHeartbeat()
	am.stopIdleTimer()
	am.callback = nil
	am.refcount = 0
	am.activeReasons = make(map[ActivityReason]int)
}

// --- internal helpers (must be called with mu held) ---

func (am *ActivityManager) recordEvent(reason ActivityReason) {
	evt := ActivityEvent{
		Reason:    reason,
		Timestamp: time.Now(),
	}
	if len(am.history) >= am.maxHistory {
		// Evict oldest quarter to avoid constant shifting.
		n := am.maxHistory / 4
		copy(am.history, am.history[n:])
		am.history = am.history[:len(am.history)-n]
	}
	am.history = append(am.history, evt)
}

func (am *ActivityManager) startHeartbeat() {
	am.stopIdleTimer()
	am.heartbeatStop = make(chan struct{})
	am.heartbeatTicker = time.NewTicker(am.heartbeatInterval)
	stop := am.heartbeatStop
	ticker := am.heartbeatTicker
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				am.mu.Lock()
				cb := am.callback
				rc := am.refcount
				am.mu.Unlock()

				am.logger.Debug("session_keepalive_heartbeat", "refcount", rc)
				if cb != nil {
					cb()
				}
			}
		}
	}()
}

func (am *ActivityManager) stopHeartbeat() {
	if am.heartbeatTicker != nil {
		am.heartbeatTicker.Stop()
		am.heartbeatTicker = nil
	}
	if am.heartbeatStop != nil {
		close(am.heartbeatStop)
		am.heartbeatStop = nil
	}
}

func (am *ActivityManager) startIdleTimerLocked() {
	am.stopIdleTimer()
	if am.callback == nil {
		return
	}
	am.idleTimer = time.AfterFunc(am.idleTimeout, func() {
		am.logger.Info("session_idle", "timeout", am.idleTimeout)
	})
}

func (am *ActivityManager) stopIdleTimer() {
	if am.idleTimer != nil {
		am.idleTimer.Stop()
		am.idleTimer = nil
	}
}
