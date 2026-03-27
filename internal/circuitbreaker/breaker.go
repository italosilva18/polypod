package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal operation
	StateOpen                  // Failing, reject requests
	StateHalfOpen              // Testing recovery
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker implements the circuit breaker pattern for a single provider.
type Breaker struct {
	mu             sync.Mutex
	state          State
	failures       int
	successes      int
	maxFailures    int           // trips to Open after this many consecutive failures
	resetTimeout   time.Duration // time in Open before trying Half-Open
	halfOpenMax    int           // successes needed in Half-Open to close
	lastFailure    time.Time
	lastStateChange time.Time
}

// New creates a circuit breaker with sensible defaults.
func New() *Breaker {
	return &Breaker{
		state:        StateClosed,
		maxFailures:  3,
		resetTimeout: 30 * time.Second,
		halfOpenMax:  2,
	}
}

// NewWithConfig creates a circuit breaker with custom settings.
func NewWithConfig(maxFailures int, resetTimeout time.Duration, halfOpenMax int) *Breaker {
	return &Breaker{
		state:        StateClosed,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		halfOpenMax:  halfOpenMax,
	}
}

// Allow checks if a request should be allowed through.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if enough time has passed to try Half-Open
		if time.Since(b.lastFailure) >= b.resetTimeout {
			b.state = StateHalfOpen
			b.successes = 0
			b.lastStateChange = time.Now()
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful request.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0

	if b.state == StateHalfOpen {
		b.successes++
		if b.successes >= b.halfOpenMax {
			b.state = StateClosed
			b.lastStateChange = time.Now()
		}
	}
}

// RecordFailure records a failed request.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	b.lastFailure = time.Now()

	switch b.state {
	case StateClosed:
		if b.failures >= b.maxFailures {
			b.state = StateOpen
			b.lastStateChange = time.Now()
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.lastStateChange = time.Now()
	}
}

// State returns the current state.
func (b *Breaker) GetState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Reset forces the breaker to Closed state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.failures = 0
	b.successes = 0
	b.lastStateChange = time.Now()
}

// Stats returns a human-readable status.
func (b *Breaker) Stats() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("state=%s failures=%d successes=%d last_failure=%s",
		b.state, b.failures, b.successes, b.lastFailure.Format("15:04:05"))
}

// Registry manages circuit breakers for multiple providers.
type Registry struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
}

// NewRegistry creates a breaker registry.
func NewRegistry() *Registry {
	return &Registry{breakers: make(map[string]*Breaker)}
}

// Get returns the breaker for a provider, creating one if needed.
func (r *Registry) Get(provider string) *Breaker {
	r.mu.RLock()
	b, ok := r.breakers[provider]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	b = New()
	r.breakers[provider] = b
	return b
}

// Stats returns status of all breakers.
func (r *Registry) Stats() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := make(map[string]string)
	for name, b := range r.breakers {
		stats[name] = b.Stats()
	}
	return stats
}
