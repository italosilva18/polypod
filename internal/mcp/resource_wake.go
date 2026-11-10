package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultResourceWakeInterval = 30 * time.Second
	resourceWakeTimeout         = 15 * time.Second
	resourceWakeMaxRetry        = time.Minute
	resourceWakeReconcileEvery  = 10 * time.Second
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
	// storePath is configured before the handler starts. mu protects it and
	// every in-memory view of the durable pending snapshot.
	storePath string
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
		// The handler can be disabled temporarily. Accepted wakes remain on
		// disk and in memory so re-enabling it does not discard them.
		if d.storePath == "" {
			clear(d.pending)
			clear(d.nextRetry)
			clear(d.failures)
			clear(d.generation)
		}
	}
	d.mu.Unlock()
	d.wake()
}

// notify persists an accepted wake before returning to the SDK callback.
// A storage error prevents the in-memory dispatcher from treating the wake
// as accepted; the callback reports that error to the operational log.
func (d *resourceWakeDispatcher) notify(update ResourceUpdate) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.handler == nil {
		return nil
	}
	key := resourceWakeKey(update.Server, update.URI)
	if d.storePath != "" {
		var queue []resourceWakeRecord
		err := updateResourceWakeFile(d.storePath, func(state *resourceWakeFile) (bool, error) {
			items := state.Pending[key]
			if len(items) == 0 || items[len(items)-1].HandoffStarted {
				if len(items) >= 2 {
					return false, fmt.Errorf("mcp: fila de notificacoes do recurso cheia")
				}
				update.WakeID = "task_" + uuid.NewString()
				update.WakeRevision = 1
				items = append(items, resourceWakeRecord{ResourceUpdate: update})
			} else {
				last := len(items) - 1
				pending := items[last].ResourceUpdate
				update = latestWake(update, pending)
				update.WakeID = pending.WakeID
				if pending.WakeRevision == ^uint64(0) {
					return false, fmt.Errorf("mcp: revisao de notificacao de recurso esgotada")
				}
				update.WakeRevision = pending.WakeRevision + 1
				items[last].ResourceUpdate = update
			}
			state.Pending[key] = items
			queue = append([]resourceWakeRecord(nil), items...)
			return true, nil
		})
		if err != nil {
			return fmt.Errorf("mcp: salvando notificacao de recurso: %w", err)
		}
		d.adoptQueueHeadLocked(key, queue)
	} else if pending, ok := d.pending[key]; ok {
		update.WakeID = pending.WakeID
		d.pending[key] = update
	} else {
		update.WakeID = "task_" + uuid.NewString()
		d.pending[key] = update
		d.nextGen++
		d.generation[key] = d.nextGen
	}
	d.wake()
	return nil
}

func (d *resourceWakeDispatcher) adoptQueueHeadLocked(key string, queue []resourceWakeRecord) {
	if len(queue) == 0 {
		delete(d.pending, key)
		delete(d.generation, key)
		delete(d.nextRetry, key)
		delete(d.failures, key)
		return
	}
	head := queue[0].ResourceUpdate
	current, exists := d.pending[key]
	if !exists || current.WakeID != head.WakeID || current.WakeRevision != head.WakeRevision {
		d.nextGen++
		d.generation[key] = d.nextGen
		delete(d.nextRetry, key)
		delete(d.failures, key)
	}
	d.pending[key] = head
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

// markHandoff commits the dispatch boundary before a queue call. A later
// notification observes this marker and is assigned a successor WakeID. If
// another process changed the head, the local snapshot is refreshed instead.
func (d *resourceWakeDispatcher) markHandoff(key string, update ResourceUpdate, generation uint64) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.generation[key] != generation || d.pending[key].WakeID != update.WakeID {
		return false, nil
	}
	if d.storePath == "" {
		return true, nil
	}
	var queue []resourceWakeRecord
	err := updateResourceWakeFile(d.storePath, func(state *resourceWakeFile) (bool, error) {
		items := state.Pending[key]
		queue = append([]resourceWakeRecord(nil), items...)
		if len(items) == 0 || items[0].WakeID != update.WakeID || items[0].WakeRevision != update.WakeRevision {
			return false, nil
		}
		if items[0].HandoffStarted {
			return false, nil
		}
		items[0].HandoffStarted = true
		state.Pending[key] = items
		queue = append([]resourceWakeRecord(nil), items...)
		return true, nil
	})
	if err != nil {
		return false, fmt.Errorf("mcp: persistindo inicio de entrega: %w", err)
	}
	d.adoptQueueHeadLocked(key, queue)
	return len(queue) > 0 && queue[0].WakeID == update.WakeID && queue[0].WakeRevision == update.WakeRevision, nil
}

// reconcile adopts wakes created by another Polypod process. A process that
// survives a CLI crash can then hand off that CLI's persisted notifications.
func (d *resourceWakeDispatcher) reconcile() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.storePath == "" {
		return nil
	}
	var state resourceWakeFile
	if err := withResourceWakeFileLock(d.storePath, func() error {
		var readErr error
		state, _, readErr = readResourceWakeFile(d.storePath)
		return readErr
	}); err != nil {
		return fmt.Errorf("mcp: reconciliando notificacoes de recursos: %w", err)
	}
	for key, queue := range state.Pending {
		d.adoptQueueHeadLocked(key, queue)
	}
	for key := range d.pending {
		if _, exists := state.Pending[key]; !exists {
			d.adoptQueueHeadLocked(key, nil)
		}
	}
	return nil
}

func (d *resourceWakeDispatcher) complete(key string, update ResourceUpdate, generation uint64, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.generation[key] != generation {
		return
	}
	now := time.Now()
	if err == nil {
		if d.storePath != "" {
			var queue []resourceWakeRecord
			persistErr := updateResourceWakeFile(d.storePath, func(state *resourceWakeFile) (bool, error) {
				items := state.Pending[key]
				queue = append([]resourceWakeRecord(nil), items...)
				if len(items) == 0 || items[0].WakeID != update.WakeID {
					return false, nil
				}
				if items[0].WakeRevision != update.WakeRevision {
					return false, fmt.Errorf("mcp: revisao do wake mudou durante a entrega")
				}
				items = items[1:]
				if len(items) == 0 {
					delete(state.Pending, key)
				} else {
					state.Pending[key] = items
				}
				queue = append([]resourceWakeRecord(nil), items...)
				return true, nil
			})
			if persistErr != nil {
				d.retryLocked(key, update, now, fmt.Errorf("mcp: registrando aceite do wake: %w", persistErr))
				return
			}
			d.lastAccepted[key] = now
			delete(d.nextRetry, key)
			delete(d.failures, key)
			d.adoptQueueHeadLocked(key, queue)
			return
		}
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
	d.retryLocked(key, update, now, err)
}

func (d *resourceWakeDispatcher) retryLocked(key string, update ResourceUpdate, now time.Time, err error) {
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
	ticker := time.NewTicker(resourceWakeReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := d.reconcile(); err != nil {
				d.logger.Warn("mcp resource wake reconciliation failed", "error", err)
			}
		default:
		}
		key, update, generation, handler, wait := d.next(time.Now())
		if handler == nil {
			select {
			case <-ctx.Done():
				return
			case <-d.signal:
				continue
			case <-ticker.C:
				if err := d.reconcile(); err != nil {
					d.logger.Warn("mcp resource wake reconciliation failed", "error", err)
				}
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
			case <-ticker.C:
				timer.Stop()
				if err := d.reconcile(); err != nil {
					d.logger.Warn("mcp resource wake reconciliation failed", "error", err)
				}
				continue
			case <-timer.C:
				continue
			}
		}
		marked, markErr := d.markHandoff(key, update, generation)
		if markErr != nil {
			d.mu.Lock()
			if d.generation[key] == generation {
				d.retryLocked(key, update, time.Now(), markErr)
			}
			d.mu.Unlock()
			continue
		}
		if !marked {
			continue
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
