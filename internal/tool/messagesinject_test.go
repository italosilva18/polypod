package tool

import (
	"context"
	"sync"
	"testing"
)

func TestMessageInjector_EnqueueDrainRoundtrip(t *testing.T) {
	inj := NewMessageInjector()
	msg1 := InjectedMessage{Role: "user", Content: "hello", InjectBefore: true}
	msg2 := InjectedMessage{
		Role:    "system",
		Content: "system note",
		Attachments: []map[string]any{
			{"type": "image", "url": "x.png"},
		},
	}
	inj.Enqueue("tu_1", msg1, msg2)

	got := inj.Drain("tu_1")
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" || !got[0].InjectBefore {
		t.Errorf("msg1 mismatch: %+v", got[0])
	}
	if got[1].Role != "system" || got[1].Content != "system note" {
		t.Errorf("msg2 mismatch: %+v", got[1])
	}
	if len(got[1].Attachments) != 1 || got[1].Attachments[0]["type"] != "image" {
		t.Errorf("attachments not preserved: %+v", got[1].Attachments)
	}
}

func TestMessageInjector_DrainClears(t *testing.T) {
	inj := NewMessageInjector()
	inj.Enqueue("tu_2", InjectedMessage{Role: "user", Content: "once"})

	first := inj.Drain("tu_2")
	if len(first) != 1 {
		t.Fatalf("first drain: want 1, got %d", len(first))
	}

	second := inj.Drain("tu_2")
	if second != nil {
		t.Fatalf("second drain should be nil, got %v", second)
	}

	// PendingInjections should also see nothing.
	if p := inj.PendingInjections("tu_2"); p != nil {
		t.Errorf("PendingInjections after drain should be nil, got %v", p)
	}
}

func TestMessageInjector_ClearDropsQueue(t *testing.T) {
	inj := NewMessageInjector()
	inj.Enqueue("tu_3",
		InjectedMessage{Role: "user", Content: "a"},
		InjectedMessage{Role: "user", Content: "b"},
	)

	// PendingInjections is non-destructive — should still find them.
	if p := inj.PendingInjections("tu_3"); len(p) != 2 {
		t.Fatalf("pending: want 2, got %d", len(p))
	}
	// A second call should still return them (not cleared).
	if p := inj.PendingInjections("tu_3"); len(p) != 2 {
		t.Fatalf("pending (second call): want 2, got %d", len(p))
	}

	inj.Clear("tu_3")
	if p := inj.PendingInjections("tu_3"); p != nil {
		t.Errorf("after Clear, pending should be nil, got %v", p)
	}
}

func TestWithInjectorAndFromContext(t *testing.T) {
	inj := NewMessageInjector()
	ctx := WithInjector(context.Background(), inj)

	got := InjectorFromContext(ctx)
	if got == nil {
		t.Fatal("InjectorFromContext returned nil")
	}
	// Should be the same underlying injector — enqueue via ctx path,
	// drain via direct call.
	InjectMessage(ctx, "tu_ctx", InjectedMessage{Role: "assistant", Content: "via ctx"})

	drained := inj.Drain("tu_ctx")
	if len(drained) != 1 || drained[0].Content != "via ctx" {
		t.Errorf("expected 1 message enqueued via ctx, got %+v", drained)
	}
}

func TestInjectMessage_NoOpWithoutInjector(t *testing.T) {
	// Empty context, no injector. Must not panic.
	InjectMessage(context.Background(), "tu_none", InjectedMessage{
		Role:    "user",
		Content: "nowhere",
	})

	// Nil context should also be safe.
	if inj := InjectorFromContext(nil); inj != nil { //nolint:staticcheck // intentional nil-ctx test
		t.Errorf("InjectorFromContext(nil) should be nil, got %v", inj)
	}

	// Context with a non-injector value under a different key should return nil.
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "unrelated")
	if inj := InjectorFromContext(ctx); inj != nil {
		t.Errorf("unrelated ctx value leaked into InjectorFromContext: %v", inj)
	}
}

func TestMessageInjector_ConcurrentEnqueue(t *testing.T) {
	inj := NewMessageInjector()
	const (
		goroutines = 32
		perG       = 50
	)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				inj.Enqueue("tu_race", InjectedMessage{
					Role:    "user",
					Content: "concurrent",
				})
			}
		}(g)
	}
	wg.Wait()

	got := inj.Drain("tu_race")
	if len(got) != goroutines*perG {
		t.Errorf("want %d messages, got %d", goroutines*perG, len(got))
	}
	// After drain the queue must be empty even under concurrency.
	if p := inj.PendingInjections("tu_race"); p != nil {
		t.Errorf("after Drain: pending should be nil, got %d", len(p))
	}
}

func TestValidateInjectedMessage(t *testing.T) {
	cases := []struct {
		name    string
		msg     InjectedMessage
		wantErr bool
	}{
		{"user ok", InjectedMessage{Role: "user", Content: "hi"}, false},
		{"system ok", InjectedMessage{Role: "system", Content: "rule"}, false},
		{"assistant ok", InjectedMessage{Role: "assistant", Content: "ack"}, false},
		{"bad role", InjectedMessage{Role: "tool", Content: "x"}, true},
		{"empty role", InjectedMessage{Role: "", Content: "x"}, true},
		{"empty content", InjectedMessage{Role: "user", Content: ""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInjectedMessage(tc.msg)
			if tc.wantErr && err == nil {
				t.Errorf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
