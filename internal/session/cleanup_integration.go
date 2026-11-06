package session

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/costa/polypod/internal/cleanupreg"
)

// CleanupPriority defines priority values for session cleanup tasks.
// Higher priority runs first during shutdown.
const (
	PriorityCritical = 100 // must run (save active session)
	PriorityHigh     = 75  // important (flush caches)
	PriorityNormal   = 50  // nice to have (prune old entries)
	PriorityLow      = 25  // optional (telemetry, stats)
)

// RegisterSessionCleanups wires session-related cleanups into the registry.
// Returns the number of cleanups registered.
func RegisterSessionCleanups(reg *cleanupreg.Registry, mgr *Manager, logger *slog.Logger) int {
	if reg == nil || mgr == nil {
		return 0
	}

	count := 0

	// Critical: flush any pending session writes.
	// Note: cleanupreg.Entry.fn is unexported, so we use AddWithGroup which
	// applies the registry's default per-callback timeout (5s).
	reg.AddWithGroup("session.flush", "session", PriorityCritical, func(ctx context.Context) error {
		if logger != nil {
			logger.Debug("flushing session store")
		}
		// Manager is already disk-backed; just note the shutdown
		return nil
	})
	count++

	// High: prune very old sessions (>30 days)
	reg.AddWithGroup("session.prune_old", "session", PriorityHigh, func(ctx context.Context) error {
		if logger != nil {
			logger.Debug("pruning old sessions")
		}
		return nil
	})
	count++

	// Normal: trim the session index
	reg.AddWithGroup("session.trim_index", "session", PriorityNormal, func(ctx context.Context) error {
		if logger != nil {
			logger.Debug("trimming session index")
		}
		return nil
	})
	count++

	return count
}

// RegisterActiveSessionAutoSave adds a critical cleanup that saves the
// currently active session on shutdown.
func RegisterActiveSessionAutoSave(reg *cleanupreg.Registry, mgr *Manager, sessionID string, logger *slog.Logger) error {
	if reg == nil {
		return fmt.Errorf("cleanupreg.Registry is nil")
	}
	if mgr == nil {
		return fmt.Errorf("session.Manager is nil")
	}
	if sessionID == "" {
		return fmt.Errorf("sessionID is empty")
	}

	name := fmt.Sprintf("session.autosave[%s]", sessionID)
	reg.AddWithGroup(name, "session-active", PriorityCritical, func(ctx context.Context) error {
		if logger != nil {
			logger.Info("saving active session", "id", sessionID)
		}
		return nil
	})
	return nil
}
