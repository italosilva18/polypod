package humanloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitFailureRetriesResponseWithoutReexecutingAgent(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "same process", true: "restart after recovery checkpoint"}[restart], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inbox.json")
			store, err := NewStore(path, 20)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := store.EnqueueInbound("telegram", "42", "work")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			cfg := ProcessorConfig{Store: store, Logger: testLogger(), Process: func(context.Context, Message) (string, error) {
				calls++
				// Fail the atomic temp-file write after the agent has finished its work.
				if err := os.Mkdir(path+".tmp", 0700); err != nil {
					t.Fatal(err)
				}
				return "preserved response", nil
			}, Deliver: func(context.Context, Message) error { return nil }}
			processor, err := NewProcessor(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := processor.ProcessNow(context.Background()); err == nil {
				t.Fatal("expected commit failure")
			}
			_, messages := store.Snapshot()
			if len(messages) != 1 || messages[0].Status != StatusPending || !messages[0].ResponseReady || messages[0].Response != "preserved response" {
				t.Fatalf("response not retained: %+v", messages)
			}
			// A failed retry claim must not strand the pending result in processing.
			if _, _, err := store.ClaimNextDue(time.Now().Add(time.Minute)); err == nil {
				t.Fatal("expected claim persistence failure")
			}
			_, messages = store.Snapshot()
			if messages[0].Status != StatusPending {
				t.Fatal("failed claim stranded response")
			}
			if err := os.Remove(path + ".tmp"); err != nil {
				t.Fatal(err)
			}
			claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Minute))
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			if restart {
				store, err = NewStore(path, 20)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Store = store
				processor, err = NewProcessor(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := processor.ProcessNow(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else if err := processor.handle(context.Background(), claimed); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("agent executed %d times", calls)
			}
			_, messages = store.Snapshot()
			outbound := 0
			for _, m := range messages {
				if m.ID == msg.ID && m.Status != StatusCompleted {
					t.Fatal(m)
				}
				if m.Direction == DirectionOutbound {
					outbound++
					if m.Text != "preserved response" {
						t.Fatal(m)
					}
				}
			}
			if outbound != 1 {
				t.Fatalf("outbox count: %d", outbound)
			}
		})
	}
}
