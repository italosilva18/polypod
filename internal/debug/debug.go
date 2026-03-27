package debug

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// State holds debug mode configuration.
type State struct {
	Enabled    bool
	Categories map[string]bool // which categories to show
	LogBuffer  []LogEntry
	MaxBuffer  int
}

// LogEntry is a single debug log entry.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"` // "api", "tools", "hooks", "mcp", "prompt", "config"
	Message   string    `json:"message"`
	Data      any       `json:"data,omitempty"`
}

// New creates a debug state (disabled by default).
func New() *State {
	return &State{
		Enabled:    false,
		Categories: make(map[string]bool),
		MaxBuffer:  200,
	}
}

// Enable turns on debug mode, optionally with category filters.
// categories: "api,tools,hooks" or "!statsig,!telemetry" (exclude) or "" (all)
func (s *State) Enable(categories string) string {
	s.Enabled = true

	if categories == "" {
		s.Categories = nil // show all
		return "Debug ativado (todas as categorias)."
	}

	s.Categories = make(map[string]bool)
	for _, cat := range strings.Split(categories, ",") {
		cat = strings.TrimSpace(cat)
		if strings.HasPrefix(cat, "!") {
			s.Categories[strings.TrimPrefix(cat, "!")] = false // exclude
		} else {
			s.Categories[cat] = true // include
		}
	}

	return fmt.Sprintf("Debug ativado (categorias: %s).", categories)
}

// Disable turns off debug mode.
func (s *State) Disable() {
	s.Enabled = false
}

// Log adds an entry if debug is enabled and category matches.
func (s *State) Log(category, message string, data any) {
	if !s.Enabled {
		return
	}
	if !s.shouldLog(category) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now(),
		Category:  category,
		Message:   message,
		Data:      data,
	}

	s.LogBuffer = append(s.LogBuffer, entry)
	if len(s.LogBuffer) > s.MaxBuffer {
		s.LogBuffer = s.LogBuffer[1:]
	}
}

// FormatSystemPrompt renders the current system prompt for inspection.
func FormatSystemPrompt(systemPrompt string, toolCount int, memoryCount int, contextTokens int) string {
	var sb strings.Builder
	sb.WriteString("## System Prompt Debug\n\n")
	fmt.Fprintf(&sb, "**Tamanho**: %d caracteres (~%d tokens)\n", len(systemPrompt), len(systemPrompt)/4)
	fmt.Fprintf(&sb, "**Tools**: %d\n", toolCount)
	fmt.Fprintf(&sb, "**Memorias**: %d\n", memoryCount)
	fmt.Fprintf(&sb, "**Contexto total**: ~%d tokens\n\n", contextTokens)
	sb.WriteString("### Conteudo\n\n```\n")

	// Truncate if very long
	prompt := systemPrompt
	if len(prompt) > 3000 {
		prompt = prompt[:3000] + "\n... [truncado, " + fmt.Sprintf("%d", len(systemPrompt)) + " chars total]"
	}
	sb.WriteString(prompt)
	sb.WriteString("\n```\n")

	return sb.String()
}

// FormatAPIRequest formats an API request for debug display.
func FormatAPIRequest(model string, messages int, tools int, temperature float32) string {
	return fmt.Sprintf("[API] model=%s messages=%d tools=%d temp=%.2f", model, messages, tools, temperature)
}

// FormatAPIResponse formats an API response for debug display.
func FormatAPIResponse(promptTokens, completionTokens int, duration time.Duration, cached bool) string {
	cacheStr := ""
	if cached {
		cacheStr = " (cached)"
	}
	return fmt.Sprintf("[API] tokens=%d+%d duration=%dms%s",
		promptTokens, completionTokens, duration.Milliseconds(), cacheStr)
}

// DumpLogs returns all buffered debug logs as formatted text.
func (s *State) DumpLogs() string {
	if len(s.LogBuffer) == 0 {
		return "Nenhum log de debug."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Debug Logs (%d entradas)\n\n", len(s.LogBuffer))

	for _, e := range s.LogBuffer {
		fmt.Fprintf(&sb, "[%s] **%s**: %s",
			e.Timestamp.Format("15:04:05.000"), e.Category, e.Message)
		if e.Data != nil {
			data, _ := json.Marshal(e.Data)
			if len(data) > 200 {
				data = append(data[:200], []byte("...")...)
			}
			fmt.Fprintf(&sb, " `%s`", string(data))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

func (s *State) shouldLog(category string) bool {
	if s.Categories == nil {
		return true // no filter = show all
	}

	// Check explicit include/exclude
	if include, exists := s.Categories[category]; exists {
		return include
	}

	// If there are only include rules, default to exclude
	// If there are only exclude rules, default to include
	hasIncludes := false
	for _, v := range s.Categories {
		if v {
			hasIncludes = true
			break
		}
	}
	return !hasIncludes
}
