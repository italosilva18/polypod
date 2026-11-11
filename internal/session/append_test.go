package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendMessagesKeepsEarlierPagesAndRevision(t *testing.T) {
	storage := NewFileSystemStorage(t.TempDir(), nil)
	initial := &RestoredSession{
		ID: "append-session", Title: "Historical project", Metadata: SessionMetadata{AgentName: "coder"},
		CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
	}
	for i := 0; i < 130; i++ {
		initial.Messages = append(initial.Messages, Message{ID: fmt.Sprintf("msg-%03d", i), Role: "user", Content: fmt.Sprintf("turn %d", i)})
	}
	if err := storage.Save(initial); err != nil {
		t.Fatal(err)
	}
	oldManifest, _, oldRevision, err := storage.readDocumentWithRevision(initial.ID)
	if err != nil || oldManifest == nil || len(oldManifest.Pages) != 3 {
		t.Fatalf("initial manifest: %+v, err=%v", oldManifest, err)
	}
	header, err := storage.LoadHeader(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	header.UpdatedAt = time.Now()
	updated, err := storage.AppendMessagesAtRevision(*header, []Message{{ID: "msg-130", Role: "assistant", Content: "final"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MessageCount != 131 || updated.Revision == oldRevision || updated.Title != initial.Title || updated.Metadata.AgentName != "coder" {
		t.Fatalf("append header changed metadata or count: %+v", updated)
	}
	newManifest, _, _, err := storage.readDocumentWithRevision(initial.ID)
	if err != nil || newManifest == nil {
		t.Fatalf("updated manifest: %+v, err=%v", newManifest, err)
	}
	if newManifest.Pages[0] != oldManifest.Pages[0] || newManifest.Pages[1] != oldManifest.Pages[1] ||
		newManifest.Pages[2] == oldManifest.Pages[2] {
		t.Fatalf("append rewrote earlier pages or failed to replace partial page: old=%+v new=%+v", oldManifest.Pages, newManifest.Pages)
	}
	if _, err := os.Stat(filepath.Join(storage.pageDir(initial.ID), oldManifest.Pages[2].File)); err != nil {
		t.Fatalf("old partial page unavailable to reader of previous manifest: %v", err)
	}
	all, err := storage.Load(initial.ID)
	if err != nil || len(all.Messages) != 131 || all.Messages[0].ID != "msg-000" || all.Messages[130].ID != "msg-130" {
		t.Fatalf("append lost transcript: count=%d err=%v", len(all.Messages), err)
	}
	if _, err := storage.AppendMessagesAtRevision(*header, []Message{{ID: "stale", Role: "user", Content: "stale"}}); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("stale append not rejected: %v", err)
	}
	if _, err := storage.LoadPageAtRevision(initial.ID, 0, 10, oldRevision); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("old page cursor mixed with new revision: %v", err)
	}
	page, err := storage.LoadPageAtRevision(initial.ID, 0, 10, updated.Revision)
	if err != nil || len(page.Messages) != 10 || page.Messages[9].ID != "msg-130" {
		t.Fatalf("newest page after append: %+v, err=%v", page, err)
	}
}

func TestAppendMessagesMigratesLegacySession(t *testing.T) {
	storage := NewFileSystemStorage(t.TempDir(), nil)
	legacy := []byte(`{"id":"legacy","title":"old","messages":[{"id":"one","role":"user","content":"old turn"}],"metadata":{"tag":"cli"}}`)
	if err := os.WriteFile(storage.sessionPath("legacy"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	header, err := storage.LoadHeader("legacy")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := storage.AppendMessagesAtRevision(*header, []Message{{ID: "two", Role: "assistant", Content: "new turn"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MessageCount != 2 || updated.Revision == header.Revision || updated.PrefixHash == "" {
		t.Fatalf("legacy append did not migrate: %+v", updated)
	}
	manifest, old, _, err := storage.readDocumentWithRevision("legacy")
	if err != nil || manifest == nil || old != nil || len(manifest.Pages) != 1 {
		t.Fatalf("legacy format not migrated safely: manifest=%+v legacy=%+v err=%v", manifest, old, err)
	}
	loaded, err := storage.Load("legacy")
	if err != nil || len(loaded.Messages) != 2 || loaded.Messages[0].ID != "one" || loaded.Messages[1].ID != "two" {
		t.Fatalf("legacy turns lost: %+v err=%v", loaded, err)
	}
}
