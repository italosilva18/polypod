package tool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/costa/polypod/internal/types"
)

// --- test fixtures ---------------------------------------------------------

// plainTool is a types.Tool that does NOT implement InterruptibleTool.
type plainTool struct{}

func (plainTool) Name() string                { return "plain" }
func (plainTool) Description() string         { return "" }
func (plainTool) InputSchema() map[string]any { return map[string]any{} }
func (plainTool) Execute(types.ToolContext, map[string]any) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}
func (plainTool) ValidateInput(map[string]any) types.ValidationResult {
	return types.ValidationResult{Valid: true}
}
func (plainTool) CheckPermissions(map[string]any, types.ToolContext) types.PermissionResult {
	return types.PermissionResult{Behavior: types.PermissionAllow}
}
func (plainTool) IsConcurrencySafe(map[string]any) bool { return true }
func (plainTool) IsReadOnly(map[string]any) bool        { return true }
func (plainTool) IsDestructive(map[string]any) bool     { return false }
func (plainTool) IsEnabled() bool                       { return true }

// cancellableTool implements InterruptibleTool and returns InterruptCancel.
type cancellableTool struct{ plainTool }

func (cancellableTool) InterruptBehavior() InterruptBehavior { return InterruptCancel }

// graceTool implements InterruptibleTool and returns InterruptGrace.
type graceTool struct{ plainTool }

func (graceTool) InterruptBehavior() InterruptBehavior { return InterruptGrace }

// --- tests -----------------------------------------------------------------

func TestInterruptBehaviorString(t *testing.T) {
	cases := []struct {
		ib   InterruptBehavior
		want string
	}{
		{InterruptBlock, "block"},
		{InterruptCancel, "cancel"},
		{InterruptGrace, "grace"},
	}
	for _, c := range cases {
		if got := c.ib.String(); got != c.want {
			t.Errorf("InterruptBehavior(%d).String() = %q, want %q", int(c.ib), got, c.want)
		}
	}
}

func TestGetInterruptBehavior_DefaultsToBlock(t *testing.T) {
	if got := GetInterruptBehavior(plainTool{}); got != InterruptBlock {
		t.Errorf("plainTool: got %v, want %v", got, InterruptBlock)
	}
	// nil tool is also safe.
	if got := GetInterruptBehavior(nil); got != InterruptBlock {
		t.Errorf("nil tool: got %v, want %v", got, InterruptBlock)
	}
}

func TestGetInterruptBehavior_RespectsInterface(t *testing.T) {
	if got := GetInterruptBehavior(cancellableTool{}); got != InterruptCancel {
		t.Errorf("cancellableTool: got %v, want %v", got, InterruptCancel)
	}
	if got := GetInterruptBehavior(graceTool{}); got != InterruptGrace {
		t.Errorf("graceTool: got %v, want %v", got, InterruptGrace)
	}
}

func TestInterruptManager_RegisterUnregister(t *testing.T) {
	m := NewInterruptManager()
	if m.PendingCount() != 0 {
		t.Fatalf("new manager PendingCount = %d, want 0", m.PendingCount())
	}

	var called int32
	cancel := func() { atomic.AddInt32(&called, 1) }
	m.Register("t1", cancel)

	if m.PendingCount() != 1 {
		t.Fatalf("after Register PendingCount = %d, want 1", m.PendingCount())
	}

	m.Unregister("t1")
	if m.PendingCount() != 0 {
		t.Fatalf("after Unregister PendingCount = %d, want 0", m.PendingCount())
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("Unregister should NOT invoke cancel; called=%d", called)
	}
}

func TestInterruptManager_CancelOne(t *testing.T) {
	m := NewInterruptManager()
	var a, b int32
	m.Register("a", func() { atomic.AddInt32(&a, 1) })
	m.Register("b", func() { atomic.AddInt32(&b, 1) })

	if ok := m.CancelOne("a"); !ok {
		t.Fatal("CancelOne(a) = false, want true")
	}
	if atomic.LoadInt32(&a) != 1 {
		t.Errorf("cancel 'a' not called: %d", a)
	}
	if atomic.LoadInt32(&b) != 0 {
		t.Errorf("cancel 'b' wrongly called: %d", b)
	}
	if m.PendingCount() != 1 {
		t.Errorf("PendingCount = %d, want 1", m.PendingCount())
	}
	if ok := m.CancelOne("nope"); ok {
		t.Error("CancelOne(nope) = true, want false")
	}
}

func TestInterruptManager_CancelAll(t *testing.T) {
	m := NewInterruptManager()
	var count int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		id := string(rune('a' + i))
		m.Register(id, func() {
			atomic.AddInt32(&count, 1)
			wg.Done()
		})
	}
	if m.PendingCount() != 5 {
		t.Fatalf("PendingCount = %d, want 5", m.PendingCount())
	}
	m.CancelAll()
	wg.Wait()
	if atomic.LoadInt32(&count) != 5 {
		t.Errorf("canceled %d, want 5", count)
	}
	if m.PendingCount() != 0 {
		t.Errorf("after CancelAll PendingCount = %d, want 0", m.PendingCount())
	}
}

func TestRunWithInterrupt_Success(t *testing.T) {
	m := NewInterruptManager()
	res, err := RunWithInterrupt(
		context.Background(),
		plainTool{},
		"call-1",
		m,
		func(ctx context.Context) (types.ToolResult, error) {
			return types.ToolResult{Content: "ok"}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("result = %q, want %q", res.Content, "ok")
	}
	if m.PendingCount() != 0 {
		t.Errorf("PendingCount after completion = %d, want 0", m.PendingCount())
	}
}

func TestRunWithInterrupt_CancelBehaviorAborts(t *testing.T) {
	m := NewInterruptManager()
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		_, err := RunWithInterrupt(
			ctx,
			cancellableTool{},
			"call-cancel",
			m,
			func(childCtx context.Context) (types.ToolResult, error) {
				close(started)
				select {
				case <-childCtx.Done():
					return types.ToolResult{}, childCtx.Err()
				case <-time.After(5 * time.Second):
					return types.ToolResult{Content: "should not happen"}, nil
				}
			},
		)
		result <- err
	}()

	<-started
	cancel() // user interrupt

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error on cancel, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunWithInterrupt did not return after Cancel behavior")
	}

	if m.PendingCount() != 0 {
		t.Errorf("PendingCount after cancel = %d, want 0", m.PendingCount())
	}
}

func TestRunWithInterrupt_BlockBehaviorWaits(t *testing.T) {
	m := NewInterruptManager()
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	type out struct {
		res types.ToolResult
		err error
	}
	result := make(chan out, 1)

	go func() {
		res, err := RunWithInterrupt(
			ctx,
			plainTool{}, // blocks by default
			"call-block",
			m,
			func(childCtx context.Context) (types.ToolResult, error) {
				close(started)
				// Ignore parent interrupt, finish on our own terms.
				time.Sleep(150 * time.Millisecond)
				return types.ToolResult{Content: "finished"}, nil
			},
		)
		result <- out{res, err}
	}()

	<-started
	cancel() // user interrupt — should be ignored for Block

	select {
	case o := <-result:
		if o.err != nil {
			t.Fatalf("unexpected err: %v", o.err)
		}
		if o.res.Content != "finished" {
			t.Errorf("content = %q, want %q", o.res.Content, "finished")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Block tool never returned")
	}
}

func TestRunWithInterrupt_GraceBehavior(t *testing.T) {
	m := NewInterruptManager()

	// Shorten grace for the test.
	orig := GraceDuration
	GraceDuration = 50 * time.Millisecond
	defer func() { GraceDuration = orig }()

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		_, err := RunWithInterrupt(
			ctx,
			graceTool{},
			"call-grace",
			m,
			func(childCtx context.Context) (types.ToolResult, error) {
				close(started)
				select {
				case <-childCtx.Done():
					return types.ToolResult{}, childCtx.Err()
				case <-time.After(5 * time.Second):
					return types.ToolResult{}, nil
				}
			},
		)
		result <- err
	}()

	<-started
	start := time.Now()
	cancel()

	select {
	case err := <-result:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected grace cancel error, got nil")
		}
		if elapsed < 40*time.Millisecond {
			t.Errorf("returned too early: %v (grace=%v)", elapsed, GraceDuration)
		}
		if elapsed > 1*time.Second {
			t.Errorf("returned too late: %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("grace tool never returned")
	}
}
