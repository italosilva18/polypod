package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// connState holds per-connection cancel function and a done channel.
type connState struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// ReconnectManager manages reconnection and optional heartbeat for named connections.
type ReconnectManager struct {
	cfg    ReconnectConfig
	logger *slog.Logger

	mu    sync.Mutex
	conns map[string]*connState
}

// NewReconnectManager creates a new ReconnectManager with the given config.
func NewReconnectManager(cfg ReconnectConfig) *ReconnectManager {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ReconnectManager{
		cfg:    cfg,
		logger: logger,
		conns:  make(map[string]*connState),
	}
}

// Connect attempts to establish a connection by calling dialFn, retrying with
// exponential backoff up to cfg.MaxAttempts times (0 = unlimited). The context
// passed to dialFn is derived from ctx and will be cancelled when Stop or
// StopAll is called for this name.
func (rm *ReconnectManager) Connect(ctx context.Context, name string, dialFn func(ctx context.Context) error) error {
	// Cancel any existing goroutine for this name before starting fresh.
	rm.stopName(name)

	connCtx, cancel := context.WithCancel(ctx)
	state := &connState{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	rm.mu.Lock()
	rm.conns[name] = state
	rm.mu.Unlock()

	defer close(state.done)

	backoff := rm.cfg.InitialBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	multiplier := rm.cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}
	maxBackoff := rm.cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}

	attempt := 0
	for {
		attempt++

		rm.logger.Info("mcp: connecting", "name", name, "attempt", attempt)
		err := dialFn(connCtx)
		if err == nil {
			rm.logger.Info("mcp: connected", "name", name, "attempt", attempt)
			return nil
		}

		// Check if our context was cancelled.
		if connCtx.Err() != nil {
			rm.logger.Info("mcp: connect cancelled", "name", name)
			return connCtx.Err()
		}

		rm.logger.Warn("mcp: dial failed", "name", name, "attempt", attempt, "err", err)

		if rm.cfg.MaxAttempts > 0 && attempt >= rm.cfg.MaxAttempts {
			return fmt.Errorf("mcp: %s: dial failed after %d attempts: %w", name, attempt, err)
		}

		// Wait for backoff duration or context cancellation.
		select {
		case <-connCtx.Done():
			rm.logger.Info("mcp: connect cancelled during backoff", "name", name)
			return connCtx.Err()
		case <-time.After(backoff):
		}

		// Grow backoff exponentially up to maxBackoff.
		next := float64(backoff) * multiplier
		backoff = time.Duration(math.Min(next, float64(maxBackoff)))
	}
}

// StartHeartbeat launches a goroutine that pings the named connection at
// cfg.HeartbeatInterval. On ping failure it calls onFailure(name) and then
// attempts to reconnect via Connect. If HeartbeatInterval is 0 this is a no-op.
func (rm *ReconnectManager) StartHeartbeat(
	ctx context.Context,
	name string,
	pingFn func(ctx context.Context) error,
	onFailure func(name string),
) {
	if rm.cfg.HeartbeatInterval <= 0 {
		return
	}

	// Cancel any existing state for this name.
	rm.stopName(name)

	connCtx, cancel := context.WithCancel(ctx)
	state := &connState{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	rm.mu.Lock()
	rm.conns[name] = state
	rm.mu.Unlock()

	go func() {
		defer close(state.done)

		ticker := time.NewTicker(rm.cfg.HeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-connCtx.Done():
				rm.logger.Info("mcp: heartbeat stopped", "name", name)
				return
			case <-ticker.C:
				err := pingFn(connCtx)
				if err == nil {
					continue
				}

				if connCtx.Err() != nil {
					return
				}

				rm.logger.Warn("mcp: heartbeat ping failed", "name", name, "err", err)

				if onFailure != nil {
					onFailure(name)
				}

				// Spawn reconnect in a separate goroutine so the heartbeat
				// goroutine can exit cleanly. Connect registers its own
				// connState for this name.
				go func() {
					reconnErr := rm.Connect(connCtx, name, func(innerCtx context.Context) error {
						return pingFn(innerCtx)
					})
					if reconnErr != nil && connCtx.Err() == nil {
						rm.logger.Error("mcp: reconnect failed", "name", name, "err", reconnErr)
					}
				}()
				return
			}
		}
	}()
}

// Stop cancels the ongoing reconnect/heartbeat goroutine for the named connection
// and waits for it to finish.
func (rm *ReconnectManager) Stop(name string) {
	rm.stopName(name)
}

// StopAll cancels all managed connections and waits for their goroutines to finish.
func (rm *ReconnectManager) StopAll() {
	rm.mu.Lock()
	names := make([]string, 0, len(rm.conns))
	for n := range rm.conns {
		names = append(names, n)
	}
	rm.mu.Unlock()

	for _, n := range names {
		rm.stopName(n)
	}
}

// stopName cancels and removes the state for a single connection, waiting for
// its goroutine to finish.
func (rm *ReconnectManager) stopName(name string) {
	rm.mu.Lock()
	state, ok := rm.conns[name]
	if ok {
		delete(rm.conns, name)
	}
	rm.mu.Unlock()

	if ok {
		state.cancel()
		<-state.done
	}
}
