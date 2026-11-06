package document

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/memory"
	_ "modernc.org/sqlite"
)

// LocalScope separates documents explicitly indexed from local files from
// channel-scoped attachments. Callers must name every scope they want to read.
const LocalScope = "local"

const (
	indexChunkRunes   = 1200
	indexChunkOverlap = 120
	maxIndexSourceID  = 2048
	maxSearchResults  = 40
)

var ErrSourceNotFound = errors.New("documento indexado não encontrado")

// IndexedDocument describes the exact source revision stored by the index.
// The source hash lets a caller detect citations from an older revision.
type IndexedDocument struct {
	Scope        string
	SourceID     string
	SourceHash   string
	Format       string
	Chunks       int
	Pages        int
	PageLimit    int
	UsedOCR      bool
	TruncatedPDF bool
	IndexedAt    time.Time
}

// Citation identifies one chunk of this exact source revision without
// exposing the scope or a remote sender identifier.
func (d IndexedDocument) Citation(ordinal int) string {
	if ordinal < 1 || ordinal > d.Chunks {
		return ""
	}
	return citationFor(SearchHit{Scope: d.Scope, SourceID: d.SourceID,
		SourceHash: d.SourceHash, Ordinal: ordinal})
}

// SearchHit contains one bounded chunk and a stable citation for that source
// revision. Ordinal is one based; SourceID is never inferred from Scope.
type SearchHit struct {
	Scope      string
	SourceID   string
	SourceHash string
	Format     string
	Ordinal    int
	Text       string
	Score      float64
	Citation   string
}

type IndexStatus struct {
	Documents int
	Chunks    int
	Embedded  int
	Semantic  bool
}

// Index persists extracted document chunks separately from conversation and
// fact-memory databases. It uses SQLite FTS5 and optional semantic vectors.
type Index struct {
	db       *sql.DB
	embedder memory.Embedder
	modelID  string
	mu       sync.Mutex
}

// OpenIndex opens a private index. The host may pass nil for lexical-only
// search; no network provider is selected implicitly.
func OpenIndex(path string, embedder memory.Embedder) (*Index, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("caminho do índice obrigatório")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolver índice: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, fmt.Errorf("criar diretório do índice: %w", err)
	}
	if info, err := os.Lstat(abs); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("índice deve ser arquivo comum, não link simbólico")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspecionar índice: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("abrir arquivo do índice: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("fechar arquivo do índice: %w", err)
	}
	if err := os.Chmod(abs, 0600); err != nil {
		return nil, fmt.Errorf("proteger arquivo do índice: %w", err)
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("abrir SQLite do índice: %w", err)
	}
	db.SetMaxOpenConns(1)
	idx := &Index{db: db, embedder: embedder, modelID: embedderIdentity(embedder)}
	if err := idx.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

func (s *Index) initialize(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=FULL`,
		`PRAGMA secure_delete=ON`,
	}
	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configurar SQLite do índice: %w", err)
		}
	}
	var existing int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='document_index_fts'`).Scan(&existing)
	newFTS := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newFTS {
		return fmt.Errorf("inspecionar FTS do índice: %w", err)
	}
	schema := []string{
		`CREATE TABLE IF NOT EXISTS document_index_sources (
			id INTEGER PRIMARY KEY, scope TEXT NOT NULL, source_id TEXT NOT NULL,
			source_hash TEXT NOT NULL, format TEXT NOT NULL, chunks INTEGER NOT NULL,
			pages INTEGER NOT NULL DEFAULT 0, page_limit INTEGER NOT NULL DEFAULT 0,
			used_ocr INTEGER NOT NULL DEFAULT 0, truncated_pdf INTEGER NOT NULL DEFAULT 0,
			indexed_at_ns INTEGER NOT NULL, UNIQUE(scope, source_id))`,
		`CREATE TABLE IF NOT EXISTS document_index_chunks (
			id INTEGER PRIMARY KEY,
			source_pk INTEGER NOT NULL REFERENCES document_index_sources(id) ON DELETE CASCADE,
			ordinal INTEGER NOT NULL, content TEXT NOT NULL, embedding TEXT,
			embedding_model TEXT NOT NULL DEFAULT '',
			UNIQUE(source_pk, ordinal))`,
		`CREATE INDEX IF NOT EXISTS document_index_chunks_source ON document_index_chunks(source_pk, ordinal)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS document_index_fts USING fts5(
			content, content='document_index_chunks', content_rowid='id',
			tokenize='unicode61 remove_diacritics 2')`,
		`CREATE TRIGGER IF NOT EXISTS document_index_ai AFTER INSERT ON document_index_chunks BEGIN
			INSERT INTO document_index_fts(rowid, content) VALUES (new.id, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS document_index_ad AFTER DELETE ON document_index_chunks BEGIN
			INSERT INTO document_index_fts(document_index_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS document_index_au AFTER UPDATE OF content ON document_index_chunks BEGIN
			INSERT INTO document_index_fts(document_index_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
			INSERT INTO document_index_fts(rowid, content) VALUES (new.id, new.content);
		END`,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("iniciar esquema do índice: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range schema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("criar esquema do índice: %w", err)
		}
	}
	columns, err := tx.QueryContext(ctx, `PRAGMA table_info(document_index_chunks)`)
	if err != nil {
		return fmt.Errorf("inspecionar esquema de vetores: %w", err)
	}
	hasModel := false
	for columns.Next() {
		var position, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := columns.Scan(&position, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			columns.Close()
			return fmt.Errorf("ler esquema de vetores: %w", err)
		}
		hasModel = hasModel || name == "embedding_model"
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return fmt.Errorf("verificar esquema de vetores: %w", err)
	}
	if err := columns.Close(); err != nil {
		return fmt.Errorf("fechar inspeção de vetores: %w", err)
	}
	if !hasModel {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE document_index_chunks ADD COLUMN embedding_model TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrar identidade dos vetores: %w", err)
		}
	}
	if newFTS {
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_index_fts(document_index_fts) VALUES('rebuild')`); err != nil {
			return fmt.Errorf("preencher índice de busca: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("concluir esquema do índice: %w", err)
	}
	return nil
}

func (s *Index) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// IndexFile extracts a local file and atomically replaces its prior revision.
// The canonical path is the source ID within LocalScope.
func (s *Index) IndexFile(ctx context.Context, path string, opts Options) (IndexedDocument, bool, error) {
	if err := ctx.Err(); err != nil {
		return IndexedDocument{}, false, err
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return IndexedDocument{}, false, fmt.Errorf("resolver documento: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return IndexedDocument{}, false, fmt.Errorf("resolver arquivo de origem: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return IndexedDocument{}, false, errors.New("origem deve ser arquivo comum")
	}
	if info.Size() > MaxInputBytes {
		return IndexedDocument{}, false, fmt.Errorf("documento excede %d MiB", MaxInputBytes>>20)
	}
	hash, err := hashFile(ctx, canonical, opts)
	if err != nil {
		return IndexedDocument{}, false, err
	}
	if existing, found, err := s.getSource(ctx, LocalScope, canonical); err != nil {
		return IndexedDocument{}, false, err
	} else if found && existing.SourceHash == hash && (s.embedder == nil || s.embeddingsComplete(ctx, LocalScope, canonical)) {
		return existing, false, nil
	}
	extracted, err := Extract(ctx, canonical, opts)
	if err != nil {
		return IndexedDocument{}, false, err
	}
	infoAfter, err := os.Stat(canonical)
	if err != nil || infoAfter.Size() != info.Size() || !infoAfter.ModTime().Equal(info.ModTime()) {
		return IndexedDocument{}, false, errors.New("documento mudou durante a indexação")
	}
	meta := IndexedDocument{Scope: LocalScope, SourceID: canonical, SourceHash: hash,
		Format: extracted.Format, Pages: extracted.Pages, PageLimit: extracted.PageLimit,
		UsedOCR: extracted.UsedOCR, TruncatedPDF: extracted.TruncatedPDF}
	return s.upsert(ctx, meta, extracted.Text)
}

// IndexText indexes already extracted text, including a remote attachment.
// scope and sourceID must be stable and caller-provided. For a remote channel,
// use an exact chat scope and a source ID that does not contain a phone number.
func (s *Index) IndexText(ctx context.Context, scope, sourceID, content, format string) (IndexedDocument, bool, error) {
	if err := validateSource(scope, sourceID); err != nil {
		return IndexedDocument{}, false, err
	}
	if !utf8.ValidString(content) || len(content) > MaxOutputBytes || strings.TrimSpace(content) == "" {
		return IndexedDocument{}, false, errors.New("texto deve ser UTF-8, não vazio e ter no máximo 4 MiB")
	}
	if format == "" {
		format = ".txt"
	}
	sum := sha256.Sum256([]byte(format + "\x00" + content))
	meta := IndexedDocument{Scope: scope, SourceID: sourceID,
		SourceHash: hex.EncodeToString(sum[:]), Format: format}
	return s.upsert(ctx, meta, content)
}

func validateSource(scope, sourceID string) error {
	if strings.TrimSpace(scope) != scope || scope == "" || len(scope) > maxIndexSourceID || strings.IndexByte(scope, 0) >= 0 {
		return errors.New("escopo do documento inválido")
	}
	if strings.TrimSpace(sourceID) != sourceID || sourceID == "" || len(sourceID) > maxIndexSourceID || strings.IndexByte(sourceID, 0) >= 0 {
		return errors.New("identificador do documento inválido")
	}
	return nil
}

func hashFile(ctx context.Context, path string, opts Options) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("abrir origem: %w", err)
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, MaxInputBytes+1))
	if err != nil {
		return "", fmt.Errorf("ler origem: %w", err)
	}
	if n > MaxInputBytes {
		return "", fmt.Errorf("documento excede %d MiB", MaxInputBytes>>20)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fmt.Fprint(h, "\x00", strconv.FormatBool(opts.OCR), "\x00", opts.OCRLanguage)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Index) getSource(ctx context.Context, scope, sourceID string) (IndexedDocument, bool, error) {
	var out IndexedDocument
	var usedOCR, truncated int
	var indexed int64
	err := s.db.QueryRowContext(ctx, `SELECT source_hash,format,chunks,pages,page_limit,used_ocr,truncated_pdf,indexed_at_ns
		FROM document_index_sources WHERE scope=? AND source_id=?`, scope, sourceID).Scan(
		&out.SourceHash, &out.Format, &out.Chunks, &out.Pages, &out.PageLimit, &usedOCR, &truncated, &indexed)
	if errors.Is(err, sql.ErrNoRows) {
		return IndexedDocument{}, false, nil
	}
	if err != nil {
		return IndexedDocument{}, false, fmt.Errorf("consultar documento indexado: %w", err)
	}
	out.Scope, out.SourceID = scope, sourceID
	out.UsedOCR, out.TruncatedPDF = usedOCR != 0, truncated != 0
	out.IndexedAt = time.Unix(0, indexed)
	return out, true, nil
}

func (s *Index) embeddingsComplete(ctx context.Context, scope, sourceID string) bool {
	var missing int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM document_index_chunks c
		JOIN document_index_sources d ON d.id=c.source_pk
		WHERE d.scope=? AND d.source_id=? AND (c.embedding IS NULL OR c.embedding_model<>?)`,
		scope, sourceID, s.modelID).Scan(&missing)
	return err == nil && missing == 0
}

func (s *Index) upsert(ctx context.Context, meta IndexedDocument, content string) (IndexedDocument, bool, error) {
	if err := validateSource(meta.Scope, meta.SourceID); err != nil {
		return IndexedDocument{}, false, err
	}
	if existing, found, err := s.getSource(ctx, meta.Scope, meta.SourceID); err != nil {
		return IndexedDocument{}, false, err
	} else if found && existing.SourceHash == meta.SourceHash &&
		(s.embedder == nil || s.embeddingsComplete(ctx, meta.Scope, meta.SourceID)) {
		return existing, false, nil
	}
	chunks := splitIndexText(content)
	if len(chunks) == 0 {
		return IndexedDocument{}, false, errors.New("documento não contém texto indexável")
	}
	meta.Chunks = len(chunks)
	meta.IndexedAt = time.Now().UTC()
	vectors := make([]string, len(chunks))
	if s.embedder != nil {
		for start := 0; start < len(chunks); start += 32 {
			end := min(start+32, len(chunks))
			batch, err := s.embedder.Embed(ctx, chunks[start:end])
			if err != nil || len(batch) != end-start {
				break // lexical search remains complete when the provider is down
			}
			for i, vector := range batch {
				if !validVector(vector) {
					continue
				}
				encoded, err := json.Marshal(vector)
				if err == nil {
					vectors[start+i] = string(encoded)
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return IndexedDocument{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IndexedDocument{}, false, fmt.Errorf("iniciar indexação: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO document_index_sources
		(scope,source_id,source_hash,format,chunks,pages,page_limit,used_ocr,truncated_pdf,indexed_at_ns)
		VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(scope,source_id) DO UPDATE SET
		source_hash=excluded.source_hash,format=excluded.format,chunks=excluded.chunks,
		pages=excluded.pages,page_limit=excluded.page_limit,used_ocr=excluded.used_ocr,
		truncated_pdf=excluded.truncated_pdf,indexed_at_ns=excluded.indexed_at_ns`,
		meta.Scope, meta.SourceID, meta.SourceHash, meta.Format, meta.Chunks, meta.Pages,
		meta.PageLimit, meta.UsedOCR, meta.TruncatedPDF, meta.IndexedAt.UnixNano())
	if err != nil {
		return IndexedDocument{}, false, fmt.Errorf("gravar documento: %w", err)
	}
	var sourcePK int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM document_index_sources WHERE scope=? AND source_id=?`,
		meta.Scope, meta.SourceID).Scan(&sourcePK); err != nil {
		return IndexedDocument{}, false, fmt.Errorf("obter ID do documento: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_index_chunks WHERE source_pk=?`, sourcePK); err != nil {
		return IndexedDocument{}, false, fmt.Errorf("substituir trechos: %w", err)
	}
	for i, chunk := range chunks {
		var vector any
		if vectors[i] != "" {
			vector = vectors[i]
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO document_index_chunks(source_pk,ordinal,content,embedding,embedding_model)
			VALUES (?,?,?,?,?)`, sourcePK, i+1, chunk, vector, s.modelID); err != nil {
			return IndexedDocument{}, false, fmt.Errorf("gravar trecho %d: %w", i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return IndexedDocument{}, false, fmt.Errorf("concluir indexação: %w", err)
	}
	return meta, true, nil
}

func validVector(v []float64) bool {
	if len(v) == 0 || len(v) > 8192 {
		return false
	}
	var norm float64
	for _, n := range v {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		norm += n * n
	}
	return norm > 0 && !math.IsInf(norm, 0)
}

func embedderIdentity(embedder memory.Embedder) string {
	if embedder == nil {
		return ""
	}
	if named, ok := embedder.(interface{ Name() string }); ok && named.Name() != "" {
		return named.Name()
	}
	return fmt.Sprintf("%T", embedder)
}

func splitIndexText(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	runes := []rune(strings.TrimSpace(content))
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]string, 0, (len(runes)+indexChunkRunes-1)/indexChunkRunes)
	for start := 0; start < len(runes); {
		end := min(start+indexChunkRunes, len(runes))
		if end < len(runes) {
			for i := end; i > start+indexChunkRunes*3/4; i-- {
				if runes[i-1] == ' ' || runes[i-1] == '\n' {
					end = i
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end == len(runes) {
			break
		}
		start = max(end-indexChunkOverlap, start+1)
	}
	return chunks
}
