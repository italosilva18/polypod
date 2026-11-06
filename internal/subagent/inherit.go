// Package subagent — inherited execution context for forked children.
//
// When a parent agent forks a child, the child needs to inherit a bundle of
// runtime context: working directory, environment variables, permissions,
// tool allowlist, and session identity. This file defines that bundle and
// the depth-guard that prevents runaway recursive forking.
//
// Integration notes:
//   - Permissions is typed as `any` to avoid importing `internal/types` here
//     (which would risk an import cycle once the advanced query engine is
//     wired in). Callers should cast to their concrete PermissionContext
//     type on use.
//   - Depth is zero at the root conversation and increments by one per fork
//     layer. The guard is intentionally coarse — a more nuanced budget
//     (e.g., total forks per session) belongs in the engine, not here.
package subagent

import "errors"

// MaxForkDepth caps recursive forking. At depth N, a fork attempt that
// would produce depth N+1 is rejected by ValidateForkDepth. Three layers
// is enough for practical orchestrator → worker → verifier flows while
// still blocking runaway self-spawn loops.
const MaxForkDepth = 3

// ErrMaxForkDepth is returned by ValidateForkDepth when a fork would
// exceed MaxForkDepth. Callers should surface this to the model as a
// tool-call error so the parent can choose a different strategy.
var ErrMaxForkDepth = errors.New("subagent: max fork depth exceeded")

// InheritedContext is the runtime bundle threaded from a parent agent into
// its forked child. Every field is copied into the child at fork time.
//
// Zero value is a valid "root" context: Depth=0, no env, no permissions.
// Call Child() to derive a child context with Depth+1 and defensive copies
// of the map/slice fields.
type InheritedContext struct {
	// CWD is the working directory the child should execute in. Typically
	// equal to the parent's CWD, unless the fork happens inside an
	// isolated git worktree (see buildWorktreeNotice in the TS source).
	CWD string

	// Env is the environment variable overlay to apply on top of the
	// process environment. A nil map means "inherit the parent's env
	// verbatim". An empty non-nil map means "apply no overrides".
	Env map[string]string

	// Permissions holds the parent's PermissionContext (from
	// `internal/types`). Stored as `any` to avoid an import cycle.
	Permissions any

	// ToolAllowList restricts which tools the child may call. Empty means
	// "inherit parent's full pool" — matches the TS FORK_AGENT tools:['*']
	// behavior.
	ToolAllowList []string

	// ParentSessionID identifies the originating conversation. Children
	// preserve this value across fork layers so logs and tracing can
	// correlate the whole tree back to the root.
	ParentSessionID string

	// Depth is 0 at the root, 1 at the first fork, and so on.
	Depth int
}

// Child returns a new InheritedContext one level deeper than the receiver.
//
// Defensive-copy semantics:
//   - Env map is cloned so the child's mutations do not leak upward.
//   - ToolAllowList slice is cloned for the same reason.
//   - Permissions is copied by reference — it is opaque here, and
//     concrete PermissionContext types are expected to be either immutable
//     or safely shareable across fork layers.
//
// The receiver is taken by value so callers cannot accidentally observe
// mutations applied here.
func (c InheritedContext) Child() InheritedContext {
	child := InheritedContext{
		CWD:             c.CWD,
		Permissions:     c.Permissions,
		ParentSessionID: c.ParentSessionID,
		Depth:           c.Depth + 1,
	}
	if c.Env != nil {
		child.Env = make(map[string]string, len(c.Env))
		for k, v := range c.Env {
			child.Env[k] = v
		}
	}
	if c.ToolAllowList != nil {
		child.ToolAllowList = make([]string, len(c.ToolAllowList))
		copy(child.ToolAllowList, c.ToolAllowList)
	}
	return child
}

// ValidateForkDepth returns ErrMaxForkDepth when the given context's Depth
// already meets or exceeds MaxForkDepth. Call this BEFORE producing the
// child via Child() — if the parent is already at depth MaxForkDepth, a
// further fork would land at MaxForkDepth+1 and must be rejected.
func ValidateForkDepth(ctx InheritedContext) error {
	if ctx.Depth >= MaxForkDepth {
		return ErrMaxForkDepth
	}
	return nil
}
