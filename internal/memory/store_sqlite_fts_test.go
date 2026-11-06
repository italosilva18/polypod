package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemoryTestDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "memories.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
	return db
}

func topicsOf(memories []Memory) map[string]bool {
	topics := make(map[string]bool, len(memories))
	for _, m := range memories {
		topics[m.Topic] = true
	}
	return topics
}

func TestSQLiteFTSMigratesAndTracksWrites(t *testing.T) {
	ctx := context.Background()
	db := openMemoryTestDB(t)
	if _, err := db.Exec(`INSERT INTO memories(topic, content, tags) VALUES (?, ?, ?)`,
		"cidade", "Moro em Recife", `["local"]`); err != nil {
		t.Fatal(err)
	}
	s := NewSQLiteStore(db)
	if !s.ftsReady {
		t.Fatal("FTS5 migration did not initialize")
	}

	check := func(query, topic string, want bool) {
		t.Helper()
		got, err := s.SearchRelevant(ctx, query, 0, 0.2)
		if err != nil {
			t.Fatal(err)
		}
		if topicsOf(got)[topic] != want {
			t.Fatalf("SearchRelevant(%q) topics = %v; %q present = %v", query, topicsOf(got), topic, want)
		}
	}
	check("Recif", "cidade", true) // suffix search within an indexed word
	if _, err := db.Exec(`UPDATE memories SET content = ? WHERE topic = ?`, "Moro em Salvador", "cidade"); err != nil {
		t.Fatal(err)
	}
	check("Recif", "cidade", false)
	check("Salvador", "cidade", true)

	// Trigram FTS cannot index a one- or two-character topic or tag. The
	// short-key table keeps reverse containment matches from IsRelevant.
	if _, err := db.Exec(`INSERT INTO memories(topic, content, tags) VALUES (?, ?, ?)`,
		"go", "Prefiro ferramentas", `[]`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memories(topic, content, tags) VALUES (?, ?, ?)`,
		"choice", "Use Rust", `["ui"]`); err != nil {
		t.Fatal(err)
	}
	check("golang", "go", true)
	check("build-ui", "choice", true)
	if _, err := db.Exec(`UPDATE memories SET tags = ? WHERE topic = ?`, `[]`, "choice"); err != nil {
		t.Fatal(err)
	}
	check("build-ui", "choice", false)
	if _, err := db.Exec(`DELETE FROM memories WHERE topic = ?`, "cidade"); err != nil {
		t.Fatal(err)
	}
	check("Salvador", "cidade", false)

	if err := s.SaveWithMeta(ctx, Memory{Topic: "idioma", Content: "Prefiro configuração simples"}); err != nil {
		t.Fatal(err)
	}
	check("configura", "idioma", true) // Portuguese stem as a substring
}

func TestSQLiteFTSFallbackWhenUnavailable(t *testing.T) {
	ctx := context.Background()
	db := openMemoryTestDB(t)
	// An occupied virtual-table name makes FTS setup fail without affecting
	// the ordinary memories table, as on a build without FTS5 support.
	if _, err := db.Exec(`CREATE TABLE memories_fts (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	s := NewSQLiteStore(db)
	if s.ftsReady {
		t.Fatal("expected FTS setup to be unavailable")
	}
	if err := s.Save(ctx, "idioma", "Prefiro configuração simples"); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchRelevant(ctx, "configura", 10, 0.2)
	if err != nil || len(got) != 1 || got[0].Topic != "idioma" {
		t.Fatalf("scan fallback failed: %v, %v", got, err)
	}
}

func TestSQLiteFTSCandidatesMatchLegacyScoring(t *testing.T) {
	ctx := context.Background()
	s := NewSQLiteStore(openMemoryTestDB(t))
	entries := []Memory{
		{Topic: "lang", Content: "Use Rust"},
		{Topic: "idioma", Content: "Prefiro configuração simples"},
		{Topic: "choice", Content: "Use Rust", Tags: []string{"a&b", "ui"}},
		{Topic: "animais", Content: "猫 鱼 狗"},
		{Topic: "citacao", Content: `A frase a"b é literal`},
		{Topic: "pontuacao", Content: "Sinal !!! importante"},
		{Topic: "acentos", Content: "ação necessária"},
	}
	for _, entry := range entries {
		if err := s.SaveWithMeta(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{
		"language", "configura", "configuração", "choose-a&b-now",
		"build-ui", "猫 狗", `a"b`, "!!!", "AÇÃO", "go",
		"um texto muito longo que passa de oitenta caracteres para acionar a busca por varredura completa",
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			want := make(map[string]bool)
			for _, m := range all {
				if relevant, score := m.IsRelevant(query); relevant && score >= 0.2 {
					want[m.Topic] = true
				}
			}
			if ftsQuery, usable := buildMemoryFTSQuery(query); usable {
				candidates, err := s.searchFTSCandidates(ctx, ftsQuery)
				if err != nil {
					t.Fatalf("FTS query for %q: %v", query, err)
				}
				indexed := topicsOf(candidates)
				for topic := range want {
					if !indexed[topic] {
						t.Fatalf("FTS candidates for %q miss %q: %v", query, topic, indexed)
					}
				}
			}
			got, err := s.SearchRelevant(ctx, query, 0, 0.2)
			if err != nil {
				t.Fatal(err)
			}
			actual := topicsOf(got)
			if len(actual) != len(want) {
				t.Fatalf("topics for %q = %v, want %v", query, actual, want)
			}
			for topic := range want {
				if !actual[topic] {
					t.Fatalf("topics for %q = %v, want %v", query, actual, want)
				}
			}
		})
	}
}

func TestSQLiteFTSManagedRevisionAndForget(t *testing.T) {
	ctx := context.Background()
	store := NewManagedStore(NewSQLiteStore(openMemoryTestDB(t)))
	for i := 0; i < 60; i++ {
		topic := fmt.Sprintf("cidade_%02d", i)
		if err := store.SaveWithMeta(ctx, Memory{Topic: topic, Content: "Moro em Recife", Source: "dream"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWithMeta(ctx, Memory{Topic: topic, Content: "Agora moro em Salvador", Source: "dream"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveWithMeta(ctx, Memory{Topic: "cidade_atual", Content: "Moro em Recife hoje", Source: "user"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.SearchRelevant(ctx, "Recife", 1, 0.2)
	if err != nil || len(got) != 1 || got[0].Topic != "cidade_atual" {
		t.Fatalf("active result starved by revisions: %v, %v", got, err)
	}
	if err := store.Delete(ctx, "cidade_atual"); err != nil {
		t.Fatal(err)
	}
	got, err = store.SearchRelevant(ctx, "Recife", 10, 0.2)
	if err != nil || len(got) != 0 {
		t.Fatalf("forgotten or superseded content leaked: %v, %v", got, err)
	}
	if err := store.SaveWithMeta(ctx, Memory{Topic: "cidade_atual", Content: "Moro em Recife hoje", Source: "dream"}); err != nil {
		t.Fatal(err)
	}
	got, err = store.SearchRelevant(ctx, "Recife", 10, 0.2)
	if err != nil || len(got) != 0 {
		t.Fatalf("automatic save bypassed tombstone: %v, %v", got, err)
	}
	if err := store.Save(ctx, "cidade_atual", "Moro em Recife hoje"); err != nil {
		t.Fatal(err)
	}
	got, err = store.SearchRelevant(ctx, "Recife", 1, 0.2)
	if err != nil || len(got) != 1 || got[0].Topic != "cidade_atual" {
		t.Fatalf("explicit save did not revoke tombstone: %v, %v", got, err)
	}
}

func BenchmarkSQLiteSearchRelevant(b *testing.B) {
	ctx := context.Background()
	db := openMemoryTestDB(b)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO memories(topic, content) VALUES (?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 8000; i++ {
		content := fmt.Sprintf("Nota de projeto %d: preferências gerais para ferramenta de linha de comando", i)
		if i == 7999 {
			content = "Usar xylophonic para esta integração específica"
		}
		if _, err := stmt.Exec(fmt.Sprintf("nota_%d", i), content); err != nil {
			b.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	s := NewSQLiteStore(db)
	if !s.ftsReady {
		b.Skip("FTS5 unavailable")
	}
	for _, search := range []struct {
		name  string
		query string
	}{
		{name: "rare", query: "xylophonic"},
		{name: "common", query: "ferramenta"},
	} {
		b.Run(search.name, func(b *testing.B) {
			query := search.query
			b.Run("fts5", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					got, err := s.SearchRelevant(ctx, query, 1, 0.2)
					if err != nil || len(got) != 1 {
						b.Fatalf("search: %v, %v", got, err)
					}
				}
			})
			b.Run("previous_full_scan", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					all, err := s.List(ctx)
					if err != nil {
						b.Fatal(err)
					}
					var got []Memory
					for _, m := range all {
						if relevant, score := m.IsRelevant(query); relevant && score >= 0.2 {
							got = append(got, m)
						}
					}
					sort.SliceStable(got, func(i, j int) bool {
						_, a := got[i].IsRelevant(query)
						_, c := got[j].IsRelevant(query)
						return a > c
					})
					if len(got) == 0 {
						b.Fatal("scan returned no matches")
					}
				}
			})
		})
	}
}
