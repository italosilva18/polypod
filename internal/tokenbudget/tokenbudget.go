// Package tokenbudget provides token budget tracking and reservation.
package tokenbudget

import (
	"fmt"
	"sync"
	"time"
)

// Budget tracks token usage against a limit with optional reservations.
type Budget struct {
	mu       sync.Mutex
	limit    int
	used     int
	reserved int

	// Stats
	peak       int
	resetCount int
	history    []Snapshot
	maxHistory int
}

// Snapshot captures the state at a point in time.
type Snapshot struct {
	Time      time.Time
	Limit     int
	Used      int
	Reserved  int
	Available int
	Percent   float64
}

// New creates a new budget with the given limit.
func New(limit int) *Budget {
	return &Budget{
		limit:      limit,
		maxHistory: 100,
	}
}

// Limit returns the current limit.
func (b *Budget) Limit() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit
}

// SetLimit changes the budget limit.
func (b *Budget) SetLimit(limit int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limit = limit
}

// Used returns the current used count.
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Reserved returns the current reserved count.
func (b *Budget) Reserved() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reserved
}

// Total returns used + reserved.
func (b *Budget) Total() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used + b.reserved
}

// Remaining returns the unused, unreserved capacity.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remainingLocked()
}

func (b *Budget) remainingLocked() int {
	rem := b.limit - b.used - b.reserved
	if rem < 0 {
		return 0
	}
	return rem
}

// Reserve attempts to reserve n tokens for future use.
// Returns true if reservation was made, false if insufficient capacity.
func (b *Budget) Reserve(n int) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.remainingLocked() < n {
		return false
	}
	b.reserved += n
	b.recordSnapshot()
	return true
}

// MustReserve reserves n tokens or panics if insufficient.
func (b *Budget) MustReserve(n int) {
	if !b.Reserve(n) {
		panic(fmt.Sprintf("tokenbudget: cannot reserve %d tokens, only %d available", n, b.Remaining()))
	}
}

// Release releases previously reserved tokens.
func (b *Budget) Release(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.reserved -= n
	if b.reserved < 0 {
		b.reserved = 0
	}
}

// Commit converts reserved tokens to used tokens.
// If actualUsed > 0, that many are added to used (must be <= reserved).
func (b *Budget) Commit(reservedAmount int, actualUsed int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Release the reservation
	b.reserved -= reservedAmount
	if b.reserved < 0 {
		b.reserved = 0
	}

	// Add actual usage
	if actualUsed > 0 {
		b.used += actualUsed
		if b.used > b.peak {
			b.peak = b.used
		}
	}
	b.recordSnapshot()
}

// Use directly adds to the used count (no reservation).
// Returns true if successful, false if would exceed limit.
func (b *Budget) Use(n int) bool {
	if n <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.used+b.reserved+n > b.limit {
		return false
	}
	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	b.recordSnapshot()
	return true
}

// ForceUse adds to the used count even if it exceeds the limit.
// Returns the new used count.
func (b *Budget) ForceUse(n int) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	b.recordSnapshot()
	return b.used
}

// Reset clears used and reserved counts (but keeps the limit).
func (b *Budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.used = 0
	b.reserved = 0
	b.resetCount++
	b.recordSnapshot()
}

// Percent returns the percentage of the limit currently used (0-100).
func (b *Budget) Percent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.percentLocked()
}

func (b *Budget) percentLocked() float64 {
	if b.limit == 0 {
		return 0
	}
	return float64(b.used+b.reserved) / float64(b.limit) * 100
}

// IsExceeded returns true if total exceeds the limit.
func (b *Budget) IsExceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used+b.reserved > b.limit
}

// IsNearLimit returns true if usage exceeds the threshold percentage (0-100).
func (b *Budget) IsNearLimit(thresholdPercent float64) bool {
	return b.Percent() >= thresholdPercent
}

// Snapshot captures the current state.
func (b *Budget) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *Budget) snapshotLocked() Snapshot {
	return Snapshot{
		Time:      time.Now(),
		Limit:     b.limit,
		Used:      b.used,
		Reserved:  b.reserved,
		Available: b.remainingLocked(),
		Percent:   b.percentLocked(),
	}
}

// History returns the recent snapshots.
func (b *Budget) History() []Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Snapshot, len(b.history))
	copy(out, b.history)
	return out
}

// Peak returns the peak usage observed.
func (b *Budget) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// SetMaxHistory sets the maximum history retention.
func (b *Budget) SetMaxHistory(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maxHistory = n
	if len(b.history) > n {
		b.history = b.history[len(b.history)-n:]
	}
}

func (b *Budget) recordSnapshot() {
	if b.maxHistory <= 0 {
		return
	}
	snap := b.snapshotLocked()
	b.history = append(b.history, snap)
	if len(b.history) > b.maxHistory {
		b.history = b.history[len(b.history)-b.maxHistory:]
	}
}

// String returns a human-friendly representation.
func (b *Budget) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("Budget{used=%d, reserved=%d, limit=%d, %.1f%%}",
		b.used, b.reserved, b.limit, b.percentLocked())
}

// ====== Multi-budget aggregator ======

// Aggregator combines multiple budgets.
type Aggregator struct {
	mu      sync.Mutex
	budgets map[string]*Budget
}

// NewAggregator creates a new aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{
		budgets: make(map[string]*Budget),
	}
}

// AddBudget adds a named budget to the aggregator.
func (a *Aggregator) AddBudget(name string, b *Budget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.budgets[name] = b
}

// Get returns the budget by name.
func (a *Aggregator) Get(name string) *Budget {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.budgets[name]
}

// Remove deletes a budget by name.
func (a *Aggregator) Remove(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.budgets, name)
}

// TotalUsed returns the sum of used across all budgets.
func (a *Aggregator) TotalUsed() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, b := range a.budgets {
		total += b.Used()
	}
	return total
}

// TotalLimit returns the sum of limits across all budgets.
func (a *Aggregator) TotalLimit() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, b := range a.budgets {
		total += b.Limit()
	}
	return total
}

// Names returns all registered budget names.
func (a *Aggregator) Names() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, 0, len(a.budgets))
	for n := range a.budgets {
		names = append(names, n)
	}
	return names
}
