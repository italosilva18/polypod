// Package subagent — fork-and-inherit primitive.
//
// This file implements the Go port of the TypeScript `forkSubagent` primitive
// from `src/tools/AgentTool/forkSubagent.ts`. The existing executor
// (`executor.go`) spawns child agents with a fresh context (system prompt +
// one user query). The fork primitive instead inherits the parent's entire
// conversation so the child can reuse the parent's prompt cache (Anthropic
// prompt caching requires byte-identical prefixes).
//
// Integration notes:
//   - This file intentionally does NOT wire to the existing SubAgentExecutor.
//     The real integration will be performed by a future change in the main
//     query package, which will supply a concrete `ForkEngine` implementation
//     that talks to the advanced query engine / provider loop.
//   - `Message`-typed slices are kept as `[]any` to avoid an import cycle
//     with the call site (the conversation type lives above this package in
//     the dependency graph). Call sites cast as needed.
//   - `BuildForkedMessages` is the byte-identical-prefix builder. It MUST NOT
//     mutate its input slice, because callers frequently retain the parent
//     conversation for their own rendering/logging.
package subagent

import (
	"context"
	"fmt"
	"strings"
)

// ForkBoilerplateTag is the exact XML-ish marker prepended to a forked
// child's initial user message. Mirrors `FORK_BOILERPLATE_TAG` in TS
// (`src/constants/xml.ts`) — used both to stamp forked directives and to
// detect them later via IsForkedChild, preventing recursive fork loops.
//
// Keep this string byte-identical with the TS constant so a Go-side detector
// can recognize conversations minted by the TS CLI and vice versa.
const ForkBoilerplateTag = "<forked-subagent>"

// forkDirectivePrefix mirrors the TS `FORK_DIRECTIVE_PREFIX`. The directive
// text is appended after this prefix so both runtimes produce byte-compatible
// child messages.
const forkDirectivePrefix = "Directive: "

// ForkRequest is the input for a ForkEngine run.
//
// ParentMessages is typed as []any to avoid an import cycle — the concrete
// element type is whatever Message type the caller uses (e.g., the executor's
// openai.ChatCompletionMessage, or a richer advanced-query message struct).
// The ForkEngine implementation on the other side is responsible for
// type-asserting the elements back to its native message type.
type ForkRequest struct {
	// ParentMessages is the full conversation history of the parent at the
	// moment of the fork. The first N-1 elements will be copied verbatim by
	// BuildForkedMessages so the API request prefix stays byte-identical.
	ParentMessages []any

	// ChildInstruction is the per-fork directive (the "scope") that differs
	// between sibling forks. It is appended as the last user message,
	// wrapped in ForkBoilerplateTag.
	ChildInstruction string

	// ChildAgentName is the logical agent identity to bill the fork against.
	// Usually the synthetic "fork" agent, but may be any registered agent.
	ChildAgentName string

	// MaxTurns caps the child's tool-calling loop. Zero means "use engine
	// default".
	MaxTurns int

	// ToolAllowList, when non-empty, restricts the child's tool pool. When
	// empty, the child inherits the parent's full pool (the TS behavior for
	// the implicit-fork path — see FORK_AGENT in forkSubagent.ts:60).
	ToolAllowList []string
}

// ForkResult is the output of a ForkEngine run.
type ForkResult struct {
	// Messages is the full child conversation after completion (including
	// the inherited prefix). Callers can inspect the tail for the final
	// assistant text.
	Messages []any

	// StopReason is engine-defined: "end_turn", "max_turns", "max_tokens",
	// "cancelled", etc.
	StopReason string

	// TokensUsed is the total prompt+completion tokens consumed by the
	// child. Used by the parent for budget accounting.
	TokensUsed int

	// ChildAgentName echoes the request for convenience.
	ChildAgentName string
}

// ForkEngine is the abstraction that actually runs a fork. It is implemented
// by the query engine at wire-up time (outside this package) and injected
// into whatever dispatches forks — this keeps the subagent package free of
// provider/loop dependencies and avoids an import cycle.
//
// A concrete implementation will:
//  1. Take req.ParentMessages as the starting conversation.
//  2. Call BuildForkedMessages(req.ParentMessages, req.ChildInstruction).
//  3. Drive the provider loop with the child's tool allowlist until stop.
//  4. Return a ForkResult whose Messages tail carries the final reply.
type ForkEngine interface {
	Run(ctx context.Context, req ForkRequest) (*ForkResult, error)
}

// BuildForkedMessages returns a new message slice that inherits `parentMessages`
// verbatim and appends a single forked-child directive as the final user
// message.
//
// Contract:
//   - The input slice is NOT mutated. We allocate a new backing array with
//     len(parentMessages)+1 capacity.
//   - Element references (the `any` values) are copied by value — we do not
//     deep-clone them. This is intentional: prompt caching is byte-level, so
//     the child MUST observe the exact same assistant/tool_use blocks as the
//     parent. Deep cloning risks diverging pointer-equal helper fields.
//   - The trailing user message is produced by ChildDirective(directive) so
//     the boilerplate tag + directive prefix match the TS emitter byte for
//     byte.
//
// Note: unlike the TS version, we do NOT synthesize tool_result placeholders
// for in-flight parent tool_use blocks. The TS path must do so because the
// TS parent hands off mid-turn; in the Go port, forks are dispatched from a
// settled parent conversation, so the caller is responsible for ensuring the
// tail of parentMessages is a valid conversational state (no dangling
// tool_use without a tool_result). If a future integration needs the
// mid-turn handoff, extend BuildForkedMessages — do NOT rewrite the caller.
func BuildForkedMessages(parentMessages []any, childInstruction string) []any {
	out := make([]any, len(parentMessages), len(parentMessages)+1)
	copy(out, parentMessages)
	out = append(out, ChildDirective(childInstruction))
	return out
}

// ChildDirective formats the user-message text for a forked child. Exported
// so tests (and any future non-default caller) can assert byte-identity
// against the TS emitter.
//
// The returned value is a plain string for simplicity. Callers holding a
// richer Message type should wrap it in their native user-message
// constructor (e.g., createUserMessage with a single text block).
func ChildDirective(directive string) string {
	var b strings.Builder
	b.Grow(len(ForkBoilerplateTag)*2 + len(forkDirectivePrefix) + len(directive) + 8)
	b.WriteString(ForkBoilerplateTag)
	b.WriteString("\n")
	b.WriteString(forkDirectivePrefix)
	b.WriteString(directive)
	b.WriteString("\n</")
	b.WriteString(strings.TrimPrefix(ForkBoilerplateTag, "<"))
	return b.String()
}

// IsForkedChild reports whether a conversation was started by a fork.
//
// It scans the messages from newest to oldest looking for the fork
// boilerplate tag in any string-shaped user content. This mirrors the TS
// `isInForkChild` helper. Because messages are typed as `any` here, we
// fall back to fmt-like stringification via a type switch on the common
// shapes:
//   - plain string
//   - types implementing fmt.Stringer
//   - struct literals with a "Content" string field (accessed via the
//     shared helper below, which uses a small reflection-free switch to
//     avoid pulling in reflect for the hot path).
//
// Unknown shapes are skipped. If you add a new Message type to the
// codebase, either (a) ensure its Content is a plain string, or (b)
// extend messageText below.
func IsForkedChild(messages []any) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		text := messageText(messages[i])
		if text == "" {
			continue
		}
		if strings.Contains(text, ForkBoilerplateTag) {
			return true
		}
	}
	return false
}

// messageText extracts best-effort string content from a message value for
// the IsForkedChild scanner. It deliberately handles only a small set of
// shapes to stay allocation-free and reflect-free.
func messageText(m any) string {
	switch v := m.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case interface{ GetContent() string }:
		return v.GetContent()
	}
	// Struct-by-value path: try the common Content field via a tiny
	// interface assertion. We keep this intentionally narrow.
	if c, ok := m.(interface{ ContentString() string }); ok {
		return c.ContentString()
	}
	return ""
}
