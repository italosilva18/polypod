package cli

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/session"
)

type countedPagedStorage struct {
	*session.FileSystemStorage
	loads int
	saves int
}

func (s *countedPagedStorage) Load(id string) (*session.RestoredSession, error) {
	s.loads++
	return s.FileSystemStorage.Load(id)
}

func (s *countedPagedStorage) Save(restored *session.RestoredSession) error {
	s.saves++
	return s.FileSystemStorage.Save(restored)
}

func TestSaveActiveSessionAppendsOnlyWhenPersistedPrefixMatches(t *testing.T) {
	fs := session.NewFileSystemStorage(t.TempDir(), nil)
	initial := &session.RestoredSession{
		ID: "paged-cli", CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now(),
		Metadata: session.SessionMetadata{Tag: "cli", AgentName: "coder"},
	}
	conv := conversation.NewSession("cli:local", "cli", "local")
	for i := 0; i < 130; i++ {
		message := session.Message{ID: fmt.Sprintf("turn-%03d", i), Role: "user", Content: fmt.Sprintf("pedido %d", i)}
		initial.Messages = append(initial.Messages, message)
		conv.Messages = append(conv.Messages, conversation.Message{ID: message.ID, Role: conversation.RoleUser, Content: message.Content})
	}
	initial.Title = session.TitleFromFirstMessage(initial.Messages)
	if err := fs.Save(initial); err != nil {
		t.Fatal(err)
	}
	storage := &countedPagedStorage{FileSystemStorage: fs}
	adapter := &Adapter{sessStorage: storage, activeSessionID: initial.ID, activeSessionRevision: initial.Revision}
	conv.Messages = append(conv.Messages, conversation.Message{ID: "new-turn", Role: conversation.RoleAssistant, Content: "resposta"})
	if err := adapter.saveActiveSession(conv); err != nil {
		t.Fatal(err)
	}
	if storage.loads != 0 || storage.saves != 0 {
		t.Fatalf("append loaded or rewrote complete snapshot: loads=%d saves=%d", storage.loads, storage.saves)
	}
	header, err := fs.LoadHeader(initial.ID)
	if err != nil || header.MessageCount != 131 || header.Revision != adapter.activeSessionRevision {
		t.Fatalf("append manifest mismatch: %+v err=%v", header, err)
	}

	// Automatic compaction or rewind can replace an older message while the
	// last turn stays the same. The digest must force the full Save path.
	conv.Messages[10].Content = "pedido reescrito"
	conv.Messages = append(conv.Messages, conversation.Message{ID: "after-rewrite", Role: conversation.RoleUser, Content: "novo"})
	if err := adapter.saveActiveSession(conv); err != nil {
		t.Fatal(err)
	}
	if storage.loads != 1 || storage.saves != 1 {
		t.Fatalf("changed prefix was appended: loads=%d saves=%d", storage.loads, storage.saves)
	}
	loaded, err := fs.Load(initial.ID)
	if err != nil || len(loaded.Messages) != 132 || loaded.Messages[10].Content != "pedido reescrito" {
		t.Fatalf("full replacement lost changed history: %+v err=%v", loaded, err)
	}

	// An external writer advances the manifest revision. The adapter must
	// refuse to overwrite it even if its in-memory prefix still matches.
	staleRevision := adapter.activeSessionRevision
	other, err := fs.Load(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	other.Messages = append(other.Messages, session.Message{ID: "external", Role: "assistant", Content: "externo"})
	if err := fs.Save(other); err != nil {
		t.Fatal(err)
	}
	conv.Messages = append(conv.Messages, conversation.Message{ID: "local", Role: conversation.RoleUser, Content: "local"})
	if err := adapter.saveActiveSession(conv); !errors.Is(err, session.ErrSessionConflict) {
		t.Fatalf("stale revision %s accepted after external save: %v", staleRevision, err)
	}
}
