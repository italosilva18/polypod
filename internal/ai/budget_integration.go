package ai

import (
	"fmt"
	"sync"

	"github.com/costa/polypod/internal/tokenbudget"
)

// SessionBudgetTracker manages token budgets per session.
type SessionBudgetTracker struct {
	mu       sync.RWMutex
	budgets  map[string]*tokenbudget.Budget
	defaults int // default per-session budget
}

// NewSessionBudgetTracker creates a new tracker with a default per-session limit.
func NewSessionBudgetTracker(defaultLimit int) *SessionBudgetTracker {
	if defaultLimit <= 0 {
		defaultLimit = 1_000_000 // 1M tokens
	}
	return &SessionBudgetTracker{
		budgets:  make(map[string]*tokenbudget.Budget),
		defaults: defaultLimit,
	}
}

// GetOrCreate returns the budget for a session, creating it if needed.
func (t *SessionBudgetTracker) GetOrCreate(sessionID string) *tokenbudget.Budget {
	t.mu.Lock()
	defer t.mu.Unlock()

	if b, ok := t.budgets[sessionID]; ok {
		return b
	}
	b := tokenbudget.New(t.defaults)
	t.budgets[sessionID] = b
	return b
}

// SetLimit sets a custom limit for a specific session.
func (t *SessionBudgetTracker) SetLimit(sessionID string, limit int) {
	b := t.GetOrCreate(sessionID)
	b.SetLimit(limit)
}

// Use records token usage for a session.
// Returns error if the session would exceed its budget.
func (t *SessionBudgetTracker) Use(sessionID string, inputTokens, outputTokens int) error {
	b := t.GetOrCreate(sessionID)
	total := inputTokens + outputTokens
	if !b.Use(total) {
		return fmt.Errorf("session %s: budget exceeded (used %d, limit %d)",
			sessionID, b.Used(), b.Limit())
	}
	return nil
}

// ForceUse records usage even if it exceeds the limit.
func (t *SessionBudgetTracker) ForceUse(sessionID string, inputTokens, outputTokens int) {
	b := t.GetOrCreate(sessionID)
	b.ForceUse(inputTokens + outputTokens)
}

// Remaining returns remaining tokens for a session.
func (t *SessionBudgetTracker) Remaining(sessionID string) int {
	t.mu.RLock()
	b, ok := t.budgets[sessionID]
	t.mu.RUnlock()
	if !ok {
		return t.defaults
	}
	return b.Remaining()
}

// Percent returns the percentage of the budget used for a session.
func (t *SessionBudgetTracker) Percent(sessionID string) float64 {
	t.mu.RLock()
	b, ok := t.budgets[sessionID]
	t.mu.RUnlock()
	if !ok {
		return 0
	}
	return b.Percent()
}

// IsNearLimit returns true if the session is within the threshold of its limit.
func (t *SessionBudgetTracker) IsNearLimit(sessionID string, thresholdPercent float64) bool {
	t.mu.RLock()
	b, ok := t.budgets[sessionID]
	t.mu.RUnlock()
	if !ok {
		return false
	}
	return b.IsNearLimit(thresholdPercent)
}

// Reset clears usage for a session (keeps the limit).
func (t *SessionBudgetTracker) Reset(sessionID string) {
	t.mu.RLock()
	b, ok := t.budgets[sessionID]
	t.mu.RUnlock()
	if ok {
		b.Reset()
	}
}

// Remove deletes a session's budget entirely.
func (t *SessionBudgetTracker) Remove(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.budgets, sessionID)
}

// SessionIDs returns all tracked session IDs.
func (t *SessionBudgetTracker) SessionIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ids := make([]string, 0, len(t.budgets))
	for id := range t.budgets {
		ids = append(ids, id)
	}
	return ids
}

// TotalUsed returns the sum of used tokens across all sessions.
func (t *SessionBudgetTracker) TotalUsed() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	total := 0
	for _, b := range t.budgets {
		total += b.Used()
	}
	return total
}

// Count returns the number of tracked sessions.
func (t *SessionBudgetTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.budgets)
}
