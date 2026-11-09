package contextwindow

import (
	"log/slog"
	"sync"
)

// ModelLimits defines the context window constraints for a model.
type ModelLimits struct {
	// MaxContextTokens is the total context window size.
	MaxContextTokens int
	// MaxOutputTokens is the max tokens for a single response.
	MaxOutputTokens int
	// ReservedForOutput is the token budget reserved for model output.
	ReservedForOutput int
}

// WellKnownLimits returns limits for well-known models.
func WellKnownLimits(model string) ModelLimits {
	limits := map[string]ModelLimits{
		// Anthropic
		"claude-sonnet-4-20250514":   {MaxContextTokens: 200000, MaxOutputTokens: 16384, ReservedForOutput: 16384},
		"claude-3-7-sonnet-20250219": {MaxContextTokens: 200000, MaxOutputTokens: 16384, ReservedForOutput: 16384},
		"claude-3-5-sonnet-20241022": {MaxContextTokens: 200000, MaxOutputTokens: 8192, ReservedForOutput: 8192},
		"claude-3-5-haiku-20241022":  {MaxContextTokens: 200000, MaxOutputTokens: 8192, ReservedForOutput: 8192},
		// OpenAI
		"gpt-4o":       {MaxContextTokens: 128000, MaxOutputTokens: 16384, ReservedForOutput: 16384},
		"gpt-4o-mini":  {MaxContextTokens: 128000, MaxOutputTokens: 16384, ReservedForOutput: 16384},
		"gpt-4.1":      {MaxContextTokens: 1048576, MaxOutputTokens: 32768, ReservedForOutput: 32768},
		"gpt-4.1-mini": {MaxContextTokens: 1048576, MaxOutputTokens: 32768, ReservedForOutput: 32768},
		"o3":           {MaxContextTokens: 200000, MaxOutputTokens: 100000, ReservedForOutput: 100000},
		"o3-mini":      {MaxContextTokens: 200000, MaxOutputTokens: 100000, ReservedForOutput: 65536},
		"o4-mini":      {MaxContextTokens: 200000, MaxOutputTokens: 100000, ReservedForOutput: 100000},
		// Google
		"gemini-2.5-pro":   {MaxContextTokens: 1048576, MaxOutputTokens: 65536, ReservedForOutput: 65536},
		"gemini-2.5-flash": {MaxContextTokens: 1048576, MaxOutputTokens: 65536, ReservedForOutput: 65536},
		"gemini-2.0-flash": {MaxContextTokens: 1048576, MaxOutputTokens: 8192, ReservedForOutput: 8192},
		// DeepSeek
		"deepseek-chat":     {MaxContextTokens: 128000, MaxOutputTokens: 8192, ReservedForOutput: 8192},
		"deepseek-reasoner": {MaxContextTokens: 128000, MaxOutputTokens: 8192, ReservedForOutput: 8192},
		// Groq
		"llama-3.3-70b-versatile": {MaxContextTokens: 131072, MaxOutputTokens: 32768, ReservedForOutput: 32768},
		// Mistral
		"mistral-large-latest": {MaxContextTokens: 128000, MaxOutputTokens: 8192, ReservedForOutput: 8192},
	}

	if l, ok := limits[model]; ok {
		return l
	}

	// Default conservative limits
	return ModelLimits{
		MaxContextTokens:  128000,
		MaxOutputTokens:   8192,
		ReservedForOutput: 8192,
	}
}

// UsageLevel indicates how full the context window is.
type UsageLevel int

const (
	UsageLow      UsageLevel = iota // < 50%
	UsageMedium                     // 50-80%
	UsageHigh                       // 80-95%
	UsageCritical                   // > 95%
)

// Manager tracks and manages context window usage for a session.
type Manager struct {
	mu     sync.RWMutex
	limits ModelLimits
	logger *slog.Logger

	// Current token counts
	totalTokens   int
	systemTokens  int
	historyTokens int
	toolTokens    int

	// Compaction thresholds (percentage of MaxContextTokens)
	autoCompactThreshold float64 // default 0.80 (80%)
	criticalThreshold    float64 // default 0.95 (95%)

	// Callbacks
	onAutoCompact func() // Called when auto-compact threshold is reached
	onCritical    func() // Called when critical threshold is reached
}

// NewManager creates a context window manager for the given model.
func NewManager(model string, logger *slog.Logger) *Manager {
	return &Manager{
		limits:               WellKnownLimits(model),
		logger:               logger,
		autoCompactThreshold: 0.80,
		criticalThreshold:    0.95,
	}
}

// NewManagerWithLimits creates a manager with custom limits.
func NewManagerWithLimits(limits ModelLimits, logger *slog.Logger) *Manager {
	return &Manager{
		limits:               limits,
		logger:               logger,
		autoCompactThreshold: 0.80,
		criticalThreshold:    0.95,
	}
}

// SetAutoCompactCallback sets the function called when auto-compact threshold is hit.
func (m *Manager) SetAutoCompactCallback(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAutoCompact = fn
}

// SetModel refreshes token limits after an interactive model switch.
func (m *Manager) SetModel(model string) {
	m.mu.Lock()
	m.limits = WellKnownLimits(model)
	m.mu.Unlock()
}

// SetCriticalCallback sets the function called when critical threshold is hit.
func (m *Manager) SetCriticalCallback(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCritical = fn
}

// SetThresholds updates the compaction thresholds (0.0-1.0).
func (m *Manager) SetThresholds(autoCompact, critical float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoCompactThreshold = autoCompact
	m.criticalThreshold = critical
}

// Update records the current token usage and checks thresholds.
func (m *Manager) Update(system, history, tool int) {
	m.mu.Lock()
	m.systemTokens = system
	m.historyTokens = history
	m.toolTokens = tool
	m.totalTokens = system + history + tool
	level := m.usageLevelLocked()
	onAutoCompact := m.onAutoCompact
	onCritical := m.onCritical
	m.mu.Unlock()

	switch level {
	case UsageCritical:
		m.logger.Warn("context window CRITICAL",
			"tokens", m.totalTokens,
			"max", m.limits.MaxContextTokens,
			"percent", m.UsagePercent(),
		)
		if onCritical != nil {
			go onCritical()
		}
	case UsageHigh:
		m.logger.Info("context window HIGH - auto-compact recommended",
			"tokens", m.totalTokens,
			"max", m.limits.MaxContextTokens,
			"percent", m.UsagePercent(),
		)
		if onAutoCompact != nil {
			go onAutoCompact()
		}
	}
}

// AddTokens incrementally adds tokens to the total.
func (m *Manager) AddTokens(count int) {
	m.mu.Lock()
	m.totalTokens += count
	m.mu.Unlock()
}

// Reset clears the token counts (e.g., after compaction).
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalTokens = 0
	m.systemTokens = 0
	m.historyTokens = 0
	m.toolTokens = 0
}

// AvailableForInput returns the number of tokens available for input
// (total context minus reserved for output and current usage).
func (m *Manager) AvailableForInput() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	available := m.limits.MaxContextTokens - m.limits.ReservedForOutput - m.totalTokens
	if available < 0 {
		return 0
	}
	return available
}

// UsagePercent returns the current context window usage as a percentage.
func (m *Manager) UsagePercent() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.limits.MaxContextTokens == 0 {
		return 0
	}
	return float64(m.totalTokens) / float64(m.limits.MaxContextTokens) * 100
}

// UsageLevel returns the current usage level.
func (m *Manager) UsageLevel() UsageLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usageLevelLocked()
}

func (m *Manager) usageLevelLocked() UsageLevel {
	if m.limits.MaxContextTokens == 0 {
		return UsageLow
	}
	ratio := float64(m.totalTokens) / float64(m.limits.MaxContextTokens)
	switch {
	case ratio >= m.criticalThreshold:
		return UsageCritical
	case ratio >= m.autoCompactThreshold:
		return UsageHigh
	case ratio >= 0.50:
		return UsageMedium
	default:
		return UsageLow
	}
}

// NeedsCompaction returns true if the context window is at or above
// the auto-compact threshold.
func (m *Manager) NeedsCompaction() bool {
	level := m.UsageLevel()
	return level >= UsageHigh
}

// Limits returns the current model limits.
func (m *Manager) Limits() ModelLimits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limits
}

// Stats returns a map of current context window statistics.
func (m *Manager) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return map[string]any{
		"total_tokens":        m.totalTokens,
		"system_tokens":       m.systemTokens,
		"history_tokens":      m.historyTokens,
		"tool_tokens":         m.toolTokens,
		"max_context":         m.limits.MaxContextTokens,
		"max_output":          m.limits.MaxOutputTokens,
		"available_for_input": m.AvailableForInput(),
		"usage_percent":       m.UsagePercent(),
		"usage_level":         m.UsageLevel(),
		"needs_compaction":    m.NeedsCompaction(),
	}
}
