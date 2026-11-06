package humanloop

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestQuestionOutboundIsDeferredAndIdempotent(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "inbox.json"), 50)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour)
	first, err := store.EnqueueQuestionOutboundOnce("whatsapp", "chat", "pergunta", "ask:q_one", due)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextDue(time.Now()); err != nil || ok {
		t.Fatalf("deferred question was claimed: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.ClaimNextDue(due.Add(2 * time.Hour)); err != nil || ok {
		t.Fatalf("expired question was claimed by generic worker: ok=%v err=%v", ok, err)
	}
	replayed, err := store.EnqueueQuestionOutboundOnce("whatsapp", "chat", "pergunta", "ask:q_one", due)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("duplicate question changed identity: %#v, %v", replayed, err)
	}
	if err := store.Retry(first.ID, errors.New("expired"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.EnqueueQuestionOutboundOnce("whatsapp", "chat", "pergunta", "ask:q_one", due)
	if err != nil || refreshed.ID != first.ID || refreshed.Status != StatusPending {
		t.Fatalf("question retry did not reuse failed outbox: %#v, %v", refreshed, err)
	}
	if _, err := store.EnqueueQuestionOutboundOnce("whatsapp", "other-chat", "pergunta", "ask:q_one", due); err == nil {
		t.Fatal("stable question ID accepted a different destination")
	}
}
