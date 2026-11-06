package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LogCategory identifies a debug logging category.
type LogCategory string

const (
	LogCategoryQuery      LogCategory = "query"
	LogCategoryTool       LogCategory = "tool"
	LogCategoryPermission LogCategory = "permission"
	LogCategoryHook       LogCategory = "hook"
	LogCategoryMCP        LogCategory = "mcp"
	LogCategoryCompact    LogCategory = "compact"
	LogCategorySession    LogCategory = "session"
	LogCategoryAPI        LogCategory = "api"
	LogCategoryConfig     LogCategory = "config"
)

// DebugFilter controls which debug categories are shown.
type DebugFilter struct {
	Include     []string // if non-empty, only show these categories
	Exclude     []string // if non-empty, hide these categories
	IsExclusive bool     // true when using !-prefix exclusions
}

// ParseDebugFilter parses a comma-separated filter string.
// "api,hooks" → include only api and hooks.
// "!mcp,!file" → exclude mcp and file.
// Empty/nil → nil (show everything).
func ParseDebugFilter(filter string) *DebugFilter {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil
	}
	parts := strings.Split(filter, ",")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}

	hasExcl := false
	hasIncl := false
	for _, p := range cleaned {
		if strings.HasPrefix(p, "!") {
			hasExcl = true
		} else {
			hasIncl = true
		}
	}
	// Mixed modes are invalid — return nil (show all)
	if hasExcl && hasIncl {
		return nil
	}

	var result DebugFilter
	result.IsExclusive = hasExcl
	for _, p := range cleaned {
		p = strings.TrimPrefix(p, "!")
		p = strings.ToLower(p)
		if hasExcl {
			result.Exclude = append(result.Exclude, p)
		} else {
			result.Include = append(result.Include, p)
		}
	}
	return &result
}

// --- Category extraction ---

var (
	mcpPattern     = regexp.MustCompile(`^MCP server ["']([^"']+)["']`)
	prefixPattern  = regexp.MustCompile(`^([^:\[]+):`)
	bracketPattern = regexp.MustCompile(`^\[([^\]]+)\]`)
)

// ExtractDebugCategories parses categories from a debug message string.
func ExtractDebugCategories(message string) []string {
	seen := make(map[string]bool)
	var cats []string
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			cats = append(cats, s)
		}
	}

	// MCP server "name"
	if m := mcpPattern.FindStringSubmatch(message); len(m) > 1 {
		add("mcp")
		add(m[1])
	} else {
		// Simple "category: message"
		if m := prefixPattern.FindStringSubmatch(message); len(m) > 1 {
			add(m[1])
		}
	}

	// [CATEGORY] prefix
	if m := bracketPattern.FindStringSubmatch(message); len(m) > 1 {
		add(m[1])
	}

	// "1P event:" pattern
	if strings.Contains(strings.ToLower(message), "1p event:") {
		add("1p")
	}

	return cats
}

// ShouldShowDebugMessage returns true if the message passes the filter.
func ShouldShowDebugMessage(message string, filter *DebugFilter) bool {
	if filter == nil {
		return true
	}
	cats := ExtractDebugCategories(message)
	return ShouldShowDebugCategories(cats, filter)
}

// ShouldShowDebugCategories returns true if at least one category passes the filter.
func ShouldShowDebugCategories(categories []string, filter *DebugFilter) bool {
	if filter == nil {
		return true
	}
	if len(categories) == 0 {
		return false
	}
	if filter.IsExclusive {
		for _, cat := range categories {
			for _, excl := range filter.Exclude {
				if cat == excl {
					return false
				}
			}
		}
		return true
	}
	for _, cat := range categories {
		for _, incl := range filter.Include {
			if cat == incl {
				return true
			}
		}
	}
	return false
}

// --- DebugLogger ---

// DebugLogEntry is a single captured debug log line.
type DebugLogEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Category  LogCategory `json:"category"`
	Message   string      `json:"message"`
}

// DebugLogger provides category-filtered debug logging with an in-memory ring buffer.
type DebugLogger struct {
	mu         sync.Mutex
	entries    []DebugLogEntry
	maxEntries int
	filter     *DebugFilter
	logger     *slog.Logger
}

// NewDebugLogger creates a DebugLogger.
func NewDebugLogger(maxEntries int, logger *slog.Logger) *DebugLogger {
	if maxEntries <= 0 {
		maxEntries = 5000
	}
	return &DebugLogger{
		entries:    make([]DebugLogEntry, 0, maxEntries),
		maxEntries: maxEntries,
		logger:     logger,
	}
}

// SetDebugFilter updates the active category filter.
func (dl *DebugLogger) SetDebugFilter(filterStr string) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.filter = ParseDebugFilter(filterStr)
}

// Log records a debug message if it passes the current filter.
func (dl *DebugLogger) Log(category LogCategory, msg string) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	entry := DebugLogEntry{
		Timestamp: time.Now(),
		Category:  category,
		Message:   msg,
	}

	// Always buffer for export, regardless of filter
	if len(dl.entries) >= dl.maxEntries {
		drop := dl.maxEntries / 10
		if drop < 1 {
			drop = 1
		}
		dl.entries = dl.entries[drop:]
	}
	dl.entries = append(dl.entries, entry)

	// Log to slog if filter passes
	if dl.filter == nil || ShouldShowDebugMessage(msg, dl.filter) {
		if dl.logger != nil {
			dl.logger.Debug(msg, "category", string(category))
		}
	}
}

// Logf is a convenience for formatted debug messages.
func (dl *DebugLogger) Logf(category LogCategory, format string, args ...any) {
	dl.Log(category, fmt.Sprintf(format, args...))
}

// DebugDump exports all buffered entries as JSON for diagnostic export.
func (dl *DebugLogger) DebugDump() ([]byte, error) {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	cp := make([]DebugLogEntry, len(dl.entries))
	copy(cp, dl.entries)
	return json.MarshalIndent(cp, "", "  ")
}

// Entries returns a copy of all buffered entries.
func (dl *DebugLogger) Entries() []DebugLogEntry {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	cp := make([]DebugLogEntry, len(dl.entries))
	copy(cp, dl.entries)
	return cp
}

// Clear removes all buffered entries.
func (dl *DebugLogger) Clear() {
	dl.mu.Lock()
	defer dl.mu.Unlock()
	dl.entries = dl.entries[:0]
}
