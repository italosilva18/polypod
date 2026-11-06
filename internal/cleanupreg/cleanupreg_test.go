package cleanupreg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_AddRun(t *testing.T) {
	r := NewRegistry()

	var called int32
	r.Add("test", 0, func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	errs := r.Run(context.Background(), 5*time.Second)

	if len(errs) != 0 {
		t.Errorf("Run returned errors: %v", errs)
	}
	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("called = %d; want 1", atomic.LoadInt32(&called))
	}
}

func TestRegistry_Priority(t *testing.T) {
	r := NewRegistry()

	var order []string
	var mu sync.Mutex

	add := func(name string, priority int) {
		r.Add(name, priority, func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, name)
			return nil
		})
	}

	add("low", 1)
	add("high", 100)
	add("medium", 50)

	r.Run(context.Background(), 5*time.Second)

	mu.Lock()
	defer mu.Unlock()

	want := []string{"high", "medium", "low"}
	for i, name := range order {
		if name != want[i] {
			t.Errorf("order[%d] = %s; want %s", i, name, want[i])
		}
	}
}

func TestRegistry_ErrorCollection(t *testing.T) {
	r := NewRegistry()

	r.Add("ok", 0, func(ctx context.Context) error { return nil })
	r.Add("fail1", 0, func(ctx context.Context) error { return errors.New("err1") })
	r.Add("fail2", 0, func(ctx context.Context) error { return errors.New("err2") })

	errs := r.Run(context.Background(), 5*time.Second)

	if len(errs) != 2 {
		t.Errorf("got %d errors; want 2", len(errs))
	}
}

func TestRegistry_Timeout(t *testing.T) {
	r := NewRegistry()

	r.Add("slow", 0, func(ctx context.Context) error {
		select {
		case <-time.After(1 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	start := time.Now()
	errs := r.Run(context.Background(), 100*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("Run took %v; want < 500ms (timeout)", elapsed)
	}
	if len(errs) == 0 {
		t.Error("Run should return timeout error")
	}
}

func TestRegistry_RunOnce(t *testing.T) {
	r := NewRegistry()

	var called int32
	r.Add("test", 0, func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	r.Run(context.Background(), 5*time.Second)
	r.Run(context.Background(), 5*time.Second) // second call should be no-op

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("called = %d; want 1 (run-once)", atomic.LoadInt32(&called))
	}
}

func TestRegistry_Reset(t *testing.T) {
	r := NewRegistry()

	var called int32
	r.Add("test", 0, func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	r.Run(context.Background(), 5*time.Second)
	r.Reset()
	r.Run(context.Background(), 5*time.Second)

	if atomic.LoadInt32(&called) != 2 {
		t.Errorf("called = %d; want 2 (reset)", atomic.LoadInt32(&called))
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := NewRegistry()

	r.Add("a", 0, func(ctx context.Context) error { return nil })
	r.Add("b", 0, func(ctx context.Context) error { return nil })
	r.Add("a", 0, func(ctx context.Context) error { return nil }) // duplicate name

	if r.Len() != 3 {
		t.Errorf("Len = %d; want 3", r.Len())
	}

	removed := r.Remove("a")
	if removed != 2 {
		t.Errorf("Remove returned %d; want 2", removed)
	}
	if r.Len() != 1 {
		t.Errorf("Len after Remove = %d; want 1", r.Len())
	}
}

func TestRegistry_AddSimple(t *testing.T) {
	r := NewRegistry()

	var called int32
	r.AddSimple("simple", func() error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	r.Run(context.Background(), 5*time.Second)

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("called = %d; want 1", atomic.LoadInt32(&called))
	}
}

func TestRegistry_AddWithGroup(t *testing.T) {
	r := NewRegistry()

	r.AddWithGroup("a", "g1", 0, func(ctx context.Context) error { return nil })
	r.AddWithGroup("b", "g1", 0, func(ctx context.Context) error { return nil })
	r.AddWithGroup("c", "g2", 0, func(ctx context.Context) error { return nil })

	removed := r.RemoveGroup("g1")
	if removed != 2 {
		t.Errorf("RemoveGroup returned %d; want 2", removed)
	}
	if r.Len() != 1 {
		t.Errorf("Len after RemoveGroup = %d; want 1", r.Len())
	}
}

func TestRegistry_PanicRecovery(t *testing.T) {
	r := NewRegistry()

	r.Add("panic", 0, func(ctx context.Context) error {
		panic("oops")
	})

	errs := r.Run(context.Background(), 5*time.Second)

	// Should not crash, should report the panic as an error
	if len(errs) == 0 {
		t.Error("panic should be reported as error")
	}
}

func TestRegistry_ErrorHandler(t *testing.T) {
	r := NewRegistry()

	var captured int32
	r.SetErrorHandler(func(name string, err error) {
		atomic.AddInt32(&captured, 1)
	})

	r.Add("fail", 0, func(ctx context.Context) error {
		return errors.New("test error")
	})

	r.Run(context.Background(), 5*time.Second)

	if atomic.LoadInt32(&captured) != 1 {
		t.Errorf("error handler called %d times; want 1", atomic.LoadInt32(&captured))
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()

	r.Add("a", 100, func(ctx context.Context) error { return nil })
	r.Add("b", 50, func(ctx context.Context) error { return nil })

	list := r.List()
	if len(list) != 2 {
		t.Errorf("List len = %d; want 2", len(list))
	}
}

func TestDefault(t *testing.T) {
	r := Default()
	if r == nil {
		t.Error("Default should return non-nil")
	}
}
