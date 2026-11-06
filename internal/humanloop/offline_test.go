package humanloop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineRelayDoesNotExhaustAttemptsAndWakesOnReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := store.EnqueueOutboundOnce("whatsapp", "owner", "reply", "", "reply-1")
	if err != nil {
		t.Fatal(err)
	}
	connected := false
	p, err := NewProcessor(ProcessorConfig{Store: store, MaxAttempts: 2, Deliver: func(context.Context, Message) error {
		if !connected {
			return fmt.Errorf("socket: %w", ErrRemoteUnavailable)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := p.ProcessNow(context.Background()); !errors.Is(err, ErrRemoteUnavailable) {
			t.Fatalf("defer %d: got %v", i, err)
		}
		store, err = NewStore(path, 10)
		if err != nil {
			t.Fatal(err)
		}
		_, messages := store.Snapshot()
		if len(messages) != 1 || messages[0].Status != StatusPending || messages[0].Attempts != 0 || messages[0].OfflineDefers != i+1 {
			t.Fatalf("defer %d lost work: %+v", i, messages)
		}
		if err := store.ExpediteDeferredOutbounds("whatsapp", time.Now()); err != nil {
			t.Fatal(err)
		}
		p.cfg.Store = store
	}
	connected = true
	if err := p.ProcessNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if len(messages) != 1 || messages[0].ID != msg.ID || messages[0].Status != StatusCompleted || messages[0].Attempts != 1 {
		t.Fatalf("reconnect did not deliver original item: %+v", messages)
	}
	replayed, err := store.EnqueueOutboundOnce("whatsapp", "owner", "reply", "", "reply-1")
	if err != nil || replayed.ID != msg.ID {
		t.Fatalf("idempotence lost after offline: %+v, %v", replayed, err)
	}
}

func TestInboundRemoteQuestionOfflineDoesNotExhaustAttempts(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "inbox.json"), 20)
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := store.EnqueueInbound("whatsapp", "owner", "continue")
	if err != nil {
		t.Fatal(err)
	}
	connected := false
	p, err := NewProcessor(ProcessorConfig{Store: store, MaxAttempts: 2,
		Process: func(context.Context, Message) (string, error) {
			if !connected {
				return "", fmt.Errorf("ask_user nao entregue: %w", ErrRemoteUnavailable)
			}
			return "resposta final", nil
		},
		Deliver: func(context.Context, Message) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Hour))
		if err != nil || !ok || claimed.ID != inbound.ID {
			t.Fatalf("claim %d: ok=%v err=%v", i, ok, err)
		}
		if err := p.handle(context.Background(), claimed); !errors.Is(err, ErrRemoteUnavailable) {
			t.Fatalf("defer %d: %v", i, err)
		}
		_, messages := store.Snapshot()
		if messages[0].Status != StatusPending || messages[0].Attempts != 0 || messages[0].OfflineDefers != i+1 {
			t.Fatalf("inbound defer %d consumed an attempt: %+v", i, messages[0])
		}
		if len(messages) != 1 {
			t.Fatalf("deferred inbound produced a reply: %d messages", len(messages))
		}
	}
	connected = true
	claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("reconnect claim: ok=%v err=%v", ok, err)
	}
	if err := p.handle(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if messages[0].Status != StatusCompleted || messages[0].Attempts != 1 || messages[0].Response != "resposta final" || len(messages) != 2 {
		t.Fatalf("inbound did not complete after reconnect: %+v", messages[0])
	}
}

func TestPermanentDeliveryFailureStillReachesTerminalStatus(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if _, err := store.EnqueueOutbound("whatsapp", "owner", "reply", ""); err != nil {
		t.Fatal(err)
	}
	p, err := NewProcessor(ProcessorConfig{Store: store, MaxAttempts: 2, Deliver: func(context.Context, Message) error {
		return errors.New("permanent failure")
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Hour))
		if err != nil || !ok {
			t.Fatalf("claim %d: ok=%v err=%v", i, ok, err)
		}
		_ = p.handle(context.Background(), claimed)
	}
	_, messages := store.Snapshot()
	if messages[0].Status != StatusFailed || messages[0].Attempts != 2 {
		t.Fatalf("permanent error must exhaust attempts: %+v", messages[0])
	}
}

func TestReconnectWakeOnlyExpeditesMatchingDeferredChannel(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "inbox.json"), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"whatsapp", "telegram"} {
		if _, err := store.EnqueueOutbound(channel, "owner", "hello", ""); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for range 2 {
		msg, ok, err := store.ClaimNextDue(now)
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		if err := store.DeferUnavailable(msg.ID, ErrRemoteUnavailable, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ExpediteDeferredOutbounds("whatsapp", now); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	for _, msg := range messages {
		if msg.Channel == "whatsapp" && msg.NextAttemptAt.After(now) {
			t.Fatal("WhatsApp was not expedited")
		}
		if msg.Channel == "telegram" && !msg.NextAttemptAt.After(now) {
			t.Fatal("another relay was expedited")
		}
	}
}
