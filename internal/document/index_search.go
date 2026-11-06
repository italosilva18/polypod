package document

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

type scoredHit struct {
	SearchHit
	id int64
}

// Search returns only chunks in explicitly named scopes. A remote caller must
// pass its exact chat scope, optionally with LocalScope for shared local files.
// Empty scopes fail closed rather than searching every private conversation.
func (s *Index) Search(ctx context.Context, query string, scopes []string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 1000 {
		return nil, errors.New("consulta deve ter entre 1 e 1000 caracteres")
	}
	scopes, err := normalizedScopes(scopes)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	combined := make(map[int64]scoredHit)
	if fts := documentFTSQuery(query); fts != "" {
		if err := s.searchFTS(ctx, fts, scopes, limit*4, combined); err != nil {
			if err := s.searchLiteral(ctx, query, scopes, limit*4, combined); err != nil {
				return nil, err
			}
		}
	} else if err := s.searchLiteral(ctx, query, scopes, limit*4, combined); err != nil {
		return nil, err
	}
	if s.embedder != nil {
		s.searchSemantic(ctx, query, scopes, combined)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ranked := make([]scoredHit, 0, len(combined))
	for _, hit := range combined {
		ranked = append(ranked, hit)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			if ranked[i].SourceID == ranked[j].SourceID {
				return ranked[i].Ordinal < ranked[j].Ordinal
			}
			return ranked[i].SourceID < ranked[j].SourceID
		}
		return ranked[i].Score > ranked[j].Score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]SearchHit, len(ranked))
	for i, hit := range ranked {
		out[i] = hit.SearchHit
	}
	return out, nil
}

func normalizedScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > 8 {
		return nil, errors.New("informe de 1 a 8 escopos de documentos")
	}
	seen := make(map[string]bool, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if err := validateSource(scope, "scope-check"); err != nil {
			return nil, err
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	return out, nil
}

func scopeClause(scopes []string) (string, []any) {
	placeholders := make([]string, len(scopes))
	args := make([]any, len(scopes))
	for i, scope := range scopes {
		placeholders[i] = "?"
		args[i] = scope
	}
	return "s.scope IN (" + strings.Join(placeholders, ",") + ")", args
}

func documentFTSQuery(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	terms := make([]string, 0, min(len(words), 12))
	seen := make(map[string]bool)
	for _, word := range words {
		word = strings.ToLower(word)
		if len([]rune(word)) < 2 || len([]rune(word)) > 64 || seen[word] {
			continue
		}
		seen[word] = true
		terms = append(terms, `"`+strings.ReplaceAll(word, `"`, `""`)+`"`)
		if len(terms) == 12 {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

func (s *Index) searchFTS(ctx context.Context, query string, scopes []string, limit int, combined map[int64]scoredHit) error {
	where, args := scopeClause(scopes)
	params := append([]any{query}, args...)
	params = append(params, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,s.scope,s.source_id,s.source_hash,s.format,c.ordinal,c.content,
		bm25(document_index_fts) FROM document_index_fts
		JOIN document_index_chunks c ON c.id=document_index_fts.rowid
		JOIN document_index_sources s ON s.id=c.source_pk
		WHERE document_index_fts MATCH ? AND `+where+`
		ORDER BY bm25(document_index_fts) LIMIT ?`, params...)
	if err != nil {
		return fmt.Errorf("buscar no índice textual: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hit scoredHit
		var rank float64
		if err := rows.Scan(&hit.id, &hit.Scope, &hit.SourceID, &hit.SourceHash,
			&hit.Format, &hit.Ordinal, &hit.Text, &rank); err != nil {
			return fmt.Errorf("ler resultado textual: %w", err)
		}
		hit.Score = 0.8 / (1 + math.Abs(rank))
		hit.Citation = citationFor(hit.SearchHit)
		combined[hit.id] = hit
	}
	return rows.Err()
}

func (s *Index) searchLiteral(ctx context.Context, query string, scopes []string, limit int, combined map[int64]scoredHit) error {
	where, args := scopeClause(scopes)
	pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(query))
	params := append([]any{"%" + pattern + "%"}, args...)
	params = append(params, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,s.scope,s.source_id,s.source_hash,s.format,c.ordinal,c.content
		FROM document_index_chunks c JOIN document_index_sources s ON s.id=c.source_pk
		WHERE lower(c.content) LIKE ? ESCAPE '\' AND `+where+`
		ORDER BY s.indexed_at_ns DESC, c.ordinal ASC LIMIT ?`, params...)
	if err != nil {
		return fmt.Errorf("buscar texto literal: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hit scoredHit
		if err := rows.Scan(&hit.id, &hit.Scope, &hit.SourceID, &hit.SourceHash,
			&hit.Format, &hit.Ordinal, &hit.Text); err != nil {
			return fmt.Errorf("ler resultado literal: %w", err)
		}
		hit.Score = 0.55
		hit.Citation = citationFor(hit.SearchHit)
		combined[hit.id] = hit
	}
	return rows.Err()
}

func (s *Index) searchSemantic(ctx context.Context, query string, scopes []string, combined map[int64]scoredHit) {
	vectors, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vectors) != 1 || !validVector(vectors[0]) {
		return
	}
	where, args := scopeClause(scopes)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,s.scope,s.source_id,s.source_hash,s.format,c.ordinal,c.content,c.embedding
		FROM document_index_chunks c JOIN document_index_sources s ON s.id=c.source_pk
		WHERE `+where+` AND c.embedding IS NOT NULL AND c.embedding_model=? LIMIT 10000`,
		append(args, s.modelID)...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var hit scoredHit
		var raw string
		if err := rows.Scan(&hit.id, &hit.Scope, &hit.SourceID, &hit.SourceHash,
			&hit.Format, &hit.Ordinal, &hit.Text, &raw); err != nil {
			return
		}
		var vector []float64
		if err := json.Unmarshal([]byte(raw), &vector); err != nil {
			continue
		}
		similarity, ok := cosineVectors(vectors[0], vector)
		if !ok || similarity < 0.78 {
			continue
		}
		score := 0.8 * similarity
		if old, ok := combined[hit.id]; ok {
			score = math.Max(old.Score, score) + 0.15*math.Min(old.Score, score)
		}
		hit.Score = score
		hit.Citation = citationFor(hit.SearchHit)
		combined[hit.id] = hit
	}
}

func cosineVectors(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) || !validVector(a) || !validVector(b) {
		return 0, false
	}
	var dot, aa, bb float64
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	return dot / math.Sqrt(aa*bb), true
}

func citationFor(hit SearchHit) string {
	sum := sha256.Sum256([]byte(hit.Scope + "\x00" + hit.SourceID + "\x00" + hit.SourceHash))
	return "[doc:" + hex.EncodeToString(sum[:8]) + "#" + fmt.Sprint(hit.Ordinal) + "]"
}

// ReadChunk retrieves the exact cited chunk within its scope. The citation
// includes the revision hash, so a caller can detect an updated source.
func (s *Index) ReadChunk(ctx context.Context, scope, sourceID string, ordinal int) (SearchHit, error) {
	if err := validateSource(scope, sourceID); err != nil {
		return SearchHit{}, err
	}
	if ordinal < 1 {
		return SearchHit{}, errors.New("trecho deve ser maior que zero")
	}
	var hit SearchHit
	err := s.db.QueryRowContext(ctx, `SELECT s.scope,s.source_id,s.source_hash,s.format,c.ordinal,c.content
		FROM document_index_chunks c JOIN document_index_sources s ON s.id=c.source_pk
		WHERE s.scope=? AND s.source_id=? AND c.ordinal=?`, scope, sourceID, ordinal).Scan(
		&hit.Scope, &hit.SourceID, &hit.SourceHash, &hit.Format, &hit.Ordinal, &hit.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return SearchHit{}, ErrSourceNotFound
	}
	if err != nil {
		return SearchHit{}, fmt.Errorf("ler trecho indexado: %w", err)
	}
	hit.Citation = citationFor(hit)
	return hit, nil
}

// DeleteSource removes one source and all of its chunks in one transaction.
func (s *Index) DeleteSource(ctx context.Context, scope, sourceID string) (bool, error) {
	if err := validateSource(scope, sourceID); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("iniciar remoção do índice: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM document_index_sources WHERE scope=? AND source_id=?`, scope, sourceID)
	if err != nil {
		return false, fmt.Errorf("remover documento: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("confirmar remoção: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("concluir remoção: %w", err)
	}
	return count > 0, nil
}

// Status reports one scope only. It never exposes other chats' document counts.
func (s *Index) Status(ctx context.Context, scope string) (IndexStatus, error) {
	if err := validateSource(scope, "scope-check"); err != nil {
		return IndexStatus{}, err
	}
	var status IndexStatus
	err := s.db.QueryRowContext(ctx, `SELECT count(DISTINCT s.id),count(c.id),
		COALESCE(sum(CASE WHEN c.embedding IS NOT NULL THEN 1 ELSE 0 END),0)
		FROM document_index_sources s LEFT JOIN document_index_chunks c ON c.source_pk=s.id
		WHERE s.scope=?`, scope).Scan(&status.Documents, &status.Chunks, &status.Embedded)
	status.Semantic = s.embedder != nil
	if err != nil {
		return IndexStatus{}, fmt.Errorf("consultar estado do índice: %w", err)
	}
	return status, nil
}
