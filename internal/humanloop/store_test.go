package humanloop

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, maxHistory int) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "humanloop", "inbox.json")
	store, err := NewStore(path, maxHistory)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, path
}

func TestStorePersistsProtectedJournal(t *testing.T) {
	store, path := newTestStore(t, 20)
	created, err := store.EnqueueInbound("relay", "owner", "verifique os backups")
	if err != nil {
		t.Fatalf("EnqueueInbound: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("permissao = %o, esperava 600", got)
	}

	reloaded, err := NewStore(path, 20)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	_, messages := reloaded.Snapshot()
	if len(messages) != 1 || messages[0].ID != created.ID {
		t.Fatalf("mensagem nao persistida: %#v", messages)
	}
}

func TestStoreRecoversInterruptedProcessing(t *testing.T) {
	store, path := newTestStore(t, 20)
	if _, err := store.EnqueueInbound("relay", "owner", "continue"); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("ClaimNextDue: ok=%v err=%v", ok, err)
	}
	if claimed.Status != StatusProcessing || claimed.Attempts != 1 {
		t.Fatalf("claim inesperado: %#v", claimed)
	}

	reloaded, err := NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	_, messages := reloaded.Snapshot()
	if messages[0].Status != StatusPending {
		t.Fatalf("status recuperado = %s", messages[0].Status)
	}
	if messages[0].Error == "" {
		t.Fatal("recuperacao deveria registrar a interrupcao")
	}
	claimedAgain, ok, err := reloaded.ClaimNextDue(time.Now().Add(time.Second))
	if err != nil || !ok || claimedAgain.Attempts != 2 {
		t.Fatalf("segunda tentativa: %#v ok=%v err=%v", claimedAgain, ok, err)
	}
}

func TestStoreRetryBecomesTerminal(t *testing.T) {
	store, _ := newTestStore(t, 20)
	msg, err := store.EnqueueOutbound("relay", "owner", "falhou", "")
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimNextDue(time.Now().Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := store.Retry(claimed.ID, errors.New("offline"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	_, messages := store.Snapshot()
	if messages[0].ID != msg.ID || messages[0].Status != StatusFailed {
		t.Fatalf("estado terminal inesperado: %#v", messages)
	}
	if messages[0].CompletedAt == nil {
		t.Fatal("falha terminal sem CompletedAt")
	}
}

func TestStoreBoundsOnlyFinishedHistory(t *testing.T) {
	store, _ := newTestStore(t, 2)
	pending, err := store.EnqueueInbound("relay", "owner", "permanece")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		msg, err := store.EnqueueOutbound("relay", "owner", "historico", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Complete(msg.ID, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	_, messages := store.Snapshot()
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, esperava um ativo + dois finais", len(messages))
	}
	foundPending := false
	for _, msg := range messages {
		if msg.ID == pending.ID && msg.Status == StatusPending {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatal("trimming descartou trabalho ativo")
	}
}

func TestStorePersistsHeartbeat(t *testing.T) {
	store, path := newTestStore(t, 20)
	at := time.Now().Round(time.Millisecond)
	if err := store.RecordHeartbeat(at, errors.New("erro temporario")); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := reloaded.Snapshot()
	if !state.LastHeartbeat.Equal(at) || state.LastError != "erro temporario" {
		t.Fatalf("heartbeat nao persistido: %#v", state)
	}
}
