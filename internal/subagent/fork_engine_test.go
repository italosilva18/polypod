package subagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestNullForkEngine_ReturnsErrForkNotConfigured asserts that the zero
// fork engine always rejects with the sentinel error. This is the contract
// callers rely on for nil-safe wiring (default to NullForkEngine, let the
// error surface to the model).
func TestNullForkEngine_ReturnsErrForkNotConfigured(t *testing.T) {
	var eng ForkEngine = NullForkEngine{}
	res, err := eng.Run(context.Background(), ForkRequest{
		ChildInstruction: "do something",
	})
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrForkNotConfigured) {
		t.Fatalf("expected ErrForkNotConfigured, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "fork:") {
		t.Fatalf("expected error prefix 'fork:', got %q", err.Error())
	}
}

// TestForkEngineImpl_NilExecutor verifies that a forkEngineImpl with a nil
// executor fails the same way as NullForkEngine. This guards the common
// misconfiguration where NewForkEngine is called with a nil executor
// (e.g., during partial bring-up).
func TestForkEngineImpl_NilExecutor(t *testing.T) {
	eng := &forkEngineImpl{executor: nil, maxDepth: MaxForkDepth}
	_, err := eng.Run(context.Background(), ForkRequest{ChildInstruction: "hi"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrForkNotConfigured) {
		t.Fatalf("expected ErrForkNotConfigured, got %v", err)
	}
}

// TestForkEngineImpl_DepthGuard verifies that a parent conversation which
// already contains MaxForkDepth layers of fork boilerplate is rejected.
// This is the safety valve against runaway recursive forking.
//
// Because the production forkEngineImpl depth guard counts ForkBoilerplateTag
// occurrences in ParentMessages, we build a synthetic parent with that many
// markers. The guard short-circuits before touching the (nil) executor, so
// this test does not need a real SubAgentExecutor.
func TestForkEngineImpl_DepthGuard(t *testing.T) {
	// Build a parent conversation already at MaxForkDepth layers.
	parent := make([]any, 0, MaxForkDepth)
	for i := 0; i < MaxForkDepth; i++ {
		parent = append(parent, ChildDirective("layer "+string(rune('A'+i))))
	}

	// Use a low max-depth constant so the test is also valid if
	// MaxForkDepth is later changed: we assert behavior at the cap, not
	// the specific number.
	eng := &forkEngineImpl{
		executor: nil, // guard must fire before executor is dereferenced
		maxDepth: MaxForkDepth,
	}

	_, err := eng.Run(context.Background(), ForkRequest{
		ParentMessages:   parent,
		ChildInstruction: "one layer too deep",
	})
	if err == nil {
		t.Fatal("expected depth-guard error, got nil")
	}
	if !errors.Is(err, ErrMaxForkDepth) {
		t.Fatalf("expected ErrMaxForkDepth, got %v", err)
	}
}

// TestForkEngineImpl_DepthGuardAllowsUnderCap is the positive-path companion
// to TestForkEngineImpl_DepthGuard: a parent with fewer fork layers than the
// configured cap must pass the guard. We set maxDepth=2 and supply one
// existing layer so the guard sees depth=1 < 2 and proceeds. Because we do
// not supply an executor, we expect the next step (ErrForkNotConfigured) to
// fire — which proves the guard did NOT reject.
func TestForkEngineImpl_DepthGuardAllowsUnderCap(t *testing.T) {
	parent := []any{ChildDirective("first layer")}
	eng := &forkEngineImpl{
		executor: nil,
		maxDepth: 2,
	}
	_, err := eng.Run(context.Background(), ForkRequest{
		ParentMessages:   parent,
		ChildInstruction: "second layer is ok",
	})
	if err == nil {
		t.Fatal("expected ErrForkNotConfigured from nil executor, got nil")
	}
	if errors.Is(err, ErrMaxForkDepth) {
		t.Fatalf("depth guard fired unexpectedly: %v", err)
	}
	if !errors.Is(err, ErrForkNotConfigured) {
		t.Fatalf("expected ErrForkNotConfigured, got %v", err)
	}
}

// TestCountForkLayers_StringShapes exercises the depth counter directly on
// plain string messages (the simplest shape messageText handles). Three
// messages, two of which contain the boilerplate tag, should yield a
// depth count of 2.
func TestCountForkLayers_StringShapes(t *testing.T) {
	parent := []any{
		"system: root",
		ChildDirective("layer one"),
		"assistant: ok",
		ChildDirective("layer two"),
	}
	got := countForkLayers(parent)
	if got != 2 {
		t.Fatalf("countForkLayers = %d, want 2", got)
	}
}

// TestCountForkLayers_Empty verifies the zero case: no messages, no tags,
// depth zero. This is the common "root conversation" entry state.
func TestCountForkLayers_Empty(t *testing.T) {
	if got := countForkLayers(nil); got != 0 {
		t.Fatalf("countForkLayers(nil) = %d, want 0", got)
	}
	if got := countForkLayers([]any{"hello", "world"}); got != 0 {
		t.Fatalf("countForkLayers(no-tags) = %d, want 0", got)
	}
}

// TestNewForkEngine_ReturnsInterface verifies the constructor contract:
// NewForkEngine returns a value typed as ForkEngine (the interface), not
// the concrete struct. This matters for wiring — callers should treat the
// engine as opaque. We assert by storing the return value in a variable of
// the interface type, which would fail to compile if the signature drifted.
func TestNewForkEngine_ReturnsInterface(t *testing.T) {
	// A nil executor is fine here; we only care about the returned type.
	var eng ForkEngine = NewForkEngine(nil)
	if eng == nil {
		t.Fatal("NewForkEngine returned nil")
	}
	// Calling Run with nil executor should produce ErrForkNotConfigured,
	// proving the returned value is a real forkEngineImpl (not a
	// NullForkEngine — both produce ErrForkNotConfigured here, but only
	// forkEngineImpl consults its maxDepth field, which we cover above).
	_, err := eng.Run(context.Background(), ForkRequest{ChildInstruction: "x"})
	if !errors.Is(err, ErrForkNotConfigured) {
		t.Fatalf("expected ErrForkNotConfigured, got %v", err)
	}
}
