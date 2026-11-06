package humanloop

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequeueFailedInboundPreservesIdentityAndAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "humanloop.json")
	store, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.EnqueueInboundOnce("whatsapp", "user-private", "message-private", "transport-1")
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextDue(time.Now())
	if err != nil || !ok || claimed.ID != original.ID {
		t.Fatalf("claim = %v, %v, %v", claimed.ID, ok, err)
	}
	if err := store.Retry(original.ID, errors.New("provider schema rejected"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	maintenance, err := OpenStoreForMaintenance(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := maintenance.PreviewFailedInbound(original.ID)
	if err != nil || preview.Channel != "whatsapp" || preview.Attempts != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if err := maintenance.RequeueFailedInbound(original.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.RequeueFailedInbound(original.ID, time.Now()); err == nil {
		t.Fatal("second replay should be rejected")
	}
	reopened, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reopened.Snapshot()
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	msg := messages[0]
	if msg.ID != original.ID || msg.ExternalID != original.ExternalID || msg.Status != StatusPending || msg.Attempts != 0 || msg.Error != "" || msg.CompletedAt != nil || msg.Text != original.Text || msg.UserID != original.UserID {
		t.Fatalf("requeued item did not preserve identity/state: status=%s attempts=%d", msg.Status, msg.Attempts)
	}
	if len(msg.ReplayHistory) != 1 || msg.ReplayHistory[0].Attempts != 1 || msg.ReplayHistory[0].Error != "provider schema rejected" || msg.ReplayHistory[0].FailedAt.IsZero() {
		t.Fatalf("history not preserved: %+v", msg.ReplayHistory)
	}
	duplicate, err := reopened.EnqueueInboundOnce("whatsapp", "user-private", "message-private", "transport-1")
	if err != nil || duplicate.ID != original.ID {
		t.Fatalf("dedup changed: id=%s err=%v", duplicate.ID, err)
	}
	claimed, ok, err = reopened.ClaimNextDue(time.Now())
	if err != nil || !ok || claimed.ID != original.ID || claimed.Attempts != 1 {
		t.Fatalf("replay claim = id:%s ok:%v attempts:%d err:%v", claimed.ID, ok, claimed.Attempts, err)
	}
	if err := reopened.CompleteInbound(original.ID, "ack", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CompleteInbound(original.ID, "ack", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, messages = reopened.Snapshot()
	if len(messages) != 2 || messages[1].Direction != DirectionOutbound || messages[1].ReplyTo != original.ID {
		t.Fatalf("response was not queued exactly once: %d messages", len(messages))
	}
}

func TestMaintenancePreviewDoesNotRecoverProcessing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "humanloop.json")
	store, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueInbound("whatsapp", "user", "text"); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextDue(time.Now())
	if err != nil || !ok {
		t.Fatalf("claim failed: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	maintenance, err := OpenStoreForMaintenance(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.PreviewFailedInbound(claimed.ID); err == nil {
		t.Fatal("processing item must not be replayable")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("preview mutated the store")
	}
}

func TestRequeueFailedInboundRejectsExistingOutboxAndRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "humanloop.json")
	store, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := store.EnqueueInbound("whatsapp", "user", "text")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ClaimNextDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(msg.ID, errors.New("failed"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	// A failed write must leave the in-memory record untouched.
	badPath := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(badPath, 0o700); err != nil {
		t.Fatal(err)
	}
	store.path = badPath
	if err := store.RequeueFailedInbound(msg.ID, time.Now()); err == nil {
		t.Fatal("expected write failure")
	}
	store.path = path
	preview, err := store.PreviewFailedInbound(msg.ID)
	if err != nil || preview.Attempts != 1 || preview.PriorReplays != 0 {
		t.Fatalf("failed write changed item: %+v, %v", preview, err)
	}
	item, err := store.findLocked(msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	item.ResponseReady = true
	if err := store.RequeueFailedInbound(msg.ID, time.Now()); err == nil {
		t.Fatal("inbound with a prepared response must block replay")
	}
	item.ResponseReady = false
	if _, err := store.EnqueueOutbound("whatsapp", "user", "response", msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RequeueFailedInbound(msg.ID, time.Now()); err == nil {
		t.Fatal("ambiguous existing outbox must block replay")
	}
}
