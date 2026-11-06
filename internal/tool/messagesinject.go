// Package tool — message injection primitive.
//
// TypeScript tools in Claude Code can return a `newMessages` field from their
// Execute step, letting a tool inject additional conversation turns that are
// appended before the next model call (rather than stuffing everything into
// the tool_result text). Go's ToolResult already carries a NewMessages slice
// (see types.ToolResult), but some tools need to enqueue extra messages from
// deep inside their execution without plumbing return values all the way up.
//
// This file provides that out-of-band primitive: a goroutine-safe queue keyed
// by toolUseID. A tool grabs the injector from its context, calls Enqueue
// during execution, and the orchestrator calls Drain after the tool finishes
// to splice the messages into the conversation.
package tool

import (
	"context"
	"errors"
	"sync"
)

// InjectedMessage is a single conversation turn queued for injection.
//
// Attachments is a free-form slice of map-shaped payloads (images, files,
// tool_use blocks, whatever the caller's renderer can handle). It stays as
// []map[string]any to avoid pulling in a concrete message type from any
// particular provider SDK.
//
// InjectBefore controls ordering relative to the tool_result block:
//   - true  → message is placed before the tool_result in the conversation
//   - false → message is placed after the tool_result
type InjectedMessage struct {
	// Role must be one of "user", "system", or "assistant".
	Role string
	// Content is the text body of the message. Required.
	Content string
	// Attachments carries optional structured payloads alongside the text.
	Attachments []map[string]any
	// InjectBefore: if true, splice before the tool_result; otherwise after.
	InjectBefore bool
}

// MessageInjector is the read/clear side of the injection queue used by the
// orchestrator. Tools use MessageInjectorImpl.Enqueue (or the InjectMessage
// context helper) to push messages.
type MessageInjector interface {
	// PendingInjections returns a copy of the queue for the given toolUseID.
	// It does NOT clear the queue; pair with Clear or use Drain.
	PendingInjections(toolUseID string) []InjectedMessage
	// Clear discards any pending messages for toolUseID.
	Clear(toolUseID string)
}

// MessageInjectorImpl is the default, goroutine-safe MessageInjector.
type MessageInjectorImpl struct {
	mu    sync.RWMutex
	queue map[string][]InjectedMessage
}

// NewMessageInjector returns an empty injector ready for use.
func NewMessageInjector() *MessageInjectorImpl {
	return &MessageInjectorImpl{
		queue: make(map[string][]InjectedMessage),
	}
}

// Enqueue appends one or more messages to the queue for the given toolUseID.
// Safe to call concurrently.
func (m *MessageInjectorImpl) Enqueue(toolUseID string, msgs ...InjectedMessage) {
	if len(msgs) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue[toolUseID] = append(m.queue[toolUseID], msgs...)
}

// PendingInjections returns a defensive copy of the queued messages for
// toolUseID. Returns nil if nothing is queued.
func (m *MessageInjectorImpl) PendingInjections(toolUseID string) []InjectedMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src, ok := m.queue[toolUseID]
	if !ok || len(src) == 0 {
		return nil
	}
	out := make([]InjectedMessage, len(src))
	copy(out, src)
	return out
}

// Clear removes the queue for toolUseID.
func (m *MessageInjectorImpl) Clear(toolUseID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.queue, toolUseID)
}

// Drain atomically returns the queued messages for toolUseID and clears them.
// Returns nil if nothing was queued.
func (m *MessageInjectorImpl) Drain(toolUseID string) []InjectedMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.queue[toolUseID]
	if !ok || len(src) == 0 {
		// Still delete so an empty-but-present entry doesn't linger.
		delete(m.queue, toolUseID)
		return nil
	}
	out := make([]InjectedMessage, len(src))
	copy(out, src)
	delete(m.queue, toolUseID)
	return out
}

// injectorCtxKey is an unexported type so keys don't collide across packages.
type injectorCtxKey struct{}

// WithInjector returns a new context carrying inj. Passing a nil injector is
// allowed and will make InjectorFromContext return nil.
func WithInjector(ctx context.Context, inj MessageInjector) context.Context {
	return context.WithValue(ctx, injectorCtxKey{}, inj)
}

// InjectorFromContext retrieves the MessageInjector stored by WithInjector,
// or nil if none is present.
func InjectorFromContext(ctx context.Context) MessageInjector {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(injectorCtxKey{})
	if v == nil {
		return nil
	}
	inj, ok := v.(MessageInjector)
	if !ok {
		return nil
	}
	return inj
}

// InjectMessage is a convenience helper for tools: it looks up the injector
// on ctx and enqueues msg under toolUseID. If no injector is present, or the
// injector is not an Enqueue-capable *MessageInjectorImpl, the call is a
// silent no-op — tools don't need to care whether the orchestrator wired the
// queue up.
func InjectMessage(ctx context.Context, toolUseID string, msg InjectedMessage) {
	inj := InjectorFromContext(ctx)
	if inj == nil {
		return
	}
	// Only the concrete impl exposes Enqueue (the interface is read-only on
	// purpose). If a custom implementation is provided, callers should use
	// its own enqueue path.
	if impl, ok := inj.(*MessageInjectorImpl); ok {
		impl.Enqueue(toolUseID, msg)
	}
}

// ValidateInjectedMessage checks that m has a supported role and non-empty
// content. Attachments are not validated — they are opaque to this package.
func ValidateInjectedMessage(m InjectedMessage) error {
	switch m.Role {
	case "user", "system", "assistant":
	default:
		return errors.New("injected message: role must be one of user|system|assistant")
	}
	if m.Content == "" {
		return errors.New("injected message: content is required")
	}
	return nil
}
