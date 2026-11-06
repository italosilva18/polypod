package humanloop

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDestinationAcknowledgementSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := store.EnqueueOutbound("notification", "owner", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDelivered(msg.ID, "telegram"); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !store.DeliveredTo(msg.ID, "telegram") || store.DeliveredTo(msg.ID, "local") {
		t.Fatal("destination acknowledgements not independent")
	}
}

func TestInboundExternalIDSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueInboundOnce("telegram", "42", "hello", "7")
	if err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueueInboundOnce("telegram", "42", "hello", "7")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatal("duplicate event enqueued")
	}
}

func TestOutboundDedupSurvivesHistoryTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := NewStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueOutboundOnce("scheduler", "owner", "done", "", "run_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(first.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueueOutbound("scheduler", "owner", "another", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(second.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.EnqueueOutboundOnce("scheduler", "owner", "done", "", "run_1")
	if err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if replay.ID != first.ID || len(messages) != 1 {
		t.Fatal("trimmed callback was enqueued again")
	}
}
