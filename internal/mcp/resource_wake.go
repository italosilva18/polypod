package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultResourceWakeInterval = 30 * time.Second
	resourceWakeTimeout         = 15 * time.Second
	resourceWakeMaxRetry        = time.Minute
)

// ResourceUpdateHandler persists a wake for a subscribed resource. Returning
// nil means the wake has been accepted by a durable queue, not that the agent
// has finished processing it. A failed handoff is retried while the Manager
// remains open.
type ResourceUpdateHandler func(context.Context, ResourceUpdate) error

// ResourceWakeOptions controls how often repeated updates to the same URI can
// start a new agent task. A zero MinInterval uses the default of 30 seconds.
type ResourceWakeOptions struct {
	MinInterval time.Duration
}

// SetResourceUpdateHandler enables agent wake-ups for resources explicitly
// subscribed through SubscribeResource. The handler runs outside the MCP
// notification callback and Manager lock. A nil handler disables wake-ups.
func (m *Manager) SetResourceUpdateHandler(handler ResourceUpdateHandler, options ResourceWakeOptions) {
	m.resourceWake.setHandler(handler, options)
}

type resourceWakeDispatcher struct {
	mu           sync.Mutex
	handler      ResourceUpdateHandler
	minInterval  time.Duration
	pending      map[string]ResourceUpdate
	lastAccepted map[string]time.Time
	nextRetry    map[string]time.Time
	failures     map[string]int
	generation   map[string]uint64
	nextGen      uint64
	signal       chan struct{}
	cancel       context.CancelFunc
	done         chan struct{}
	closed       bool
	logger       *slog.Logger
}

func newResourceWakeDispatcher(logger *slog.Logger) *resourceWakeDispatcher {
	return &resourceWakeDispatcher{
		minInterval:  defaultResourceWakeInterval,
		pending:      make(map[string]ResourceUpdate),
		lastAccepted: make(map[string]time.Time),
		nextRetry:    make(map[string]time.Time),
		failures:     make(map[string]int),
		generation:   make(map[string]uint64),
		signal:       make(chan struct{}, 1),
		logger:       logger,
	}
}

func resourceWakeKey(server, uri string) string { return server + "\x00" + uri }

func (d *resourceWakeDispatcher) setHandler(handler ResourceUpdateHandler, options ResourceWakeOptions) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.handler = handler
	if options.MinInterval > 0 {
		d.minInterval = options.MinInterval
	} else {
		d.minInterval = defaultResourceWakeInterval
	}
	if handler != nil && d.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		d.cancel = cancel
		d.done = make(chan struct{})
		go d.loop(ctx, d.done)
	}
	if handler == nil {
		clear(d.pending)
		clear(d.nextRetry)
		clear(d.failures)
		clear(d.generation)
	}
	d.mu.Unlock()
	d.wake()
}

func (d *resourceWakeDispatcher) notify(update ResourceUpdate) {
	d.mu.Lock()
	if d.closed || d.handler == nil {
		d.mu.Unlock()
		return
	}
	key := resourceWakeKey(update.Server, update.URI)
	if pending, ok := d.pending[key]; ok {
		update.WakeID = pending.WakeID
	} else {
		update.WakeID = "task_" + uuid.NewString()
		d.nextGen++
		d.generation[key] = d.nextGen
	}
	d.pending[key] = update
	d.mu.Unlock()
	d.wake()
}

func (d *resourceWakeDispatcher) drop(server, uri string) {
	key := resourceWakeKey(server, uri)
	d.mu.Lock()
	delete(d.pending, key)
	delete(d.lastAccepted, key)
	delete(d.nextRetry, key)
	delete(d.failures, key)
	delete(d.generation, key)
	d.mu.Unlock()
	d.wake()
}

func (d *resourceWakeDispatcher) dropServer(server string) {
	d.mu.Lock()
	for key, update := range d.pending {
		if update.Server != server {
			continue
		}
		delete(d.pending, key)
		delete(d.lastAccepted, key)
		delete(d.nextRetry, key)
		delete(d.failures, key)
		delete(d.generation, key)
	}
	d.mu.Unlock()
	d.wake()
}

func (d *resourceWakeDispatcher) wake() {
	select {
	case d.signal <- struct{}{}:
	default:
	}
}

func (d *resourceWakeDispatcher) next(now time.Time) (string, ResourceUpdate, uint64, ResourceUpdateHandler, time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handler == nil || len(d.pending) == 0 {
		return "", ResourceUpdate{}, 0, nil, 0
	}
	var selected string
	var selectedDue time.Time
	for key := range d.pending {
		due := d.lastAccepted[key].Add(d.minInterval)
		if retry := d.nextRetry[key]; retry.After(due) {
			due = retry
		}
		if selected == "" || due.Before(selectedDue) {
			selected, selectedDue = key, due
		}
	}
	return selected, d.pending[selected], d.generation[selected], d.handler, max(time.Duration(0), selectedDue.Sub(now))
}

func (d *resourceWakeDispatcher) complete(key string, update ResourceUpdate, generation uint64, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.generation[key] != generation {
		return
	}
	now := time.Now()
	if err == nil {
		d.lastAccepted[key] = now
		delete(d.nextRetry, key)
		delete(d.failures, key)
		if current, ok := d.pending[key]; ok {
			if current.At.After(update.At) {
				current.WakeID = "task_" + uuid.NewString()
				d.pending[key] = current
			} else {
				delete(d.pending, key)
				delete(d.generation, key)
			}
		}
		return
	}
	d.failures[key]++
	backoff := time.Second << min(d.failures[key], 6)
	if backoff > resourceWakeMaxRetry {
		backoff = resourceWakeMaxRetry
	}
	d.nextRetry[key] = now.Add(backoff)
	d.logger.Warn("mcp resource wake handoff failed; retry scheduled", "server", update.Server, "error", err, "retry_in", backoff)
}

func (d *resourceWakeDispatcher) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		key, update, generation, handler, wait := d.next(time.Now())
		if handler == nil {
			select {
			case <-ctx.Done():
				return
			case <-d.signal:
				continue
			}
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-d.signal:
				timer.Stop()
				continue
			case <-timer.C:
				continue
			}
		}
		handoffCtx, cancel := context.WithTimeout(ctx, resourceWakeTimeout)
		err := handler(handoffCtx, update)
		cancel()
		d.complete(key, update, generation, err)
	}
}

func (d *resourceWakeDispatcher) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
