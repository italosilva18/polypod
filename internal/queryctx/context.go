// Package queryctx wraps all query-time state: system prompts, user/system
// context, query tracking, and cache-safe parameters. Migrated from
// Claude Code's queryContext.ts, queryHelpers.ts, systemPrompt.ts, systemPromptType.ts.
package queryctx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
)

// SystemPrompt is a branded type for system prompt arrays.
// Each element is a section that gets joined when sending to the API.
type SystemPrompt []string

// AsSystemPrompt wraps a string slice as a SystemPrompt.
func AsSystemPrompt(parts []string) SystemPrompt {
	out := make(SystemPrompt, len(parts))
	copy(out, parts)
	return out
}

// Join concatenates all system prompt sections with a separator.
func (sp SystemPrompt) Join(sep string) string {
	return strings.Join(sp, sep)
}

// IsEmpty returns true if the system prompt has no content.
func (sp SystemPrompt) IsEmpty() bool {
	for _, part := range sp {
		if strings.TrimSpace(part) != "" {
			return false
		}
	}
	return true
}

// QueryTracking holds chain tracking information for a query.
type QueryTracking struct {
	// ChainID uniquely identifies a query chain (UUID).
	ChainID string

	// Depth is the nesting level (0 for top-level, incremented for subagents).
	Depth int

	// Source identifies where this query originated.
	Source string

	// ParentChainID links to the parent query chain for subagent queries.
	ParentChainID string
}

// NewQueryTracking creates a new QueryTracking with a fresh chain ID.
func NewQueryTracking(source string) QueryTracking {
	return QueryTracking{
		ChainID: generateChainID(),
		Depth:   0,
		Source:  source,
	}
}

// NewChildTracking creates tracking for a sub-query with incremented depth.
func (qt QueryTracking) NewChildTracking() QueryTracking {
	return QueryTracking{
		ChainID:       generateChainID(),
		Depth:         qt.Depth + 1,
		Source:        qt.Source,
		ParentChainID: qt.ChainID,
	}
}

func generateChainID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// QueryContext wraps all state needed during a query execution.
type QueryContext struct {
	// SystemPrompt is the assembled system prompt.
	SystemPrompt SystemPrompt

	// UserContext contains user-provided context entries (e.g., CLAUDE.md).
	// Keys are context names, values are content.
	UserContext map[string]string

	// SystemContext contains system-provided context entries (e.g., git status).
	// Keys are context names, values are content.
	SystemContext map[string]string

	// Tracking holds query chain tracking information.
	Tracking QueryTracking

	// Model is the model being used for this query.
	Model string

	// MaxOutputTokens caps the output for this query (0 = default).
	MaxOutputTokens int

	// CustomSystemPrompt overrides the default system prompt when set.
	CustomSystemPrompt string

	// AppendSystemPrompt is appended to the system prompt.
	AppendSystemPrompt string

	// QuerySource identifies the origin of this query (e.g., "repl_main_thread").
	QuerySource string
}

// CacheSafeParams are the parameters that must be identical between a fork
// and parent API request to share the prompt cache. The Anthropic API cache
// key is composed of: system prompt, tools, model, messages prefix, and
// thinking config.
type CacheSafeParams struct {
	SystemPrompt  SystemPrompt
	UserContext   map[string]string
	SystemContext map[string]string
	Model         string
}

// SystemPromptBuilder assembles the final system prompt from multiple sources
// with priority: override > coordinator > agent > custom > default.
type SystemPromptBuilder struct {
	mu sync.Mutex

	// DefaultParts are the standard system prompt sections.
	DefaultParts []string

	// CustomPrompt replaces the default if set.
	CustomPrompt string

	// AgentPrompt replaces or appends based on mode.
	AgentPrompt string

	// AppendPrompt is always appended at the end.
	AppendPrompt string

	// OverridePrompt replaces all other prompts when set.
	OverridePrompt string
}

// Build assembles the final SystemPrompt based on priority rules.
//
// Priority:
//  1. OverridePrompt (if set, replaces everything)
//  2. AgentPrompt (if set, replaces default unless ProactiveMode)
//  3. CustomPrompt (if set, replaces default)
//  4. DefaultParts (standard prompt)
//
// AppendPrompt is always added at the end (except for override).
func (b *SystemPromptBuilder) Build() SystemPrompt {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Override replaces everything.
	if b.OverridePrompt != "" {
		return AsSystemPrompt([]string{b.OverridePrompt})
	}

	var parts []string

	switch {
	case b.AgentPrompt != "":
		parts = []string{b.AgentPrompt}
	case b.CustomPrompt != "":
		parts = []string{b.CustomPrompt}
	default:
		parts = make([]string, len(b.DefaultParts))
		copy(parts, b.DefaultParts)
	}

	if b.AppendPrompt != "" {
		parts = append(parts, b.AppendPrompt)
	}

	return AsSystemPrompt(parts)
}

// QueryHelpers provides utility functions for query operations.
type QueryHelpers struct{}

// IsResultSuccessful checks if the query result should be considered successful.
// Returns true if:
// - Last message is assistant with text content
// - Last message is user with only tool_result blocks
// - Stop reason is "end_turn" (model chose to emit no content)
func (QueryHelpers) IsResultSuccessful(lastMessageType string, hasTextContent bool, stopReason string) bool {
	switch lastMessageType {
	case "assistant":
		return hasTextContent
	case "user":
		// User message with tool_result blocks is valid.
		return true
	default:
		// Carve-out: API completed (stop_reason=end_turn) with zero content.
		return stopReason == "end_turn"
	}
}

// ExtractCliName extracts the actual CLI command name from a bash command string,
// skipping env var assignments (e.g. FOO=bar vercel -> vercel) and prefixes like sudo.
func (QueryHelpers) ExtractCliName(command string) string {
	stripped := map[string]bool{"sudo": true}
	tokens := strings.Fields(command)
	for _, token := range tokens {
		// Skip env var assignments (KEY=VALUE).
		if strings.Contains(token, "=") && len(token) > 1 {
			idx := strings.Index(token, "=")
			prefix := token[:idx]
			if isValidEnvVarName(prefix) {
				continue
			}
		}
		// Skip stripped commands.
		if stripped[token] {
			continue
		}
		return token
	}
	return ""
}

// isValidEnvVarName returns true if s is a valid env var name ([A-Za-z_][A-Za-z0-9_]*).
func isValidEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if i == 0 {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

// TokenStats holds a breakdown of token usage by category.
// Migrated from contextAnalysis.ts.
type TokenStats struct {
	ToolRequests        map[string]int
	ToolResults         map[string]int
	HumanMessages       int
	AssistantMessages   int
	LocalCommandOutputs int
	Other               int
	Attachments         map[string]int
	DuplicateFileReads  map[string]DuplicateRead
	Total               int
}

// DuplicateRead tracks how many times a file was read and the token cost.
type DuplicateRead struct {
	Count  int
	Tokens int
}

// NewTokenStats creates an empty TokenStats.
func NewTokenStats() *TokenStats {
	return &TokenStats{
		ToolRequests:       make(map[string]int),
		ToolResults:        make(map[string]int),
		Attachments:        make(map[string]int),
		DuplicateFileReads: make(map[string]DuplicateRead),
	}
}

// IncrementToolRequest adds token count for a tool request.
func (ts *TokenStats) IncrementToolRequest(toolName string, tokens int) {
	ts.ToolRequests[toolName] += tokens
	ts.Total += tokens
}

// IncrementToolResult adds token count for a tool result.
func (ts *TokenStats) IncrementToolResult(toolName string, tokens int) {
	ts.ToolResults[toolName] += tokens
	ts.Total += tokens
}

// ToMetrics converts TokenStats to a flat map suitable for telemetry/analytics.
func (ts *TokenStats) ToMetrics() map[string]int {
	metrics := map[string]int{
		"total_tokens":                ts.Total,
		"human_message_tokens":        ts.HumanMessages,
		"assistant_message_tokens":    ts.AssistantMessages,
		"local_command_output_tokens": ts.LocalCommandOutputs,
		"other_tokens":                ts.Other,
	}

	for typ, count := range ts.Attachments {
		metrics["attachment_"+typ+"_count"] = count
	}
	for tool, tokens := range ts.ToolRequests {
		metrics["tool_request_"+tool+"_tokens"] = tokens
	}
	for tool, tokens := range ts.ToolResults {
		metrics["tool_result_"+tool+"_tokens"] = tokens
	}

	duplicateTotal := 0
	for _, d := range ts.DuplicateFileReads {
		duplicateTotal += d.Tokens
	}
	metrics["duplicate_read_tokens"] = duplicateTotal
	metrics["duplicate_read_file_count"] = len(ts.DuplicateFileReads)

	if ts.Total > 0 {
		metrics["human_message_percent"] = int(float64(ts.HumanMessages) / float64(ts.Total) * 100)
		metrics["assistant_message_percent"] = int(float64(ts.AssistantMessages) / float64(ts.Total) * 100)
		metrics["duplicate_read_percent"] = int(float64(duplicateTotal) / float64(ts.Total) * 100)
	}

	return metrics
}
