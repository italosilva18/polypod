package query

import (
	"github.com/costa/polypod/internal/types"
)

// QueryConfig holds immutable values snapshotted once at query() entry.
// Separating these from the per-iteration State and mutable ToolUseContext
// makes the loop logic a pure reducer: (state, event, config) -> state.
//
// Intentionally excludes compile-time feature gates — those must stay
// inline at the guarded blocks for dead-code elimination / build tags.
// Migrated from Claude Code query/config.ts.
type QueryConfig struct {
	SessionID types.SessionID

	// Gates are runtime-evaluated feature flags and environment checks.
	// Snapshotted once per query call to avoid mid-loop inconsistencies.
	Gates RuntimeGates

	// Model configuration
	Model           string
	FallbackModel   string
	MaxOutputTokens int
	Temperature     float32
	ThinkingEnabled bool
	ThinkingBudget  int // max tokens for thinking, 0 = disabled

	// Budget controls
	MaxTurns     int
	MaxBudgetUSD float64 // 0 = unlimited
	TaskBudget   *TaskBudget

	// System prompt components
	CustomSystemPrompt string
	AppendSystemPrompt string

	// Query source identifier for analytics and routing
	QuerySource string
}

// RuntimeGates are runtime feature flags snapshotted once per query.
// These are NOT compile-time build tags (those stay at call sites).
type RuntimeGates struct {
	// StreamingToolExecution enables concurrent tool execution during streaming.
	StreamingToolExecution bool

	// EmitToolUseSummaries enables generating tool-use summaries after each batch.
	EmitToolUseSummaries bool

	// FastModeEnabled allows the model router to select a faster model for
	// simple queries. Disabled by POLYPOD_DISABLE_FAST_MODE env var.
	FastModeEnabled bool

	// AutoCompactEnabled allows automatic context compaction when the token
	// count exceeds the threshold. Disabled by POLYPOD_DISABLE_AUTO_COMPACT.
	AutoCompactEnabled bool

	// VerboseMode enables additional debug output during query execution.
	VerboseMode bool
}

// TaskBudget represents an output-token budget for the entire agentic turn.
// `Total` is set once; `Remaining` is computed per iteration from cumulative
// API usage and adjusted after compaction boundaries.
type TaskBudget struct {
	Total     int
	Remaining int // computed per iteration, -1 means unset
}

// BuildQueryConfig creates an immutable QueryConfig snapshot.
// Call this once at query() entry — not per iteration.
func BuildQueryConfig(opts ...QueryConfigOption) QueryConfig {
	cfg := QueryConfig{
		Gates: RuntimeGates{
			AutoCompactEnabled: true,
			FastModeEnabled:    true,
		},
		MaxTurns: 0, // 0 = unlimited
	}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// QueryConfigOption is a functional option for BuildQueryConfig.
type QueryConfigOption func(*QueryConfig)

// WithSessionID sets the session ID.
func WithSessionID(id types.SessionID) QueryConfigOption {
	return func(c *QueryConfig) { c.SessionID = id }
}

// WithModel sets the primary model name.
func WithModel(model string) QueryConfigOption {
	return func(c *QueryConfig) { c.Model = model }
}

// WithFallbackModel sets the fallback model name.
func WithFallbackModel(model string) QueryConfigOption {
	return func(c *QueryConfig) { c.FallbackModel = model }
}

// WithMaxTurns sets the maximum number of tool-use turns.
func WithMaxTurns(n int) QueryConfigOption {
	return func(c *QueryConfig) { c.MaxTurns = n }
}

// WithMaxBudgetUSD sets the maximum cost budget in USD.
func WithMaxBudgetUSD(usd float64) QueryConfigOption {
	return func(c *QueryConfig) { c.MaxBudgetUSD = usd }
}

// WithTaskBudget sets the per-turn output token budget.
func WithTaskBudget(total int) QueryConfigOption {
	return func(c *QueryConfig) {
		c.TaskBudget = &TaskBudget{Total: total, Remaining: -1}
	}
}

// WithGates overrides the runtime gates.
func WithGates(g RuntimeGates) QueryConfigOption {
	return func(c *QueryConfig) { c.Gates = g }
}

// WithMaxOutputTokens sets the max output tokens per API call.
func WithMaxOutputTokens(n int) QueryConfigOption {
	return func(c *QueryConfig) { c.MaxOutputTokens = n }
}

// WithThinking enables/disables thinking mode.
func WithThinking(enabled bool, budget int) QueryConfigOption {
	return func(c *QueryConfig) {
		c.ThinkingEnabled = enabled
		c.ThinkingBudget = budget
	}
}

// WithSystemPrompt sets the custom system prompt.
func WithSystemPrompt(prompt string) QueryConfigOption {
	return func(c *QueryConfig) { c.CustomSystemPrompt = prompt }
}

// WithAppendSystemPrompt sets additional text appended to system prompt.
func WithAppendSystemPrompt(prompt string) QueryConfigOption {
	return func(c *QueryConfig) { c.AppendSystemPrompt = prompt }
}

// WithQuerySource sets the query source identifier.
func WithQuerySource(source string) QueryConfigOption {
	return func(c *QueryConfig) { c.QuerySource = source }
}

// WithVerbose sets verbose mode.
func WithVerbose(v bool) QueryConfigOption {
	return func(c *QueryConfig) { c.Gates.VerboseMode = v }
}
