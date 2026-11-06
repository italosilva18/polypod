package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The trigram index produces a superset of the matches scored by IsRelevant.
// A short-key table covers topics and tags shorter than three characters,
// which FTS5's trigram tokenizer cannot index. Both are maintained by the
// same triggers, so direct SQL writes also keep retrieval current.
const memoryFTSCreate = `CREATE VIRTUAL TABLE memories_fts USING fts5(
	topic, content, tags, content='memories', content_rowid='id', tokenize='trigram')`

const memoryFTSShortCreate = `CREATE TABLE memories_fts_short (memory_id INTEGER PRIMARY KEY)`

// Tags are stored as JSON. JSON and Go's encoder escape some characters,
// so those tags also need to be checked outside the trigram index.
func memoryFTSShortPredicate(row string) string {
	return fmt.Sprintf(`
	(length(%[1]s.topic) BETWEEN 1 AND 2)
	OR EXISTS (
		SELECT 1 FROM json_each(CASE WHEN json_valid(%[1]s.tags) THEN %[1]s.tags ELSE '[]' END)
		WHERE type = 'text' AND (
			length(value) BETWEEN 1 AND 2
			OR json_quote(value) <> '"' || value || '"'
			OR instr(value, '<') > 0 OR instr(value, '>') > 0 OR instr(value, '&') > 0
		)
	)`, row)
}

func (s *SQLiteStore) ensureFTS(ctx context.Context) bool {
	s.ftsMu.Lock()
	defer s.ftsMu.Unlock()
	if s.ftsReady {
		return true
	}
	if err := s.initializeFTS(ctx); err != nil {
		return false
	}
	s.ftsReady = true
	return true
}

func (s *SQLiteStore) initializeFTS(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting memory FTS migration: %w", err)
	}
	defer tx.Rollback()

	var definition string
	err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='memories_fts'`).Scan(&definition)
	newFTS := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newFTS {
		return fmt.Errorf("inspecting memory FTS schema: %w", err)
	}
	if newFTS {
		if _, err := tx.ExecContext(ctx, memoryFTSCreate); err != nil {
			return fmt.Errorf("creating memory FTS index: %w", err)
		}
	} else if !strings.Contains(strings.ToLower(definition), "using fts5") {
		return errors.New("memory FTS name is occupied by a non-FTS table")
	}

	var shortTable int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name='memories_fts_short'`).Scan(&shortTable)
	newShort := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newShort {
		return fmt.Errorf("inspecting memory short-key index: %w", err)
	}
	if newShort {
		if _, err := tx.ExecContext(ctx, memoryFTSShortCreate); err != nil {
			return fmt.Errorf("creating memory short-key index: %w", err)
		}
	}

	shortPredicateNew := memoryFTSShortPredicate("new")
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, topic, content, tags)
			VALUES (new.id, new.topic, new.content, new.tags);
			INSERT INTO memories_fts_short(memory_id)
			SELECT new.id WHERE ` + shortPredicateNew + `;
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, topic, content, tags)
			VALUES ('delete', old.id, old.topic, old.content, old.tags);
			DELETE FROM memories_fts_short WHERE memory_id = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_fts_au AFTER UPDATE OF id, topic, content, tags ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, topic, content, tags)
			VALUES ('delete', old.id, old.topic, old.content, old.tags);
			DELETE FROM memories_fts_short WHERE memory_id = old.id;
			INSERT INTO memories_fts(rowid, topic, content, tags)
			VALUES (new.id, new.topic, new.content, new.tags);
			INSERT INTO memories_fts_short(memory_id)
			SELECT new.id WHERE ` + shortPredicateNew + `;
		END`,
	}
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, trigger); err != nil {
			return fmt.Errorf("creating memory FTS trigger: %w", err)
		}
	}

	// Existing databases had no index. Rebuild exactly once when the virtual
	// table is created; later writes are incremental through the triggers.
	if newFTS {
		if _, err := tx.ExecContext(ctx, `INSERT INTO memories_fts(memories_fts) VALUES('rebuild')`); err != nil {
			return fmt.Errorf("backfilling memory FTS index: %w", err)
		}
	}
	if newShort {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memories_fts_short(memory_id) SELECT m.id FROM memories AS m WHERE `+memoryFTSShortPredicate("m")); err != nil {
			return fmt.Errorf("backfilling memory short-key index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing memory FTS migration: %w", err)
	}
	return nil
}

// Full words and the whole query find content/topic/tag substrings and word
// overlap. Topic/tag column-filtered trigrams cover the reverse containment
// rules in IsRelevant (a short topic or tag can appear inside a longer query).
// Very long and short queries use the original scan to avoid broad FTS queries.
func buildMemoryFTSQuery(query string) (string, bool) {
	if strings.IndexByte(query, 0) >= 0 {
		return "", false
	}
	// IsRelevant counts word length in bytes. A one- or two-rune CJK word
	// therefore contributes to its score, even though trigram FTS cannot
	// index that word on its own.
	for _, word := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(word) > 2 && utf8.RuneCountInString(word) < 3 {
			return "", false
		}
	}
	normalized := strings.ToLower(strings.TrimSpace(query))
	runes := []rune(normalized)
	if len(runes) < 3 || len(runes) > 80 {
		return "", false
	}
	grams := make([]string, 0, len(runes)-2)
	seen := make(map[string]bool, len(runes)-2)
	for i := 0; i+3 <= len(runes); i++ {
		gram := string(runes[i : i+3])
		if strings.TrimSpace(gram) == "" || seen[gram] {
			continue
		}
		seen[gram] = true
		grams = append(grams, quoteMemoryFTSTerm(gram))
	}
	if len(grams) == 0 {
		return "", false
	}
	terms := []string{quoteMemoryFTSTerm(normalized)}
	seenWords := make(map[string]bool)
	for _, word := range strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if utf8.RuneCountInString(word) >= 3 && word != normalized && !seenWords[word] {
			seenWords[word] = true
			terms = append(terms, quoteMemoryFTSTerm(word))
		}
	}
	gramQuery := strings.Join(grams, " OR ")
	terms = append(terms, "topic : ("+gramQuery+")", "tags : ("+gramQuery+")")
	return strings.Join(terms, " OR "), true
}

func quoteMemoryFTSTerm(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}

func (s *SQLiteStore) searchFTSCandidates(ctx context.Context, ftsQuery string) ([]Memory, error) {
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT rowid AS id FROM memories_fts WHERE memories_fts MATCH ?
		UNION SELECT memory_id AS id FROM memories_fts_short
	)
	SELECT m.id, m.topic, m.content, m.created_at, m.updated_at,
		m.tags, m.priority, m.session_id, m.source, m.confidence
	FROM candidates c JOIN memories m ON m.id = c.id ORDER BY m.updated_at DESC`, ftsQuery)
	if err != nil {
		return nil, fmt.Errorf("searching memory FTS index: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}
