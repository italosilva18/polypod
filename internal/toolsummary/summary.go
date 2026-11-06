// Package toolsummary tracks and summarizes tool usage patterns.
package toolsummary

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolUse represents a single invocation of a tool.
type ToolUse struct {
	Name       string
	Timestamp  time.Time
	Duration   time.Duration
	ArgsSize   int
	OutputSize int
	Success    bool
	Error      string
}

// ToolStats holds aggregated statistics for a single tool.
type ToolStats struct {
	Name          string
	CallCount     int
	SuccessCount  int
	FailCount     int
	AvgDuration   time.Duration
	TotalDuration time.Duration
	LastUsed      time.Time
}

// Collector accumulates ToolUse records in a thread-safe manner.
type Collector struct {
	mu      sync.RWMutex
	records []ToolUse
}

var (
	globalCollector     *Collector
	globalCollectorOnce sync.Once
)

// NewCollector returns a fresh Collector.
func NewCollector() *Collector {
	return &Collector{}
}

// GetCollector returns the process-wide singleton Collector.
func GetCollector() *Collector {
	globalCollectorOnce.Do(func() {
		globalCollector = NewCollector()
	})
	return globalCollector
}

// Record appends a ToolUse entry to the collector.
func (c *Collector) Record(use ToolUse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, use)
}

// Stats returns per-tool aggregated statistics.
func (c *Collector) Stats() map[string]*ToolStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	m := make(map[string]*ToolStats)
	for _, r := range c.records {
		s, ok := m[r.Name]
		if !ok {
			s = &ToolStats{Name: r.Name}
			m[r.Name] = s
		}
		s.CallCount++
		if r.Success {
			s.SuccessCount++
		} else {
			s.FailCount++
		}
		s.TotalDuration += r.Duration
		if r.Timestamp.After(s.LastUsed) {
			s.LastUsed = r.Timestamp
		}
	}
	for _, s := range m {
		if s.CallCount > 0 {
			s.AvgDuration = s.TotalDuration / time.Duration(s.CallCount)
		}
	}
	return m
}

// TopTools returns the n most-called tools, sorted by CallCount descending.
func (c *Collector) TopTools(n int) []*ToolStats {
	stats := c.Stats()
	list := make([]*ToolStats, 0, len(stats))
	for _, s := range stats {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].CallCount > list[j].CallCount
	})
	if n > len(list) {
		n = len(list)
	}
	return list[:n]
}

// FailingTools returns tools whose failure rate exceeds 20%.
func (c *Collector) FailingTools() []*ToolStats {
	stats := c.Stats()
	var failing []*ToolStats
	for _, s := range stats {
		if s.CallCount == 0 {
			continue
		}
		failRate := float64(s.FailCount) / float64(s.CallCount)
		if failRate > 0.20 {
			failing = append(failing, s)
		}
	}
	sort.Slice(failing, func(i, j int) bool {
		rateI := float64(failing[i].FailCount) / float64(failing[i].CallCount)
		rateJ := float64(failing[j].FailCount) / float64(failing[j].CallCount)
		return rateI > rateJ
	})
	return failing
}

// FormatReport produces a Portuguese markdown report summarising tool usage.
func (c *Collector) FormatReport() string {
	stats := c.Stats()
	if len(stats) == 0 {
		return "## Relatório de Uso de Ferramentas\n\nNenhuma ferramenta foi utilizada."
	}

	// Deterministic ordering by call count desc, then name asc.
	sorted := make([]*ToolStats, 0, len(stats))
	for _, s := range stats {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CallCount != sorted[j].CallCount {
			return sorted[i].CallCount > sorted[j].CallCount
		}
		return sorted[i].Name < sorted[j].Name
	})

	var b strings.Builder
	b.WriteString("## Relatório de Uso de Ferramentas\n\n")

	// Summary line.
	totalCalls := 0
	totalSuccess := 0
	for _, s := range sorted {
		totalCalls += s.CallCount
		totalSuccess += s.SuccessCount
	}
	fmt.Fprintf(&b, "**Total de chamadas:** %d | **Ferramentas distintas:** %d | **Taxa de sucesso global:** %.1f%%\n\n",
		totalCalls, len(sorted), safePct(totalSuccess, totalCalls))

	// Table.
	b.WriteString("| Ferramenta | Chamadas | Sucesso | Falha | Duração Média | Último Uso |\n")
	b.WriteString("|---|---:|---:|---:|---:|---|\n")
	for _, s := range sorted {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s | %s |\n",
			s.Name,
			s.CallCount,
			s.SuccessCount,
			s.FailCount,
			formatDuration(s.AvgDuration),
			formatTime(s.LastUsed),
		)
	}

	// Failing tools section.
	failing := c.FailingTools()
	if len(failing) > 0 {
		b.WriteString("\n### Ferramentas com Alta Taxa de Falha (>20%)\n\n")
		for _, s := range failing {
			rate := safePct(s.FailCount, s.CallCount)
			fmt.Fprintf(&b, "- **%s**: %.1f%% de falha (%d/%d)\n", s.Name, rate, s.FailCount, s.CallCount)
		}
	}

	return b.String()
}

// Since returns all records with Timestamp at or after t.
func (c *Collector) Since(t time.Time) []ToolUse {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []ToolUse
	for _, r := range c.records {
		if !r.Timestamp.Before(t) {
			result = append(result, r)
		}
	}
	return result
}

// Reset clears all recorded data.
func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = nil
}

// --- helpers ---

func safePct(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den) * 100
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}
