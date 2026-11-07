package humanloop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/costa/polypod/internal/skill"
)

func failedInboundForResolution(t *testing.T, store *Store) Message {
	t.Helper()
	msg, err := store.EnqueueInboundOnce("whatsapp", "private-user", "private-text", "transport-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextDue(time.Now()); err != nil || !ok {
		t.Fatalf("claim inbound: ok=%t err=%v", ok, err)
	}
	if err := store.Retry(msg.ID, errors.New("schema rejected"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestResolveFailedInboundConcurrentPersistsAuditWithoutReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "humanloop.json")
	store, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	original := failedInboundForResolution(t, store)
	preview, err := store.PreviewFailedResolution(original.ID)
	if err != nil || preview.Direction != DirectionInbound || preview.Channel != "whatsapp" || preview.Attempts != 1 || preview.FailedAt.IsZero() {
		t.Fatalf("preview = %+v, err=%v", preview, err)
	}
	const workers = 8
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- store.ResolveFailed(original.ID, "operator", "teste antigo obsoleto", time.Now())
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent resolutions succeeded %d times, want 1", success)
	}
	reopened, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reopened.Snapshot()
	if len(messages) != 1 {
		t.Fatalf("message count = %d", len(messages))
	}
	resolved := messages[0]
	if resolved.ID != original.ID || resolved.Status != StatusResolved || resolved.Attempts != 1 ||
		resolved.Error != "schema rejected" || resolved.Resolution == nil ||
		resolved.Resolution.Actor != "operator" || resolved.Resolution.Reason != "teste antigo obsoleto" ||
		resolved.CompletedAt == nil || resolved.Resolution.At.IsZero() {
		t.Fatalf("audit or identity lost: %+v", resolved)
	}
	if _, ok, err := reopened.ClaimNextDue(time.Now()); err != nil || ok {
		t.Fatalf("resolved item was claimed: ok=%t err=%v", ok, err)
	}
	duplicate, err := reopened.EnqueueInboundOnce("whatsapp", "private-user", "private-text", "transport-1")
	if err != nil || duplicate.ID != original.ID {
		t.Fatalf("transport dedup lost: id=%s err=%v", duplicate.ID, err)
	}
	// Snapshot callers cannot alter durable resolution metadata through aliases.
	resolved.Resolution.Reason = "tampered"
	_, again := reopened.Snapshot()
	if again[0].Resolution.Reason != "teste antigo obsoleto" {
		t.Fatal("snapshot exposed mutable resolution record")
	}
}

func TestResolveFailedOutboundPreservesPartialDeliveryAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "humanloop.json")
	store, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := store.EnqueueOutbound("scheduler", "owner", "old notice", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextDue(time.Now()); err != nil || !ok {
		t.Fatalf("claim outbound: ok=%t err=%v", ok, err)
	}
	if err := store.MarkDelivered(msg.ID, "local"); err != nil {
		t.Fatal(err)
	}
	if err := store.Retry(msg.ID, errors.New("remote timeout"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewFailedResolution(msg.ID)
	if err != nil || preview.Direction != DirectionOutbound || len(preview.Delivered) != 1 || preview.Delivered[0] != "local" {
		t.Fatalf("partial delivery missing from preview: %+v, %v", preview, err)
	}
	badPath := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(badPath, 0700); err != nil {
		t.Fatal(err)
	}
	store.path = badPath
	if err := store.ResolveFailed(msg.ID, "operator", "obsolete notice", time.Now()); err == nil {
		t.Fatal("expected journal write failure")
	}
	store.path = path
	if _, err := store.PreviewFailedResolution(msg.ID); err != nil {
		t.Fatalf("failed write did not roll back status: %v", err)
	}
	if err := store.ResolveFailed(msg.ID, "operator", "obsolete notice", time.Now()); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path, 500)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reopened.Snapshot()
	if len(messages) != 1 || messages[0].Status != StatusResolved || len(messages[0].Delivered) != 1 ||
		messages[0].Delivered[0] != "local" || messages[0].Error != "remote timeout" {
		t.Fatalf("partial delivery or failure lost: %+v", messages)
	}
}

func TestInboxResolveSkillPreviewThenApply(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "humanloop.json"), 500)
	if err != nil {
		t.Fatal(err)
	}
	msg := failedInboundForResolution(t, store)
	reg := skill.NewRegistry()
	RegisterSkills(reg, store, nil)
	preview, err := reg.Execute("inbox_resolve", `{"id":"`+msg.ID+`"}`)
	if err != nil || !strings.Contains(preview, "mode=apply") || strings.Contains(preview, "private-text") {
		t.Fatalf("unsafe preview: %q, %v", preview, err)
	}
	if _, err := store.PreviewFailedResolution(msg.ID); err != nil {
		t.Fatalf("preview mutated record: %v", err)
	}
	if result, err := reg.Execute("inbox_resolve", `{"id":"`+msg.ID+`","mode":"apply"}`); err != nil || !strings.Contains(result, "Erro:") {
		t.Fatalf("empty reason was accepted: %q, %v", result, err)
	}
	result, err := reg.Execute("inbox_resolve", `{"id":"`+msg.ID+`","mode":"apply","reason":"teste obsoleto"}`)
	if err != nil || !strings.Contains(result, "nenhuma acao foi repetida") {
		t.Fatalf("skill resolution failed: %q, %v", result, err)
	}
	_, messages := store.Snapshot()
	if messages[0].Status != StatusResolved || messages[0].Resolution == nil || messages[0].Resolution.Actor != "agent" {
		t.Fatalf("skill resolution not audited: %+v", messages[0])
	}
}
