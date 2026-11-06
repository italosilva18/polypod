package humanloop

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestProcessorRunsAgentAndDeliversResponse(t *testing.T) {
	store, _ := newTestStore(t, 20)
	inbound, err := store.EnqueueInbound("remote", "owner", "qual e o status?")
	if err != nil {
		t.Fatal(err)
	}
	var delivered Message
	processor, err := NewProcessor(ProcessorConfig{
		Store:       store,
		Logger:      testLogger(),
		MaxAttempts: 2,
		Process: func(_ context.Context, msg Message) (string, error) {
			if msg.ID != inbound.ID {
				t.Fatalf("inbound ID = %s", msg.ID)
			}
			return "todos os servicos estao ativos", nil
		},
		Deliver: func(_ context.Context, msg Message) error {
			delivered = msg
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.ProcessNow(context.Background()); err != nil {
		t.Fatalf("ProcessNow: %v", err)
	}
	if delivered.Text != "todos os servicos estao ativos" || delivered.ReplyTo != inbound.ID {
		t.Fatalf("entrega inesperada: %#v", delivered)
	}
	_, messages := store.Snapshot()
	if len(messages) != 2 {
		t.Fatalf("mensagens = %d", len(messages))
	}
	for _, msg := range messages {
		if msg.Status != StatusCompleted {
			t.Fatalf("mensagem %s ficou em %s", msg.ID, msg.Status)
		}
	}
}

func TestProcessorRetriesThenSucceeds(t *testing.T) {
	store, _ := newTestStore(t, 20)
	if _, err := store.EnqueueInbound("remote", "owner", "tente novamente"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	processor, err := NewProcessor(ProcessorConfig{
		Store:        store,
		Logger:       testLogger(),
		MaxAttempts:  3,
		RetryDelays:  []time.Duration{0},
		MaxCycleWork: 10,
		Process: func(context.Context, Message) (string, error) {
			attempts++
			if attempts == 1 {
				return "", errors.New("provider indisponivel")
			}
			return "recuperado", nil
		},
		Deliver: func(context.Context, Message) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.ProcessNow(context.Background()); err == nil {
		t.Fatal("o ciclo deve reportar que houve uma tentativa com erro")
	}
	_, messages := store.Snapshot()
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
	if messages[0].Status != StatusCompleted || messages[0].Attempts != 2 {
		t.Fatalf("inbound inesperado: %#v", messages[0])
	}
}

func TestProcessorStopsAtMaxAttempts(t *testing.T) {
	store, _ := newTestStore(t, 20)
	if _, err := store.EnqueueOutbound("remote", "owner", "notifique", ""); err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(ProcessorConfig{
		Store:        store,
		Logger:       testLogger(),
		MaxAttempts:  3,
		RetryDelays:  []time.Duration{0},
		MaxCycleWork: 10,
		Deliver: func(context.Context, Message) error {
			return errors.New("canal offline")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := processor.ProcessNow(context.Background()); err == nil {
		t.Fatal("esperava erro de entrega")
	}
	_, messages := store.Snapshot()
	if messages[0].Status != StatusFailed || messages[0].Attempts != 3 {
		t.Fatalf("estado final inesperado: %#v", messages[0])
	}
}

func TestHeartbeatStartWakeAndStop(t *testing.T) {
	store, _ := newTestStore(t, 20)
	var mu sync.Mutex
	processed := 0
	processor, err := NewProcessor(ProcessorConfig{
		Store:       store,
		Logger:      testLogger(),
		Interval:    time.Hour,
		MaxAttempts: 1,
		Process: func(context.Context, Message) (string, error) {
			mu.Lock()
			processed++
			mu.Unlock()
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor.Start(ctx)
	defer processor.Stop()

	if _, err := store.EnqueueInbound("remote", "owner", "acorde"); err != nil {
		t.Fatal(err)
	}
	processor.Wake()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := processed
		mu.Unlock()
		state, _ := store.Snapshot()
		if count == 1 && !state.LastHeartbeat.IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	count := processed
	mu.Unlock()
	state, _ := store.Snapshot()
	t.Fatalf("Wake/heartbeat nao concluiu: processadas=%d heartbeat=%v", count, state.LastHeartbeat)
}
