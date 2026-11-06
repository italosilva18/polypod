package humanloop

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestDedupTombstonesAreBoundedAfterHistoryTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.json")
	store, err := NewStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	var recent Message
	for i := 0; i < 150; i++ {
		external := fmt.Sprintf("run-%03d", i)
		msg, err := store.EnqueueOutboundOnce("scheduler", "owner", "done", "", external)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Complete(msg.ID, "", time.Now()); err != nil {
			t.Fatal(err)
		}
		recent = msg
	}
	if len(store.dedup) > 128 || len(store.dedupOrder) != len(store.dedup) {
		t.Fatalf("dedup state unbounded: keys=%d order=%d", len(store.dedup), len(store.dedupOrder))
	}
	store, err = NewStore(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.EnqueueOutboundOnce("scheduler", "owner", "done", "", "run-149")
	if err != nil || duplicate.ID != recent.ID {
		t.Fatalf("recent duplicate not retained: %+v, %v", duplicate, err)
	}
	old, err := store.EnqueueOutboundOnce("scheduler", "owner", "done", "", "run-000")
	if err != nil {
		t.Fatal(err)
	}
	if old.ID == recent.ID || old.Status != StatusPending {
		t.Fatalf("expired tombstone was not reaccepted: %+v", old)
	}
}
