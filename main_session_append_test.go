package main

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

type countedShutdownStorage struct {
	*session.FileSystemStorage
	loads int
	saves int
}

func (s *countedShutdownStorage) Load(id string) (*session.RestoredSession, error) {
	s.loads++
	return s.FileSystemStorage.Load(id)
}

func (s *countedShutdownStorage) Save(snapshot *session.RestoredSession) error {
	s.saves++
	return s.FileSystemStorage.Save(snapshot)
}

func TestAutoSaveSessionAppendsWithoutLoadingOlderPages(t *testing.T) {
	dir := t.TempDir()
	fs := session.NewFileSystemStorage(dir, nil)
	initial := &session.RestoredSession{
		ID: "shutdown-session", CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
		Metadata: session.SessionMetadata{Tag: "cli", ProjectRoot: dir},
	}
	manager := conversation.NewManager(conversation.NewStore(nil, nil, ""), nil)
	active, err := manager.GetSession(t.Context(), "cli", "local")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 130; i++ {
		message := session.Message{ID: fmt.Sprintf("turn-%03d", i), Role: "user", Content: fmt.Sprintf("pedido %d", i)}
		initial.Messages = append(initial.Messages, message)
		active.Messages = append(active.Messages, conversation.Message{ID: message.ID, Role: conversation.RoleUser, Content: message.Content})
	}
	initial.Title = session.TitleFromFirstMessage(initial.Messages)
	if err := fs.Save(initial); err != nil {
		t.Fatal(err)
	}
	active.Messages = append(active.Messages, conversation.Message{ID: "new-answer", Role: conversation.RoleAssistant, Content: "feito"})
	storage := &countedShutdownStorage{FileSystemStorage: fs}
	autoSaveSession(manager, storage, session.NewManager(t.TempDir(), nil), initial.ID, initial.Revision, "default", "interactive", slog.Default())
	if storage.loads != 0 || storage.saves != 0 {
		t.Fatalf("shutdown loaded or rewrote complete session: loads=%d saves=%d", storage.loads, storage.saves)
	}
	loaded, err := fs.Load(initial.ID)
	if err != nil || loaded == nil || len(loaded.Messages) != 131 || loaded.Messages[130].ID != "new-answer" {
		t.Fatalf("shutdown append lost messages: %+v err=%v", loaded, err)
	}
}

func TestShutdownAppendRejectsStaleRevision(t *testing.T) {
	fs := session.NewFileSystemStorage(t.TempDir(), nil)
	initial := &session.RestoredSession{
		ID: "stale-shutdown", Messages: []session.Message{{ID: "first", Role: "user", Content: "one"}},
	}
	if err := fs.Save(initial); err != nil {
		t.Fatal(err)
	}
	header, err := fs.LoadHeader(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &session.RestoredSession{
		ID: initial.ID, Revision: header.Revision,
		Messages: []session.Message{{ID: "first", Role: "user", Content: "one"}, {ID: "local", Role: "assistant", Content: "two"}},
	}
	other, err := fs.Load(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	other.Messages = append(other.Messages, session.Message{ID: "external", Role: "assistant", Content: "external"})
	if err := fs.Save(other); err != nil {
		t.Fatal(err)
	}
	if err := saveShutdownSessionSnapshot(fs, fs, header, snapshot); !errors.Is(err, session.ErrSessionConflict) {
		t.Fatalf("stale shutdown snapshot overwrote another process: %v", err)
	}
	loaded, err := fs.Load(initial.ID)
	if err != nil || len(loaded.Messages) != 2 || loaded.Messages[1].ID != "external" {
		t.Fatalf("external transcript changed: %+v err=%v", loaded, err)
	}
}

func TestShutdownAppendFallsBackForReplacedHistory(t *testing.T) {
	fs := session.NewFileSystemStorage(t.TempDir(), nil)
	initial := &session.RestoredSession{ID: "rewritten-shutdown", Messages: []session.Message{
		{ID: "old", Role: "user", Content: "original"},
		{ID: "kept", Role: "assistant", Content: "kept"},
	}}
	if err := fs.Save(initial); err != nil {
		t.Fatal(err)
	}
	header, err := fs.LoadHeader(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	storage := &countedShutdownStorage{FileSystemStorage: fs}
	snapshot := &session.RestoredSession{ID: initial.ID, Revision: header.Revision, Messages: []session.Message{
		{ID: "summary", Role: "system", Content: "compacted"},
		{ID: "kept", Role: "assistant", Content: "kept"},
		{ID: "new", Role: "user", Content: "after compact"},
	}}
	if err := saveShutdownSessionSnapshot(storage, storage, header, snapshot); err != nil {
		t.Fatal(err)
	}
	if storage.saves != 1 {
		t.Fatalf("replaced history was appended instead of saved in full: saves=%d", storage.saves)
	}
	loaded, err := fs.Load(initial.ID)
	if err != nil || len(loaded.Messages) != 3 || loaded.Messages[0].ID != "summary" {
		t.Fatalf("replacement was lost: %+v err=%v", loaded, err)
	}
}
