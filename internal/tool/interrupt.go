// Package tool — interrupt.go adds an abstraction for tool interrupt behavior.
//
// Claude Code TS tools expose `interruptBehavior(): 'cancel' | 'block'` which
// controls what happens when the user sends Ctrl+C while a tool call is in
// flight:
//
//   - 'block'  — the interrupt is queued, the tool keeps running, and the
//     user waits for completion. This is the safe default for
//     operations that would corrupt state if aborted mid-flight
//     (file writes, multi-step edits, git mutations).
//   - 'cancel' — the tool's context is cancelled immediately, the tool is
//     expected to unwind cleanly. Used for read-only or idempotent
//     operations (file reads, searches, network fetches).
//
// Polypod's existing Tool interface always blocks. This file introduces an
// optional interface tools can implement to opt into cancellation, plus an
// InterruptManager the orchestrator uses to track in-flight tool calls and
// cancel them on demand.
package tool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// InterruptBehavior describes how a tool responds to a user interrupt while
// it is executing.
type InterruptBehavior int

const (
	// InterruptBlock is the default. The tool ignores user interrupts and
	// runs to completion; the interrupt is delivered once it returns.
	InterruptBlock InterruptBehavior = iota

	// InterruptCancel causes the tool's context to be cancelled as soon as
	// the user interrupts. Safe only for idempotent / read-only tools.
	InterruptCancel

	// InterruptGrace waits for GraceDuration after the interrupt, then
	// cancels the tool's context if it is still running. Useful for tools
	// that can finish cleanly if given a short moment.
	InterruptGrace
)

// String implements fmt.Stringer for human-readable logging.
func (ib InterruptBehavior) String() string {
	switch ib {
	case InterruptBlock:
		return "block"
	case InterruptCancel:
		return "cancel"
	case InterruptGrace:
		return "grace"
	default:
		return fmt.Sprintf("InterruptBehavior(%d)", int(ib))
	}
}

// InterruptibleTool is an optional interface tools may implement to declare
// their interrupt semantics. Tools that do not implement this interface are
// assumed to block (the conservative default).
type InterruptibleTool interface {
	InterruptBehavior() InterruptBehavior
}

// GetInterruptBehavior returns the interrupt behavior declared by t, or
// InterruptBlock if t does not implement InterruptibleTool. The type
// assertion is safe and never panics.
func GetInterruptBehavior(t types.Tool) InterruptBehavior {
	if t == nil {
		return InterruptBlock
	}
	if ib, ok := t.(InterruptibleTool); ok {
		return ib.InterruptBehavior()
	}
	return InterruptBlock
}

// GraceDuration is the default window InterruptGrace tools receive before
// their context is forcibly cancelled. It is a package-level var so tests
// and callers can override it.
var GraceDuration = 5 * time.Second

// InterruptManager tracks in-flight tool calls and exposes methods to cancel
// them. The orchestrator creates one per session and registers every tool
// invocation with it.
//
// All methods are safe for concurrent use.
type InterruptManager struct {
	mu      sync.Mutex
	pending map[string]context.CancelFunc
}

// NewInterruptManager returns an empty InterruptManager ready for use.
func NewInterruptManager() *InterruptManager {
	return &InterruptManager{
		pending: make(map[string]context.CancelFunc),
	}
}

// Register records a cancel function for the given tool-use ID. If a cancel
// func was already registered under the same ID it is overwritten (the
// previous one is NOT called — callers own lifetime).
func (m *InterruptManager) Register(toolUseID string, cancel context.CancelFunc) {
	if m == nil || cancel == nil || toolUseID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = make(map[string]context.CancelFunc)
	}
	m.pending[toolUseID] = cancel
}

// Unregister removes the entry for toolUseID without cancelling it. Called
// by the tool runner once execution finishes normally.
func (m *InterruptManager) Unregister(toolUseID string) {
	if m == nil || toolUseID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pending, toolUseID)
}

// CancelAll cancels every pending tool and clears the registry.
func (m *InterruptManager) CancelAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.pending))
	for _, c := range m.pending {
		cancels = append(cancels, c)
	}
	m.pending = make(map[string]context.CancelFunc)
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// CancelOne cancels a specific pending tool by ID. Returns true if an entry
// was found and cancelled, false otherwise.
func (m *InterruptManager) CancelOne(toolUseID string) bool {
	if m == nil || toolUseID == "" {
		return false
	}
	m.mu.Lock()
	cancel, ok := m.pending[toolUseID]
	if ok {
		delete(m.pending, toolUseID)
	}
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// PendingCount returns the number of tools currently registered as running.
func (m *InterruptManager) PendingCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// RunWithInterrupt executes a tool under the supervision of an
// InterruptManager, honouring the tool's declared InterruptBehavior.
//
// The lifecycle is:
//  1. A child context derived from ctx is created and registered with mgr
//     under toolUseID.
//  2. execute runs on a goroutine with that child context.
//  3. While execute is running, RunWithInterrupt watches ctx.Done(). If the
//     parent context is cancelled (user interrupt):
//     - InterruptBlock  — wait for execute to finish on its own.
//     - InterruptCancel — cancel the child context immediately.
//     - InterruptGrace  — wait GraceDuration, then cancel the child context.
//  4. The entry is unregistered from mgr before returning.
//
// The child context is *always* cancelled before this function returns, so
// the caller does not need to defer anything.
func RunWithInterrupt(
	ctx context.Context,
	tool types.Tool,
	toolUseID string,
	mgr *InterruptManager,
	execute func(context.Context) (types.ToolResult, error),
) (types.ToolResult, error) {
	if execute == nil {
		return types.ToolResult{}, fmt.Errorf("RunWithInterrupt: execute is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	behavior := GetInterruptBehavior(tool)

	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if mgr != nil && toolUseID != "" {
		mgr.Register(toolUseID, cancel)
		defer mgr.Unregister(toolUseID)
	}

	type outcome struct {
		result types.ToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := execute(childCtx)
		done <- outcome{result: res, err: err}
	}()

	select {
	case out := <-done:
		return out.result, out.err

	case <-ctx.Done():
		switch behavior {
		case InterruptCancel:
			cancel()
			out := <-done
			if out.err == nil {
				out.err = ctx.Err()
			}
			return out.result, out.err

		case InterruptGrace:
			timer := time.NewTimer(GraceDuration)
			defer timer.Stop()
			select {
			case out := <-done:
				return out.result, out.err
			case <-timer.C:
				cancel()
				out := <-done
				if out.err == nil {
					out.err = ctx.Err()
				}
				return out.result, out.err
			}

		case InterruptBlock:
			fallthrough
		default:
			out := <-done
			return out.result, out.err
		}
	}
}
