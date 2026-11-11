package automemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/memory"
	_ "modernc.org/sqlite"
)

func TestDreamPersistsExtractedFactAndMetricsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memories.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic TEXT NOT NULL UNIQUE,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		tags TEXT NOT NULL DEFAULT '[]',
		priority INTEGER NOT NULL DEFAULT 0,
		session_id TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		confidence REAL NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(dir, "dream-state.json")
	sessions := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "Prefiro respostas em português."}})
	extractCalls := 0
	extract := func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
		extractCalls++
		if !strings.Contains(transcript, "Prefiro respostas em português") {
			t.Fatalf("user fact missing from transcript: %q", transcript)
		}
		return []ExtractedMemory{{Type: "preference", FactKey: "usuario.idioma",
			Content: "Prefere respostas em português.", Tags: []string{"idioma"}, Importance: 4,
			Evidence: dreamEvidenceForQuote(t, transcript, "Prefiro respostas em português.")}}, nil
	}
	d := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions,
		Memory: memory.NewSQLiteStore(db), ExtractFn: extract, StateFile: stateFile,
	})
	if err := d.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	stats := d.Stats()
	if stats.Runs != 1 || stats.SuccessfulRuns != 1 || stats.MemoriesExtracted != 1 || stats.MemoriesAdded != 1 || stats.MessagesProcessed != 1 || stats.LastError != "" || stats.LastSuccessfulRunAt.IsZero() {
		t.Fatalf("unexpected first-run metrics: %+v", stats)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	memStore := memory.NewSQLiteStore(db)
	fact, err := memStore.Get(ctx, d.topicForScope(ExtractedMemory{FactKey: "usuario.idioma"}, "test-channel:user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if fact.Content != "Prefere respostas em português." || fact.Source != "dream" {
		t.Fatalf("fact was not recovered from SQLite: %+v", fact)
	}
	if raw, err := os.ReadFile(stateFile); err != nil {
		t.Fatal(err)
	} else {
		var state dreamState
		if err := json.Unmarshal(raw, &state); err != nil || state.Stats.MemoriesAdded != 1 || state.LastSequence == 0 {
			t.Fatalf("durable metrics/cursor missing: state=%+v err=%v", state, err)
		}
	}

	restarted := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions,
		Memory: memStore, ExtractFn: extract, StateFile: stateFile,
	})
	if restarted.Stats().MemoriesAdded != 1 || restarted.Stats().Runs != 1 {
		t.Fatalf("metrics not restored: %+v", restarted.Stats())
	}
	if err := restarted.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if extractCalls != 1 || restarted.Stats().MemoriesAdded != 1 || restarted.Stats().Runs != 2 {
		t.Fatalf("restart re-extracted an acknowledged fact: calls=%d stats=%+v", extractCalls, restarted.Stats())
	}
}

func TestDreamPersistsFailureAndEmptyExtractionMetrics(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sessions := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "Prefiro respostas em português."}})
	stateFile := filepath.Join(dir, "dream-state.json")
	d := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions, Memory: &stubMemoryStore{},
		StateFile: stateFile,
		ExtractFn: func(context.Context, string) ([]ExtractedMemory, error) {
			return nil, errors.New("model timeout")
		},
	})
	if err := d.RunNow(ctx); err == nil {
		t.Fatal("expected extraction error")
	}
	restarted := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions, Memory: &stubMemoryStore{},
		StateFile: stateFile,
		ExtractFn: func(context.Context, string) ([]ExtractedMemory, error) {
			return nil, nil
		},
	})
	if stats := restarted.Stats(); stats.Runs != 1 || stats.FailedRuns != 1 || !strings.Contains(stats.LastError, "model timeout") {
		t.Fatalf("failure metrics not restored: %+v", stats)
	}
	if err := restarted.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := restarted.Stats(); stats.Runs != 2 || stats.SuccessfulRuns != 1 || stats.FailedRuns != 1 ||
		stats.EmptyExtractions != 1 || stats.LastBatchExtracted != 0 || stats.LastError != "" {
		t.Fatalf("empty extraction not distinguished from failure: %+v", stats)
	}
	if restarted.lastSequence == 0 {
		t.Fatal("a legitimate empty extraction did not advance the cursor")
	}
}

func TestDreamInvalidEvidencePreservesCursorForRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence func(*testing.T, string) []MemoryEvidence
	}{
		{name: "missing", evidence: func(*testing.T, string) []MemoryEvidence { return nil }},
		{name: "incorrect_quote", evidence: func(t *testing.T, transcript string) []MemoryEvidence {
			items := dreamEvidenceForQuote(t, transcript, "Prefiro respostas curtas.")
			items[0].Quote = "Esta frase nao aparece na origem."
			return items
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			stateFile := filepath.Join(t.TempDir(), "dream-state.json")
			sessions := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "Prefiro respostas curtas."}})
			memories := &stubMemoryStore{}
			valid := false
			d := NewDreamService(DreamConfig{
				Logger: quietLogger(), SessionStore: sessions, Memory: memories, StateFile: stateFile,
				ExtractFn: func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
					evidence := tc.evidence(t, transcript)
					if valid {
						evidence = dreamEvidenceForQuote(t, transcript, "Prefiro respostas curtas.")
					}
					return []ExtractedMemory{{Type: "preference", FactKey: "usuario.estilo_resposta",
						Content: "Prefere respostas curtas.", Importance: 3, Evidence: evidence}}, nil
				},
			})
			if err := d.RunNow(ctx); err == nil {
				t.Fatal("invalid extraction was acknowledged")
			}
			if stats := d.Stats(); stats.FailedRuns != 1 || stats.SuccessfulRuns != 0 || stats.MessagesProcessed != 0 || d.lastSequence != 0 {
				t.Fatalf("invalid extraction changed cursor or committed metrics: %+v cursor=%d", stats, d.lastSequence)
			}
			if _, err := os.Stat(stateFile + ".pending"); !os.IsNotExist(err) {
				t.Fatalf("invalid extraction was cached: %v", err)
			}
			valid = true
			if err := d.RunNow(ctx); err != nil {
				t.Fatal(err)
			}
			if stats := d.Stats(); stats.Runs != 2 || stats.SuccessfulRuns != 1 || stats.FailedRuns != 1 ||
				stats.MessagesProcessed != 1 || stats.MemoriesAdded != 1 || d.lastSequence == 0 {
				t.Fatalf("retry did not commit fact and cursor: %+v cursor=%d", stats, d.lastSequence)
			}
			if len(memories.snapshot()) != 1 {
				t.Fatal("retry did not save the grounded memory")
			}
		})
	}
}

func TestDreamSensitiveOnlyExtractionDoesNotRetry(t *testing.T) {
	ctx := context.Background()
	sessions := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "Prefiro respostas curtas."}})
	memories := &stubMemoryStore{}
	calls := 0
	d := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions, Memory: memories,
		StateFile: filepath.Join(t.TempDir(), "dream-state.json"),
		ExtractFn: func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
			calls++
			return []ExtractedMemory{{Type: "preference", Content: "A senha ficticia e exemplo123.",
				Evidence: dreamEvidenceForQuote(t, transcript, "Prefiro respostas curtas.")}}, nil
		},
	})
	if err := d.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := d.Stats(); stats.FailedRuns != 0 || stats.SuccessfulRuns != 1 || stats.EmptyExtractions != 1 ||
		stats.MemoriesAdded != 0 || d.lastSequence == 0 {
		t.Fatalf("sensitive-only extraction was not safely acknowledged: %+v cursor=%d", stats, d.lastSequence)
	}
	if err := d.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(memories.snapshot()) != 0 {
		t.Fatalf("sensitive-only extraction was retried or saved: calls=%d saved=%d", calls, len(memories.snapshot()))
	}
}

func TestDreamCountsSavedBatchOnlyAfterRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	stateFile := filepath.Join(t.TempDir(), "dream-state.json")
	sessions := seededSessionStore(t, []conversation.Message{{Role: conversation.RoleUser, Content: "Gosto de respostas curtas."}})
	memories := &stubMemoryStore{saveErr: errors.New("storage unavailable")}
	extractions := 0
	d := NewDreamService(DreamConfig{
		Logger: quietLogger(), SessionStore: sessions, Memory: memories, StateFile: stateFile,
		ExtractFn: func(_ context.Context, transcript string) ([]ExtractedMemory, error) {
			extractions++
			return []ExtractedMemory{{Type: "preference", FactKey: "usuario.estilo", Content: "Prefere respostas curtas.", Importance: 4,
				Evidence: dreamEvidenceForQuote(t, transcript, "Gosto de respostas curtas.")}}, nil
		},
	})
	if err := d.RunNow(ctx); err == nil {
		t.Fatal("expected storage failure")
	}
	if stats := d.Stats(); stats.MemoriesAdded != 0 || stats.MemoriesExtracted != 0 || stats.MessagesProcessed != 0 || stats.LastBatchExtracted != 1 {
		t.Fatalf("failed batch counted as committed: %+v", stats)
	}
	memories.mu.Lock()
	memories.saveErr = nil
	memories.mu.Unlock()
	if err := d.RunNow(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := d.Stats(); stats.MemoriesAdded != 1 || stats.MemoriesExtracted != 1 || stats.MessagesProcessed != 1 || stats.FailedRuns != 1 || stats.SuccessfulRuns != 1 {
		t.Fatalf("retry metrics incorrect: %+v", stats)
	}
	if extractions != 1 || len(memories.snapshot()) != 1 {
		t.Fatalf("pending extraction was not reused: calls=%d saved=%d", extractions, len(memories.snapshot()))
	}
}
