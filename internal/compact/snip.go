package compact

// This file implements the "snip" strategy for context collapse.
//
// Snipping is a lossy-but-cheap way to reclaim tokens from a long conversation
// without calling the LLM to summarize. It walks the message history and rewrites
// old tool_result content blocks to a placeholder (optionally keeping a small
// byte-prefix of the original). The last N tool results are left untouched so the
// assistant still has fresh context to work with.
//
// Unlike compact.go (which summarizes whole groups) or microcompact.go (which
// clears specific flagged tool results), snip operates purely on structure: it
// preserves tool_use/tool_result linkage via tool_use_id, doesn't drop any
// messages, and never calls out to the model. This mirrors the TypeScript
// HISTORY_SNIP feature flag in src/query.ts / services/compact/snipCompact.ts
// from Claude Code.

import (
	"github.com/costa/polypod/internal/messages"
)

// SnipPolicy controls how tool results are snipped.
type SnipPolicy struct {
	// KeepLastN is the number of most-recent tool results to keep verbatim.
	// Older tool results are rewritten according to MaxPerResult and Placeholder.
	// Must be >= 0; 0 means "snip everything".
	KeepLastN int

	// MaxPerResult is the maximum number of bytes to keep from each older
	// tool result before the placeholder is appended. 0 means "replace the
	// entire content with the placeholder" (most aggressive).
	MaxPerResult int

	// Placeholder is the replacement text appended (or used alone) for snipped
	// tool results. It should be short and explain to the model what happened.
	Placeholder string
}

// DefaultSnipPolicy returns a SnipPolicy with production-quality defaults.
//
// KeepLastN=5 preserves enough recent tool output for the assistant to reason
// about the latest activity. MaxPerResult=200 keeps a small prefix of each older
// result so the assistant can still see what tool ran (file path, command, etc.)
// without paying for the full payload.
func DefaultSnipPolicy() SnipPolicy {
	return SnipPolicy{
		KeepLastN:    5,
		MaxPerResult: 200,
		Placeholder:  "[tool output snipped to save tokens]",
	}
}

// SnipToolResults returns a new slice of messages in which older tool_result
// blocks have been rewritten according to the policy. The input slice is not
// mutated. Every other content block (text, tool_use, image, thinking) is
// preserved as-is, and the linkage between tool_use and tool_result is kept
// intact via the tool_use_id field.
//
// Messages that contain no tool_result blocks are copied by value (cheap) —
// they still count toward the total but are never rewritten. A ContentBlock
// slice is only allocated when at least one block in that message needs to
// be snipped.
func SnipToolResults(msgs []messages.Message, policy SnipPolicy) []messages.Message {
	if len(msgs) == 0 {
		return []messages.Message{}
	}
	if policy.KeepLastN < 0 {
		policy.KeepLastN = 0
	}
	if policy.MaxPerResult < 0 {
		policy.MaxPerResult = 0
	}

	// First pass: find indexes (message-level) of tool_result-bearing messages,
	// in order. We use this to decide which ones are "recent" (kept) vs "old"
	// (snipped). A single message can carry multiple tool_result blocks; for
	// KeepLastN accounting we count at the message level, which matches how
	// the TS implementation treats per-round tool result bundles.
	var toolResultMsgIdx []int
	for i := range msgs {
		for _, b := range msgs[i].Content {
			if b.Type == messages.ContentToolResult {
				toolResultMsgIdx = append(toolResultMsgIdx, i)
				break
			}
		}
	}

	// Anything at or after this cutoff index is kept verbatim.
	keep := policy.KeepLastN
	if keep > len(toolResultMsgIdx) {
		keep = len(toolResultMsgIdx)
	}
	cutoff := len(toolResultMsgIdx) // default: nothing snipped
	if keep < len(toolResultMsgIdx) {
		cutoff = len(toolResultMsgIdx) - keep
	}

	// Build a set of message indexes that should be snipped (the "old" ones).
	snipSet := make(map[int]struct{}, cutoff)
	for i := 0; i < cutoff; i++ {
		snipSet[toolResultMsgIdx[i]] = struct{}{}
	}

	// Second pass: copy messages, rewriting tool_result blocks where needed.
	out := make([]messages.Message, len(msgs))
	for i, m := range msgs {
		if _, snip := snipSet[i]; !snip {
			// Not an old tool-result message — shallow-copy as-is. We still
			// need a defensive copy of the Content slice header because
			// callers may mutate the returned slice's blocks later; but since
			// we don't touch any blocks here, aliasing the backing array is
			// the documented "no mutation" behavior.
			out[i] = m
			continue
		}

		// Rebuild this message's content, rewriting tool_result blocks.
		newContent := make([]messages.ContentBlock, len(m.Content))
		for j, b := range m.Content {
			if b.Type != messages.ContentToolResult {
				newContent[j] = b
				continue
			}
			// Preserve tool_use_id linking; only the human-readable Content
			// field is touched.
			rewritten := b
			rewritten.Content = snipContent(b.Content, policy.MaxPerResult, policy.Placeholder)
			newContent[j] = rewritten
		}

		copied := m
		copied.Content = newContent
		// Clear the parallel ToolUseResult shadow if set — it would otherwise
		// still carry the pre-snip payload and waste tokens once marshaled.
		copied.ToolUseResult = nil
		out[i] = copied
	}

	return out
}

// snipContent rewrites a single tool_result content string. If maxPerResult
// is 0 or larger than the content, the placeholder replaces the whole string
// (or is a no-op when content is already short enough). Otherwise the first
// maxPerResult bytes of the original are kept and the placeholder is appended
// as a suffix so the model can still see the tool's preamble.
func snipContent(content string, maxPerResult int, placeholder string) string {
	if len(content) == 0 {
		// Nothing to snip, but we still mark it so downstream code knows it
		// was processed. Use the placeholder alone.
		return placeholder
	}
	if maxPerResult <= 0 {
		return placeholder
	}
	if len(content) <= maxPerResult {
		// Already smaller than the budget — leave alone rather than
		// bloat it with a placeholder. This keeps the operation idempotent
		// for short results.
		return content
	}
	// Byte-slice is safe here: tool output is treated as opaque text, and
	// even if we cut mid-rune the placeholder suffix clearly marks the split.
	return content[:maxPerResult] + "\n" + placeholder
}

// EstimateTokenSavings returns a rough estimate (in tokens) of how much
// context was freed by snipping. It compares total content-block byte size
// before and after, then divides by 4 (the conventional "~4 chars per token"
// heuristic also used by EstimateTokens in compact.go).
//
// The value is clamped to >= 0: if for some reason the "after" slice is
// larger (e.g., policy was applied to an already-tiny slice and the
// placeholder was longer than the original), this returns 0 rather than a
// negative number, since "negative savings" is meaningless to callers.
func EstimateTokenSavings(before, after []messages.Message) int {
	beforeBytes := contentBytes(before)
	afterBytes := contentBytes(after)
	if afterBytes >= beforeBytes {
		return 0
	}
	return (beforeBytes - afterBytes) / 4
}

// contentBytes sums the approximate byte size of all content blocks in a
// slice of messages. Matches the fields summed by
// messages.CountTokensInMessage so savings calculations line up with the
// rest of the compact package's token accounting.
func contentBytes(msgs []messages.Message) int {
	total := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case messages.ContentText:
				total += len(b.Text)
			case messages.ContentToolUse:
				total += len(b.Text) + len(b.Name)*2
			case messages.ContentToolResult:
				total += len(b.Content)
			}
		}
	}
	return total
}
