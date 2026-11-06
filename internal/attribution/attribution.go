// Package attribution computes and formats Claude contribution statistics
// for commit messages and pull request descriptions.
//
// Migrated from Claude Code TypeScript:
//
//	src/utils/attribution.ts
//	src/utils/commitAttribution.ts (stats subset)
//
// The package reads session transcript JSONL files, counts tool calls,
// memory-file accesses, and user prompts, then produces "Co-Authored-By"
// and PR-footer strings that survive squash merges.
package attribution

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// ProductURL is the canonical Claude Code landing page embedded in attribution text.
const ProductURL = "https://claude.com/claude-code"

// DefaultModelName is the fallback display name used when the active model is
// not a known public model (guards against codename leaks in external repos).
const DefaultModelName = "Claude Opus 4.6"

// terminalOutputTags matches TERMINAL_OUTPUT_TAGS from constants/xml.ts.
// Messages whose text content starts with one of these tags are tool output,
// not human-typed prompts, and should be excluded from the N-shot count.
var terminalOutputTags = []string{
	"bash_input",
	"bash_output",
	"tool_result",
	"function_results",
}

// memoryToolNames mirrors MEMORY_ACCESS_TOOL_NAMES from attribution.ts.
// Tool uses with these names that pass the isMemoryFileAccess heuristic are
// counted as "memories recalled".
var memoryToolNames = map[string]bool{
	"Read":      true,
	"Glob":      true,
	"Grep":      true,
	"Edit":      true,
	"Write":     true,
	"FileRead":  true,
	"FileEdit":  true,
	"FileWrite": true,
}

// AttributionStats holds computed statistics for a single session arc
// (post last compaction boundary).
type AttributionStats struct {
	// ClaudeContributionPct is the estimated percentage of lines written by
	// Claude (0–100). Zero means the percentage could not be determined.
	ClaudeContributionPct int

	// NShotCount is the number of distinct user prompts sent in this arc.
	// Used to render "3-shotted" in PR attribution text.
	NShotCount int

	// MemoriesRecalled is the number of memory-file accesses detected.
	MemoriesRecalled int

	// SessionDuration is the elapsed wall-clock time of the session, computed
	// from the first and last timestamps in the transcript. Zero when the
	// transcript has no timestamps.
	SessionDuration time.Duration

	// ToolCallCount is the total number of tool_use blocks across all
	// assistant messages in the arc.
	ToolCallCount int
}

// ---------------------------------------------------------------------------
// Transcript entry types (minimal subset for stats computation)
// ---------------------------------------------------------------------------

// transcriptEntry is the union type for all JSONL lines in a Polypod session
// transcript.  We only decode the fields we care about.
type transcriptEntry struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
	// IsSidechain marks entries that belong to a forked sub-agent and should
	// be excluded from prompt counts.
	IsSidechain bool `json:"isSidechain,omitempty"`
	// Timestamp is an optional RFC-3339 wall-clock timestamp.
	Timestamp string `json:"timestamp,omitempty"`
	// Message is the raw conversation message payload.
	Message *rawMessage `json:"message,omitempty"`
}

// rawMessage mirrors the minimal shape of an Anthropic message object as it
// appears in the transcript.
type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// contentBlock is a single element of a message content array.
type contentBlock struct {
	Type string `json:"type"`
	// Text is populated for type=="text".
	Text string `json:"text,omitempty"`
	// Name is populated for type=="tool_use".
	Name string `json:"name,omitempty"`
	// Input is populated for type=="tool_use".
	Input map[string]any `json:"input,omitempty"`
}

// ---------------------------------------------------------------------------
// ComputeStats
// ---------------------------------------------------------------------------

// ComputeStats scans decoded transcript entries and returns aggregated stats.
// Callers should pass only the post-boundary slice of entries (i.e. entries
// after the last compact_boundary system message).
//
// The messages parameter uses map[string]any to remain compatible with
// arbitrary transcript shapes; structured fields are extracted by key.
func ComputeStats(messages []map[string]any) AttributionStats {
	var stats AttributionStats

	for _, raw := range messages {
		entryType, _ := raw["type"].(string)

		switch entryType {
		case "user":
			if isSidechain(raw) {
				continue
			}
			if hasVisibleUserText(raw) {
				stats.NShotCount++
			}

		case "assistant":
			if isSidechain(raw) {
				continue
			}
			blocks := extractContentBlocks(raw)
			for _, b := range blocks {
				if b.Type == "tool_use" {
					stats.ToolCallCount++
					if memoryToolNames[b.Name] && isMemoryFileAccess(b.Name, b.Input) {
						stats.MemoriesRecalled++
					}
				}
			}
		}
	}

	return stats
}

// ---------------------------------------------------------------------------
// Public attribution formatters
// ---------------------------------------------------------------------------

// GenerateCommitAttribution reads the transcript at transcriptPath, computes
// stats for the current arc, and returns a commit message trailer suitable
// for appending to a git commit body.
//
// Format:
//
//	Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
//	🤖 Generated with [Claude Code](https://claude.com/claude-code)
func GenerateCommitAttribution(transcriptPath string) string {
	stats := loadTranscriptStats(transcriptPath)
	return buildCommitAttribution(stats, DefaultModelName)
}

// GeneratePRAttribution reads the transcript at transcriptPath and returns
// the enhanced PR footer with contribution statistics.
//
// Format:
//
//	🤖 Generated with [Claude Code](https://claude.com/claude-code) (93% 3-shotted by claude-opus-4-6, 2 memories recalled)
func GeneratePRAttribution(transcriptPath string) string {
	stats := loadTranscriptStats(transcriptPath)
	return buildPRAttribution(stats, DefaultModelName)
}

// FormatAttributionBadge returns a single-line human-readable badge string.
//
// Example: "Claude contributed 87% of this change"
func FormatAttributionBadge(stats AttributionStats) string {
	if stats.ClaudeContributionPct == 0 {
		return fmt.Sprintf("🤖 Generated with [Claude Code](%s)", ProductURL)
	}
	return fmt.Sprintf("Claude contributed %d%% of this change", stats.ClaudeContributionPct)
}

// GetEnhancedAttribution returns a full attribution block that includes the
// contribution percentage, N-shot count, memory count, and an optional session
// URL for remote sessions.
//
// If sessionURL is non-empty it takes precedence and is returned verbatim.
func GetEnhancedAttribution(stats AttributionStats, sessionURL string) string {
	if sessionURL != "" {
		return sessionURL
	}
	return buildPRAttribution(stats, DefaultModelName)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func buildCommitAttribution(stats AttributionStats, modelName string) string {
	coAuthor := fmt.Sprintf("Co-Authored-By: %s <noreply@anthropic.com>", modelName)
	generated := fmt.Sprintf("🤖 Generated with [Claude Code](%s)", ProductURL)
	return coAuthor + "\n" + generated
}

func buildPRAttribution(stats AttributionStats, modelName string) string {
	// If there is truly nothing to report, return the bare default.
	if stats.ClaudeContributionPct == 0 && stats.NShotCount == 0 && stats.MemoriesRecalled == 0 {
		return fmt.Sprintf("🤖 Generated with [Claude Code](%s)", ProductURL)
	}

	var parts []string

	if stats.ClaudeContributionPct > 0 {
		parts = append(parts, fmt.Sprintf("%d%%", stats.ClaudeContributionPct))
	}
	if stats.NShotCount > 0 {
		parts = append(parts, fmt.Sprintf("%d-shotted by %s", stats.NShotCount, modelName))
	}
	if stats.MemoriesRecalled > 0 {
		noun := "memories"
		if stats.MemoriesRecalled == 1 {
			noun = "memory"
		}
		parts = append(parts, fmt.Sprintf("%d %s recalled", stats.MemoriesRecalled, noun))
	}

	detail := strings.Join(parts, ", ")
	return fmt.Sprintf("🤖 Generated with [Claude Code](%s) (%s)", ProductURL, detail)
}

// loadTranscriptStats reads and parses a transcript JSONL file, finds the
// last compact_boundary, and returns stats for the post-boundary arc.
func loadTranscriptStats(path string) AttributionStats {
	if path == "" {
		return AttributionStats{}
	}

	f, err := os.Open(path)
	if err != nil {
		return AttributionStats{}
	}
	defer f.Close()

	var entries []transcriptEntry
	scanner := bufio.NewScanner(f)
	// Allow up to 10 MB lines (large tool outputs can be big).
	scanner.Buffer(make([]byte, 10*1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e transcriptEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}

	// Slice to post-boundary arc.
	postBoundary := slicePostBoundary(entries)

	// Collect timestamps for session duration.
	var firstTS, lastTS time.Time
	for _, e := range postBoundary {
		if e.Timestamp == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			continue
		}
		if firstTS.IsZero() {
			firstTS = t
		}
		lastTS = t
	}

	// Convert to map[string]any for ComputeStats (generic path).
	rawMsgs := entriesToMaps(postBoundary)
	stats := ComputeStats(rawMsgs)

	if !firstTS.IsZero() && !lastTS.IsZero() {
		stats.SessionDuration = lastTS.Sub(firstTS)
	}

	return stats
}

// slicePostBoundary returns only the entries after the last compact_boundary.
func slicePostBoundary(entries []transcriptEntry) []transcriptEntry {
	lastIdx := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "system" && entries[i].Subtype == "compact_boundary" {
			lastIdx = i
			break
		}
	}
	if lastIdx >= 0 {
		return entries[lastIdx+1:]
	}
	return entries
}

// entriesToMaps converts typed transcript entries to the map[string]any shape
// expected by ComputeStats.
func entriesToMaps(entries []transcriptEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		m := map[string]any{
			"type":        e.Type,
			"isSidechain": e.IsSidechain,
		}
		if e.Message != nil {
			msg := map[string]any{
				"role": e.Message.Role,
			}
			// Re-parse content as []any for the generic helpers.
			if len(e.Message.Content) > 0 {
				var content any
				if err := json.Unmarshal(e.Message.Content, &content); err == nil {
					msg["content"] = content
				}
			}
			m["message"] = msg
		}
		out = append(out, m)
	}
	return out
}

// isSidechain returns true if the entry has isSidechain==true.
func isSidechain(raw map[string]any) bool {
	v, _ := raw["isSidechain"].(bool)
	return v
}

// hasVisibleUserText returns true if the user message contains at least one
// non-terminal text block written by a human.
func hasVisibleUserText(raw map[string]any) bool {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return false
	}
	content := msg["content"]
	if content == nil {
		return false
	}

	switch c := content.(type) {
	case string:
		return !isTerminalOutput(c) && strings.TrimSpace(c) != ""
	case []any:
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				if !isTerminalOutput(text) && strings.TrimSpace(text) != "" {
					return true
				}
			case "image", "document":
				return true
			}
		}
	}
	return false
}

// isTerminalOutput returns true if the string contains a terminal-output XML
// opening tag, indicating it is bash/tool output rather than a human prompt.
func isTerminalOutput(s string) bool {
	for _, tag := range terminalOutputTags {
		if strings.Contains(s, "<"+tag+">") {
			return true
		}
	}
	return false
}

// extractContentBlocks extracts the list of content blocks from an assistant
// entry's message.content field.  Handles both string (rare) and array forms.
func extractContentBlocks(raw map[string]any) []contentBlock {
	msg, ok := raw["message"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := msg["content"].([]any)
	if !ok {
		return nil
	}

	out := make([]contentBlock, 0, len(content))
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		cb := contentBlock{
			Type: strField(block, "type"),
			Text: strField(block, "text"),
			Name: strField(block, "name"),
		}
		if inp, ok := block["input"].(map[string]any); ok {
			cb.Input = inp
		}
		out = append(out, cb)
	}
	return out
}

// isMemoryFileAccess mirrors the TypeScript isMemoryFileAccess heuristic from
// sessionFileAccessHooks.ts.  It returns true when the tool call is accessing
// a CLAUDE.md / memory file rather than a regular project file.
//
// Heuristics:
//   - Path contains "CLAUDE.md", ".polypod", or "memory" (case-insensitive)
//   - For Glob/Grep: pattern targets known memory file names
func isMemoryFileAccess(toolName string, input map[string]any) bool {
	if input == nil {
		return false
	}

	// Collect candidate path strings.
	candidates := []string{
		strField(input, "path"),
		strField(input, "file_path"),
		strField(input, "pattern"),
		strField(input, "glob"),
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		lower := strings.ToLower(c)
		if strings.Contains(lower, "claude.md") ||
			strings.Contains(lower, ".polypod") ||
			strings.Contains(lower, "/memory/") ||
			strings.Contains(lower, "memory.md") {
			return true
		}
	}
	return false
}

// strField safely extracts a string value from a map.
func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// ---------------------------------------------------------------------------
// Contribution percentage estimation
// ---------------------------------------------------------------------------

// EstimateClaudeContributionPct produces a rough estimate of Claude's
// contribution percentage based purely on session metrics when git diff data
// is unavailable.
//
// Formula: weight tool calls and message turns.
func EstimateClaudeContributionPct(stats AttributionStats) int {
	if stats.ToolCallCount == 0 && stats.NShotCount == 0 {
		return 0
	}

	// Heuristic: more tool calls relative to user prompts → higher Claude share.
	ratio := float64(stats.ToolCallCount) / math.Max(1, float64(stats.NShotCount))
	// Clamp to [0,1]: ratio=1 → 50%, ratio=10 → ~91%.
	pct := (ratio / (1 + ratio)) * 100
	if pct > 99 {
		pct = 99
	}
	return int(math.Round(pct))
}
