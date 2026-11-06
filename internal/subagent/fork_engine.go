// Package subagent — concrete ForkEngine implementation.
//
// This file bridges the abstract ForkEngine defined in fork.go with the
// existing SubAgentExecutor. It is kept separate from fork.go to preserve
// the abstraction boundary: fork.go stays dependency-free (no provider /
// openai / tool registry imports), while fork_engine.go pulls in the
// concrete executor and adapts between the two message representations.
//
// Two implementations live here:
//
//   - forkEngineImpl — delegates to a real *SubAgentExecutor.
//   - NullForkEngine — a safe zero value that always returns
//     ErrForkNotConfigured. Used as the default wiring when the host
//     application has not yet supplied a real engine.
package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ErrForkNotConfigured is returned by NullForkEngine.Run and indicates that
// a fork was requested but no concrete ForkEngine has been wired in. Callers
// (typically a tool handler on the parent) should translate this into a
// user-visible error explaining that forking is unavailable in the current
// deployment.
var ErrForkNotConfigured = errors.New("fork engine not configured")

// forkEngineImpl is the production ForkEngine. It composes the existing
// SubAgentExecutor (which owns the AI client, tool registry, and agent
// registry) with the fork-specific message builder + depth guard.
//
// The maxDepth field is intentionally per-instance rather than a package
// constant — this lets tests inject a lower value to exercise the guard
// without running real API calls. Zero means "use MaxForkDepth".
type forkEngineImpl struct {
	executor *SubAgentExecutor
	maxDepth int
}

// NewForkEngine constructs a production ForkEngine backed by the given
// SubAgentExecutor. Returning the interface (not the concrete type) is
// deliberate: callers should treat forking as an opaque capability and
// swap in NullForkEngine when the executor is absent.
func NewForkEngine(executor *SubAgentExecutor) ForkEngine {
	return &forkEngineImpl{
		executor: executor,
		maxDepth: MaxForkDepth,
	}
}

// Run executes a fork request.
//
// Flow:
//  1. Depth guard — count existing fork boilerplate tags in the parent
//     conversation; each prior fork layer contributed exactly one. A new
//     fork lands at depth = currentDepth + 1, so reject when the current
//     depth already meets the cap.
//  2. Build the byte-identical child message list via BuildForkedMessages.
//     This is load-bearing for prompt caching even though the executor
//     below currently re-derives its own prefix — future integrations that
//     swap in a message-aware executor will consume this directly.
//  3. Delegate to the executor's Execute method (the only public entry
//     point on the current SubAgentExecutor). Until the executor learns
//     to accept a pre-built message list, we pass the child directive as
//     the `query` argument. The built prefix is still returned on the
//     ForkResult so callers/logs can observe the full conversation.
//  4. Wrap the executor's string output in a final assistant message and
//     translate to ForkResult.
//
// Errors are wrapped with "fork: " for easy grep-ability at the edge.
func (f *forkEngineImpl) Run(ctx context.Context, req ForkRequest) (*ForkResult, error) {
	// 1. Depth guard. We compute the current depth from the parent
	// conversation's own fork markers so the engine works without the
	// caller having to thread an InheritedContext through every layer.
	// This matches the contract in the spec: "reject if req has an
	// embedded InheritedContext with depth >= MaxForkDepth" — the effective
	// depth is derivable from the messages themselves.
	//
	// The depth check runs BEFORE the nil-executor check so a misconfigured
	// engine still rejects runaway recursive forks loudly rather than
	// silently shadowing the depth error with a wiring error.
	depthCap := f.maxDepth
	if depthCap <= 0 {
		depthCap = MaxForkDepth
	}
	currentDepth := countForkLayers(req.ParentMessages)
	if currentDepth >= depthCap {
		return nil, fmt.Errorf("fork: %w (depth=%d, max=%d)",
			ErrMaxForkDepth, currentDepth, depthCap)
	}

	if f.executor == nil {
		return nil, fmt.Errorf("fork: %w", ErrForkNotConfigured)
	}

	// 2. Build the child message prefix. We do not mutate req.ParentMessages.
	childMessages := BuildForkedMessages(req.ParentMessages, req.ChildInstruction)

	// 3. Resolve the child agent name and delegate.
	agentName := req.ChildAgentName
	if agentName == "" {
		agentName = "task"
	}

	// The executor's public Execute method takes the directive as a plain
	// query string. We pass the full directive (including the boilerplate
	// tag) so downstream prompts can detect they are running as a forked
	// child via IsForkedChild on their own messages.
	directive := ChildDirective(req.ChildInstruction)

	reply, err := f.executor.Execute(ctx, agentName, directive)
	if err != nil {
		return nil, fmt.Errorf("fork: executor failed: %w", err)
	}

	// 4. Append the assistant reply to the built child messages so callers
	// can inspect a complete conversation trace. We use openai.ChatCompletionMessage
	// here because that is what the rest of the subagent package uses;
	// consumers that want a different shape can re-walk Messages.
	finalMessages := append(childMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: reply,
	})

	return &ForkResult{
		Messages:       finalMessages,
		StopReason:     "end_turn",
		TokensUsed:     0, // executor does not surface token counts through Execute
		ChildAgentName: agentName,
	}, nil
}

// NullForkEngine is a no-op ForkEngine. It always fails with
// ErrForkNotConfigured. Use it as the default wiring when the host has not
// supplied a real fork implementation — this keeps callers simple (they
// never have to nil-check the engine) while still producing a clear error
// if a fork tool is invoked in that configuration.
type NullForkEngine struct{}

// Run implements ForkEngine by returning ErrForkNotConfigured. The error
// is wrapped so it shows up consistently with forkEngineImpl's error
// prefix ("fork: ...").
func (NullForkEngine) Run(ctx context.Context, req ForkRequest) (*ForkResult, error) {
	return nil, fmt.Errorf("fork: %w", ErrForkNotConfigured)
}

// countForkLayers reports how many fork-boilerplate tags are already
// present in the given message list. Each prior fork layer appended
// exactly one ChildDirective (which contains ForkBoilerplateTag), so the
// count equals the current fork depth of the conversation.
//
// We iterate the full list rather than using IsForkedChild so the result
// is a count, not a boolean. Message shapes that messageText cannot read
// contribute zero — this matches IsForkedChild's best-effort policy.
func countForkLayers(messages []any) int {
	n := 0
	for _, m := range messages {
		text := messageText(m)
		if text == "" {
			continue
		}
		n += strings.Count(text, ForkBoilerplateTag)
	}
	return n
}
