// Wiring for per-category token budget tracking, intended to be called from
// the query loop (internal/query/advanced_engine.go). This file provides the
// BudgetTracker type — a per-turn, per-category accumulator that sits on top
// of the lower-level Budget type defined in tokenbudget.go.
//
// Categories mirror the Anthropic usage shape (prompt/completion/tool_result/
// attachment/cache_read/cache_write) so we can render a meaningful /cost
// breakdown and decide when to trigger /compact.
package tokenbudget

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrBudgetExceeded is returned by BudgetTracker methods when the configured
// limit has been exceeded.
var ErrBudgetExceeded = errors.New("tokenbudget: budget exceeded")

// Category is a tag for per-category token accounting.
type Category string

// Known token categories. Extra categories can be defined by callers; the
// tracker treats Category as an opaque string.
const (
	CategoryPrompt     Category = "prompt"
	CategoryCompletion Category = "completion"
	CategoryToolResult Category = "tool_result"
	CategoryAttachment Category = "attachment"
	CategoryCacheRead  Category = "cache_read"
	CategoryCacheWrite Category = "cache_write"
)

// DefaultCategories lists the well-known categories in display order.
func DefaultCategories() []Category {
	return []Category{
		CategoryPrompt,
		CategoryCompletion,
		CategoryToolResult,
		CategoryAttachment,
		CategoryCacheRead,
		CategoryCacheWrite,
	}
}

// TurnStats holds one turn's per-category token usage. Any field may be zero.
// Extras is optional — used for custom categories beyond the well-known set.
type TurnStats struct {
	Prompt     int
	Completion int
	ToolResult int
	Attachment int
	CacheRead  int
	CacheWrite int

	// Extras allows callers to record additional categories (e.g. "thinking").
	Extras map[Category]int

	// At is an optional timestamp. Zero means "now".
	At time.Time
}

// Total returns the sum of all token fields in this turn's stats.
func (t TurnStats) Total() int {
	sum := t.Prompt + t.Completion + t.ToolResult + t.Attachment + t.CacheRead + t.CacheWrite
	for _, v := range t.Extras {
		sum += v
	}
	return sum
}

// TrackerConfig configures a BudgetTracker.
type TrackerConfig struct {
	// Limit is the total token budget (context window, or custom cap).
	// A zero or negative limit disables the exceeded check.
	Limit int

	// CompactThreshold is the fraction (0.0 – 1.0) of the limit after which
	// ShouldTriggerCompact returns true. Defaults to 0.70 when <= 0.
	CompactThreshold float64
}

// BudgetTracker accumulates per-category token usage across turns and
// exposes helpers for the query loop and the /cost command.
//
// It is safe for concurrent use.
type BudgetTracker struct {
	mu      sync.Mutex
	cfg     TrackerConfig
	totals  map[Category]int64
	turns   int
	started time.Time
	last    time.Time
}

// NewTracker creates a BudgetTracker with the given configuration.
func NewTracker(cfg TrackerConfig) *BudgetTracker {
	if cfg.CompactThreshold <= 0 {
		cfg.CompactThreshold = 0.70
	}
	return &BudgetTracker{
		cfg:     cfg,
		totals:  make(map[Category]int64),
		started: time.Now(),
	}
}

// Limit returns the configured limit (may be zero).
func (t *BudgetTracker) Limit() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg.Limit
}

// SetLimit updates the tracker's limit. Useful when the active model changes.
func (t *BudgetTracker) SetLimit(limit int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cfg.Limit = limit
}

// Turns returns the number of turns recorded so far.
func (t *BudgetTracker) Turns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.turns
}

// TotalUsed returns the sum of all tokens recorded across every category.
func (t *BudgetTracker) TotalUsed() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return int(t.totalLocked())
}

func (t *BudgetTracker) totalLocked() int64 {
	var sum int64
	for _, v := range t.totals {
		sum += v
	}
	return sum
}

// UsedIn returns the cumulative tokens recorded for a single category.
func (t *BudgetTracker) UsedIn(c Category) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return int(t.totals[c])
}

// Categories returns a copy of the per-category totals map.
func (t *BudgetTracker) Categories() map[Category]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[Category]int, len(t.totals))
	for k, v := range t.totals {
		out[k] = int(v)
	}
	return out
}

// RemainingBudget reports the tokens remaining before the configured limit.
//
// If the tracker has no limit configured (Limit <= 0), it returns math.MaxInt32
// so callers can treat "no limit" uniformly. If the budget has been exceeded,
// it returns 0 and ErrBudgetExceeded.
//
// The context is accepted for API symmetry with call sites in the query loop
// (and to support future cancellation). Cancellation is honoured.
func (t *BudgetTracker) RemainingBudget(ctx context.Context) (int, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.Limit <= 0 {
		// No limit configured — treat as "effectively unlimited".
		return int(^uint32(0) >> 1), nil
	}
	rem := int64(t.cfg.Limit) - t.totalLocked()
	if rem < 0 {
		return 0, ErrBudgetExceeded
	}
	return int(rem), nil
}

// RecordTurn accumulates one turn's per-category usage into the tracker.
// Negative values are clamped to zero. It is safe to call with a zero-valued
// TurnStats (will simply increment the turn counter).
func (t *BudgetTracker) RecordTurn(turn TurnStats) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if v := turn.Prompt; v > 0 {
		t.totals[CategoryPrompt] += int64(v)
	}
	if v := turn.Completion; v > 0 {
		t.totals[CategoryCompletion] += int64(v)
	}
	if v := turn.ToolResult; v > 0 {
		t.totals[CategoryToolResult] += int64(v)
	}
	if v := turn.Attachment; v > 0 {
		t.totals[CategoryAttachment] += int64(v)
	}
	if v := turn.CacheRead; v > 0 {
		t.totals[CategoryCacheRead] += int64(v)
	}
	if v := turn.CacheWrite; v > 0 {
		t.totals[CategoryCacheWrite] += int64(v)
	}
	for c, v := range turn.Extras {
		if v <= 0 {
			continue
		}
		t.totals[c] += int64(v)
	}

	t.turns++
	if !turn.At.IsZero() {
		t.last = turn.At
	} else {
		t.last = time.Now()
	}
}

// Reset clears the accumulated usage but keeps configuration.
func (t *BudgetTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totals = make(map[Category]int64)
	t.turns = 0
	t.started = time.Now()
	t.last = time.Time{}
}

// UsageRatio returns the fraction of the limit that has been consumed
// (0.0 – may exceed 1.0 if over budget). Returns 0 when no limit is set.
func (t *BudgetTracker) UsageRatio() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.Limit <= 0 {
		return 0
	}
	return float64(t.totalLocked()) / float64(t.cfg.Limit)
}

// ShouldTriggerCompact returns true when the current usage exceeds the
// configured compact threshold (default 70%). Always false when no limit
// is configured.
func (t *BudgetTracker) ShouldTriggerCompact() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.Limit <= 0 {
		return false
	}
	ratio := float64(t.totalLocked()) / float64(t.cfg.Limit)
	return ratio >= t.cfg.CompactThreshold
}

// IsExceeded reports whether the cumulative usage has passed the limit.
func (t *BudgetTracker) IsExceeded() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfg.Limit <= 0 {
		return false
	}
	return t.totalLocked() > int64(t.cfg.Limit)
}

// BudgetReport returns a human-readable multi-line breakdown of token usage
// suitable for the /cost command.
func (t *BudgetTracker) BudgetReport() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var b strings.Builder
	total := t.totalLocked()

	b.WriteString("Token Budget Report\n")
	b.WriteString("-------------------\n")
	fmt.Fprintf(&b, "Turns recorded : %d\n", t.turns)
	fmt.Fprintf(&b, "Total tokens   : %s\n", formatInt(int(total)))

	if t.cfg.Limit > 0 {
		ratio := float64(total) / float64(t.cfg.Limit) * 100
		rem := int64(t.cfg.Limit) - total
		if rem < 0 {
			rem = 0
		}
		fmt.Fprintf(&b, "Limit          : %s\n", formatInt(t.cfg.Limit))
		fmt.Fprintf(&b, "Remaining      : %s\n", formatInt(int(rem)))
		fmt.Fprintf(&b, "Usage          : %.1f%%\n", ratio)
		fmt.Fprintf(&b, "Compact at     : %.0f%% (%s)\n",
			t.cfg.CompactThreshold*100,
			yesNo(ratio/100 >= t.cfg.CompactThreshold, "TRIGGERED", "below threshold"))
	} else {
		b.WriteString("Limit          : (unlimited)\n")
	}

	if t.turns > 0 && !t.started.IsZero() {
		dur := time.Since(t.started).Round(time.Second)
		fmt.Fprintf(&b, "Elapsed        : %s\n", dur)
	}

	b.WriteString("\nPer-category breakdown:\n")
	// Sort: known categories first in fixed order, then any extras alphabetically.
	known := DefaultCategories()
	seen := make(map[Category]bool, len(known))
	for _, c := range known {
		seen[c] = true
		v := int(t.totals[c])
		pct := 0.0
		if total > 0 {
			pct = float64(v) / float64(total) * 100
		}
		fmt.Fprintf(&b, "  %-12s %10s  (%5.1f%%)\n", c, formatInt(v), pct)
	}
	// Collect extras
	var extras []Category
	for c := range t.totals {
		if !seen[c] {
			extras = append(extras, c)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })
	for _, c := range extras {
		v := int(t.totals[c])
		pct := 0.0
		if total > 0 {
			pct = float64(v) / float64(total) * 100
		}
		fmt.Fprintf(&b, "  %-12s %10s  (%5.1f%%)\n", c, formatInt(v), pct)
	}
	return b.String()
}

// --- helpers ---

func formatInt(n int) string {
	// Insert thousands separators ("_") without pulling in x/text.
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return sign + s
	}
	var out strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		out.WriteString(s[:pre])
		if len(s) > pre {
			out.WriteByte('_')
		}
	}
	for i := pre; i < len(s); i += 3 {
		out.WriteString(s[i : i+3])
		if i+3 < len(s) {
			out.WriteByte('_')
		}
	}
	return sign + out.String()
}

func yesNo(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
