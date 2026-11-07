package automemory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
)

func TestEpisodesPairAcrossRestartWithoutDuplicating(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	journal := conversation.NewStore(nil, nil, "")
	memories := memory.NewFileStore(dir)
	stateFile := filepath.Join(dir, "episode-state.json")
	session, err := journal.Get(ctx, "whatsapp", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	session.AddMessage(conversation.RoleUser, "Implemente a rotina diária de resumo dos documentos do projeto")
	if err := journal.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	config := EpisodeConfig{SessionStore: journal, Memory: memories, StateFile: stateFile, MaxMessages: 1, Logger: quietLogger()}
	if err := NewEpisodeService(config).RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := memories.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("open request became a completed episode: %d memories", len(before))
	}
	session.AddMessage(conversation.RoleAssistant, "Concluído. Também encontrei um token secreto.")
	if err := journal.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	restarted := NewEpisodeService(config)
	if err := restarted.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if err := NewEpisodeService(config).RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := memories.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one durable episode, got %d", len(got))
	}
	if got[0].Source != "dream_episode" || got[0].Confidence >= 0.5 ||
		!strings.Contains(got[0].Content, "não fato verificado") ||
		!strings.Contains(got[0].Content, "não foram confirmados") ||
		strings.Contains(got[0].Content, "Concluído") || strings.Contains(got[0].Content, "token secreto") {
		t.Fatalf("episode provenance or uncertainty lost: %#v", got[0])
	}
	if len(got[0].Tags) != 4 || !strings.HasPrefix(got[0].Tags[2], "journal:") || !strings.HasPrefix(got[0].Tags[3], "journal:") {
		t.Fatalf("episode lacks exact journal provenance: %#v", got[0].Tags)
	}
}

func TestEpisodesSkipSecretsLinksTestsAndSyntheticChannels(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	journal := conversation.NewStore(nil, nil, "")
	memories := memory.NewFileStore(dir)
	session, err := journal.Get(ctx, "whatsapp", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{
		{"Configure a conta com senha: Abc123!", "Ok"},
		{"Implemente a integração descrita em https://example.com/segredo", "Ok"},
		{"Faça teste Polypod 2", "Ok"},
		{"Corrija a importação de documentos e registre o resultado no histórico", "Investiguei"},
	}
	for _, pair := range pairs {
		session.AddMessage(conversation.RoleUser, pair[0])
		session.AddMessage(conversation.RoleAssistant, pair[1])
	}
	if err := journal.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	synthetic, err := journal.Get(ctx, "scheduler", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	synthetic.AddMessage(conversation.RoleUser, "Implemente uma rotina automática para cada minuto")
	synthetic.AddMessage(conversation.RoleAssistant, "Ok")
	if err := journal.Save(ctx, synthetic); err != nil {
		t.Fatal(err)
	}
	service := NewEpisodeService(EpisodeConfig{SessionStore: journal, Memory: memories, StateFile: filepath.Join(dir, "episode-state.json"), MaxMessages: 2, Logger: quietLogger()})
	if err := service.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := memories.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Content, "importação de documentos") {
		t.Fatalf("unexpected episode selection: %#v", got)
	}
}

func TestEpisodesRetryUnacknowledgedBatch(t *testing.T) {
	ctx := context.Background()
	journal := conversation.NewStore(nil, nil, "")
	session, err := journal.Get(ctx, "whatsapp", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	session.AddMessage(conversation.RoleUser, "Configure uma rotina para consolidar os relatórios semanalmente")
	session.AddMessage(conversation.RoleAssistant, "Vou configurar a rotina")
	if err := journal.Save(ctx, session); err != nil {
		t.Fatal(err)
	}
	memories := &stubMemoryStore{saveErr: errors.New("storage unavailable")}
	cfg := EpisodeConfig{SessionStore: journal, Memory: memories, StateFile: filepath.Join(t.TempDir(), "episode-state.json"), Logger: quietLogger()}
	if err := NewEpisodeService(cfg).RunNow(ctx); err == nil {
		t.Fatal("expected failed memory save")
	}
	memories.mu.Lock()
	memories.saveErr = nil
	memories.mu.Unlock()
	if err := NewEpisodeService(cfg).RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if len(memories.snapshot()) != 1 {
		t.Fatalf("expected the failed batch to be replayed once, got %d saves", len(memories.snapshot()))
	}
}
