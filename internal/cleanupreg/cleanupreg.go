// Package cleanupreg provides a graceful shutdown registry for cleanup callbacks.
package cleanupreg

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"sync"
	"time"
)

// CleanupFn is a cleanup callback.
type CleanupFn func(ctx context.Context) error

// Entry is a registered cleanup entry.
type Entry struct {
	Name     string
	Priority int           // Higher priority runs first
	Timeout  time.Duration // Per-callback timeout (0 = use registry default)
	Group    string        // Optional group label
	fn       CleanupFn
}

// Registry holds cleanup callbacks.
type Registry struct {
	mu             sync.Mutex
	entries        []Entry
	defaultTimeout time.Duration
	onError        func(name string, err error)
	didRun         bool
}

// NewRegistry creates a new cleanup registry.
func NewRegistry() *Registry {
	return &Registry{
		defaultTimeout: 5 * time.Second,
	}
}

// SetDefaultTimeout sets the default per-callback timeout.
func (r *Registry) SetDefaultTimeout(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultTimeout = d
}

// SetErrorHandler sets a callback called when a cleanup fails.
func (r *Registry) SetErrorHandler(fn func(name string, err error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onError = fn
}

// Add registers a cleanup callback.
func (r *Registry) Add(name string, priority int, fn CleanupFn) {
	r.AddWithOptions(Entry{
		Name:     name,
		Priority: priority,
		fn:       fn,
	})
}

// AddWithOptions registers a cleanup with full options.
func (r *Registry) AddWithOptions(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.fn == nil {
		return
	}
	r.entries = append(r.entries, e)
}

// AddSimple registers a no-context cleanup.
func (r *Registry) AddSimple(name string, fn func() error) {
	r.Add(name, 0, func(ctx context.Context) error {
		return fn()
	})
}

// AddWithGroup registers a cleanup with a group label.
func (r *Registry) AddWithGroup(name, group string, priority int, fn CleanupFn) {
	r.AddWithOptions(Entry{
		Name:     name,
		Priority: priority,
		Group:    group,
		fn:       fn,
	})
}

// Remove deletes all entries with the given name.
func (r *Registry) Remove(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	survivors := r.entries[:0]
	removed := 0
	for _, e := range r.entries {
		if e.Name == name {
			removed++
			continue
		}
		survivors = append(survivors, e)
	}
	r.entries = survivors
	return removed
}

// RemoveGroup deletes all entries in the given group.
func (r *Registry) RemoveGroup(group string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	survivors := r.entries[:0]
	removed := 0
	for _, e := range r.entries {
		if e.Group == group {
			removed++
			continue
		}
		survivors = append(survivors, e)
	}
	r.entries = survivors
	return removed
}

// Len returns the number of registered cleanups.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// List returns a copy of the entries (without the fn field exposed).
func (r *Registry) List() []EntryInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]EntryInfo, len(r.entries))
	for i, e := range r.entries {
		out[i] = EntryInfo{
			Name:     e.Name,
			Priority: e.Priority,
			Group:    e.Group,
			Timeout:  e.Timeout,
		}
	}
	return out
}

// EntryInfo describes a registered entry without the function reference.
type EntryInfo struct {
	Name     string
	Priority int
	Group    string
	Timeout  time.Duration
}

// Run executes all cleanups in priority order (highest first).
// Each runs with its own timeout. Returns errors collected from all callbacks.
// Subsequent calls are no-ops.
func (r *Registry) Run(ctx context.Context, totalTimeout time.Duration) []error {
	r.mu.Lock()
	if r.didRun {
		r.mu.Unlock()
		return nil
	}
	r.didRun = true

	// Snapshot entries
	entries := make([]Entry, len(r.entries))
	copy(entries, r.entries)
	defaultTimeout := r.defaultTimeout
	onError := r.onError
	r.mu.Unlock()

	// Sort by priority desc, then registration order
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Priority > entries[j].Priority
	})

	if totalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, totalTimeout)
		defer cancel()
	}

	var errs []error
	for _, e := range entries {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Errorf("cleanup '%s' skipped: %w", e.Name, ctx.Err()))
			continue
		}

		timeout := e.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}

		cbCtx, cancel := context.WithTimeout(ctx, timeout)
		err := safeCall(cbCtx, e.fn)
		cancel()

		if err != nil {
			wrapped := fmt.Errorf("cleanup '%s': %w", e.Name, err)
			errs = append(errs, wrapped)
			if onError != nil {
				onError(e.Name, err)
			}
		}
	}

	return errs
}

// RunOnSignals registers a goroutine that runs cleanups when any signal is received.
// Returns a stop function that disarms the signal handler.
func (r *Registry) RunOnSignals(totalTimeout time.Duration, sigs ...os.Signal) (stop func()) {
	if len(sigs) == 0 {
		sigs = []os.Signal{os.Interrupt}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})

	go func() {
		select {
		case <-ch:
			ctx := context.Background()
			r.Run(ctx, totalTimeout)
		case <-done:
		}
	}()

	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// Reset clears the run-once flag (for testing).
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.didRun = false
}

// safeCall wraps a callback in a panic recovery.
func safeCall(ctx context.Context, fn CleanupFn) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during cleanup: %v", r)
		}
	}()
	return fn(ctx)
}

// ===== Default global registry =====

var defaultRegistry = NewRegistry()

// Default returns the global default registry.
func Default() *Registry {
	return defaultRegistry
}

// Add adds a cleanup to the default registry.
func Add(name string, priority int, fn CleanupFn) {
	defaultRegistry.Add(name, priority, fn)
}

// AddSimple adds a simple cleanup to the default registry.
func AddSimple(name string, fn func() error) {
	defaultRegistry.AddSimple(name, fn)
}

// Run runs the default registry.
func Run(ctx context.Context, totalTimeout time.Duration) []error {
	return defaultRegistry.Run(ctx, totalTimeout)
}
