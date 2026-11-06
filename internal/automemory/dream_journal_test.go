package automemory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
)

func TestDreamJournalDrainsLongConversationAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := conversation.NewStore(nil, nil, dir)
	sess, err := store.Get(ctx, "cli", "user")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 65; i++ {
		sess.AddMessage(conversation.RoleUser, fmt.Sprintf("preferencia duravel numero %03d", i))
		if err := store.Save(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	sess.AddMessage(conversation.RoleUser, "minha senha = do-not-extract")
	sess.AddMessage(conversation.RoleAssistant, "untrusted assistant statement")
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	var transcripts []string
	extract := func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
		transcripts = append(transcripts, transcript)
		return nil, nil
	}
	cfg := DreamConfig{SessionStore: store, Memory: &stubMemoryStore{}, ExtractFn: extract, MaxMessages: 10, StateFile: filepath.Join(dir, "dream-state.json"), Logger: quietLogger()}
	for i := 0; i < 9; i++ { // Restart both persistence layers between every batch.
		cfg.SessionStore = conversation.NewStore(nil, nil, dir)
		if err := NewDreamService(cfg).RunNow(ctx); err != nil {
			t.Fatal(err)
		}
	}
	all := strings.Join(transcripts, "")
	if strings.Contains(all, "do-not-extract") || strings.Contains(all, "untrusted assistant") {
		t.Fatal("unsafe evidence reached extraction")
	}
	for i := 0; i < 65; i++ {
		needle := fmt.Sprintf("preferencia duravel numero %03d", i)
		if strings.Count(all, needle) != 1 {
			t.Fatalf("missing or repeated: %s", needle)
		}
	}
	if len(transcripts) != 7 {
		t.Fatalf("unexpected extraction calls: %d", len(transcripts))
	}
}

func TestDreamDoesNotAdvanceCursorOnStateWriteFailure(t *testing.T) {
	store := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "prefiro respostas curtas"}})
	d := NewDreamService(DreamConfig{SessionStore: store, Memory: &stubMemoryStore{}, StateFile: t.TempDir(), Logger: quietLogger(), ExtractFn: func(context.Context, string) ([]ExtractedMemory, error) { return nil, nil }})
	if err := d.RunNow(context.Background()); err == nil {
		t.Fatal("expected state write failure")
	}
	if d.lastSequence != 0 {
		t.Fatal("cursor advanced despite failure")
	}
}

func TestDreamReplaysPersistedBatchAfterWriteFailure(t *testing.T) {
	ctx := context.Background()
	store := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "prefiro respostas curtas"}})
	mem := &stubMemoryStore{saveErr: errors.New("disk unavailable")}
	calls := 0
	cfg := DreamConfig{SessionStore: store, Memory: mem, StateFile: filepath.Join(t.TempDir(), "state.json"), Logger: quietLogger(), ExtractFn: func(context.Context, string) ([]ExtractedMemory, error) {
		calls++
		return []ExtractedMemory{{Type: "preference", Content: "prefere respostas curtas", Importance: 3}}, nil
	}}
	if err := NewDreamService(cfg).RunNow(ctx); err == nil {
		t.Fatal("expected memory failure")
	}
	sess, err := store.Get(ctx, "test-channel", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	sess.AddMessage(conversation.RoleUser, "outro turno depois da falha")
	if err := store.Save(ctx, sess); err != nil {
		t.Fatal(err)
	}
	mem.saveErr = nil
	if err := NewDreamService(cfg).RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("model called again for interrupted batch: %d", calls)
	}
	if len(mem.saved) != 1 || len(mem.saved[0].Tags) != 1 || !strings.HasPrefix(mem.saved[0].Tags[0], "journal:") {
		t.Fatal("missing provenance")
	}
}

func TestDreamFactKeyUpdatesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := conversation.NewStore(nil, nil, dir)
	sess, err := store.Get(ctx, "cli", "user")
	if err != nil {
		t.Fatal(err)
	}
	mem := memory.NewStoreFromDB(nil, t.TempDir())
	d := NewDreamService(DreamConfig{SessionStore: store, Memory: mem, Logger: quietLogger(), ExtractFn: func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
		content := "Moro em Recife"
		if strings.Contains(transcript, "Salvador") {
			content = "Moro em Salvador"
		}
		return []ExtractedMemory{{Type: "fact", FactKey: "usuario.cidade", Content: content, Importance: 3}}, nil
	}})
	for _, content := range []string{"Moro em Recife", "Agora moro em Salvador"} {
		sess.AddMessage(conversation.RoleUser, content)
		if err := store.Save(ctx, sess); err != nil {
			t.Fatal(err)
		}
		if err := d.RunNow(ctx); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := mem.List(ctx)
	if err != nil || len(facts) != 1 || facts[0].Content != "Moro em Salvador" {
		t.Fatalf("conflicting facts remained active: %v %v", facts, err)
	}
	if err := mem.Delete(ctx, facts[0].Topic); err != nil {
		t.Fatal(err)
	}
	// Reset consolidation state: private historical evidence must not resurrect
	// a fact the user explicitly removed from derived memory.
	d.lastSequence = 0
	if err := d.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	facts, err = mem.List(ctx)
	if err != nil || len(facts) != 0 {
		t.Fatalf("forgotten fact resurrected from raw journal: %v %v", facts, err)
	}
}
