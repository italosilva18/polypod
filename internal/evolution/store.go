package evolution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const minSuccesses = 3

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var evidenceTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:/.-]{0,499}$`)

const schema = `
PRAGMA busy_timeout=5000;
PRAGMA synchronous=FULL;
CREATE TABLE IF NOT EXISTS outcomes (
 id TEXT PRIMARY KEY, source_kind TEXT NOT NULL, source_id TEXT NOT NULL,
 pattern_key TEXT NOT NULL, success INTEGER NOT NULL, verified_by TEXT NOT NULL,
 evidence_ref TEXT NOT NULL, tool_names_json TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(source_kind,source_id)
);
CREATE INDEX IF NOT EXISTS outcomes_pattern_idx ON outcomes(pattern_key,created_at);
CREATE TABLE IF NOT EXISTS verification_plans (
 source_kind TEXT NOT NULL, subject_id TEXT NOT NULL, pattern_key TEXT NOT NULL,
 kind TEXT NOT NULL, path TEXT NOT NULL, selector TEXT NOT NULL DEFAULT '',
 expected_status INTEGER NOT NULL DEFAULT 0, expected_sha256 TEXT NOT NULL,
 baseline_sha256 TEXT NOT NULL, created_at INTEGER NOT NULL,
 PRIMARY KEY(source_kind,subject_id)
);
CREATE TABLE IF NOT EXISTS verification_runs (
 source_kind TEXT NOT NULL, run_id TEXT NOT NULL, subject_id TEXT NOT NULL,
 baseline_sha256 TEXT NOT NULL, created_at INTEGER NOT NULL,
 final_sha256 TEXT, finished_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(source_kind,run_id)
);
CREATE TABLE IF NOT EXISTS tool_traces (
 source_kind TEXT NOT NULL, run_id TEXT NOT NULL, tool_names_json TEXT NOT NULL,
 created_at INTEGER NOT NULL, PRIMARY KEY(source_kind,run_id)
);
CREATE TABLE IF NOT EXISTS drafts (
 id TEXT PRIMARY KEY, pattern_key TEXT NOT NULL UNIQUE, target_skill TEXT NOT NULL UNIQUE,
 body TEXT NOT NULL, body_sha256 TEXT NOT NULL, status TEXT NOT NULL,
 evidence_json TEXT NOT NULL, success_count INTEGER NOT NULL, total_count INTEGER NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 reviewed_by TEXT NOT NULL, reviewed_at INTEGER NOT NULL, applied_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS drafts_status_idx ON drafts(status,updated_at);
`

// Store persists reviewed learning separately from conversation memory. The
// mutex serializes filesystem application; SQLite serializes record updates.
type Store struct {
	db        *sql.DB
	skillsDir string
	applyMu   sync.Mutex
	verifyMu  sync.Mutex
}

// Open creates private state and reconciles a publication interrupted between
// writing SKILL.md and committing its database status.
func Open(dbPath, skillsDir string) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(skillsDir) == "" {
		return nil, errors.New("caminhos de evolução e skills são obrigatórios")
	}
	absSkills, err := filepath.Abs(skillsDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dbPath, os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criando evolução: %w", err)
	}
	if err := ensureVerificationRunColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("atualizando execuções verificadas: %w", err)
	}
	if err := ensureVerificationPlanColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("atualizando planos verificados: %w", err)
	}
	s := &Store{db: db, skillsDir: absSkills}
	if err := s.recoverApplying(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("recuperando skills aplicadas: %w", err)
	}
	return s, nil
}

func ensureVerificationPlanColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(verification_plans)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"selector", "selector TEXT NOT NULL DEFAULT ''"},
		{"expected_status", "expected_status INTEGER NOT NULL DEFAULT 0"},
	} {
		if !columns[column.name] {
			if _, err := db.Exec(`ALTER TABLE verification_plans ADD COLUMN ` + column.definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureVerificationRunColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(verification_runs)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var position int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"final_sha256", "final_sha256 TEXT"},
		{"finished_at", "finished_at INTEGER NOT NULL DEFAULT 0"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE verification_runs ADD COLUMN ` + column.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// RecordOutcome accepts a caller-verified result. Verification is an assertion
// supplied by the caller; this package never treats external page or model text
// as verification. The source pair is idempotent and immutable.
func (s *Store) RecordOutcome(ctx context.Context, in OutcomeInput) (Outcome, error) {
	if internalVerifier(in.VerifiedBy) {
		return Outcome{}, errors.New("identificador reservado ao verificador interno")
	}
	return s.recordOutcome(ctx, in)
}

func (s *Store) recordOutcome(ctx context.Context, in OutcomeInput) (Outcome, error) {
	var err error
	in, err = validateOutcome(in)
	if err != nil {
		return Outcome{}, err
	}
	toolsJSON, err := json.Marshal(in.ToolNames)
	if err != nil {
		return Outcome{}, err
	}
	created := time.Now().UnixMilli()
	id := "outcome_" + uuid.NewString()
	success := 0
	if in.Success {
		success = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO outcomes
 (id,source_kind,source_id,pattern_key,success,verified_by,evidence_ref,tool_names_json,created_at)
 VALUES(?,?,?,?,?,?,?,?,?)`, id, in.SourceKind, in.SourceID, in.PatternKey, success,
		in.VerifiedBy, in.EvidenceRef, string(toolsJSON), created)
	if err != nil {
		return Outcome{}, err
	}
	out, err := s.outcomeBySource(ctx, in.SourceKind, in.SourceID)
	if err != nil {
		return Outcome{}, err
	}
	if out.PatternKey != in.PatternKey || out.Success != in.Success || out.VerifiedBy != in.VerifiedBy ||
		out.EvidenceRef != in.EvidenceRef || !reflect.DeepEqual(out.ToolNames, in.ToolNames) {
		return Outcome{}, fmt.Errorf("fonte %s/%s já registrada com outro resultado: %w", in.SourceKind, in.SourceID, ErrStateConflict)
	}
	return out, nil
}

func validateOutcome(in OutcomeInput) (OutcomeInput, error) {
	in.SourceKind = strings.TrimSpace(in.SourceKind)
	in.SourceID = strings.TrimSpace(in.SourceID)
	in.PatternKey = normalizePattern(in.PatternKey)
	in.VerifiedBy = strings.TrimSpace(in.VerifiedBy)
	in.EvidenceRef = strings.TrimSpace(in.EvidenceRef)
	switch in.SourceKind {
	case "task", "routine", "manual":
	default:
		return in, errors.New("source_kind deve ser task, routine ou manual")
	}
	if !simpleMetadata(in.SourceID, 160) || !simpleMetadata(in.VerifiedBy, 120) ||
		!simpleMetadata(in.EvidenceRef, 500) || in.PatternKey == "" || len(in.PatternKey) > 160 {
		return in, errors.New("fonte, padrão, verificador e referência de evidência são obrigatórios e limitados")
	}
	if !evidenceTokenPattern.MatchString(in.SourceID) || !evidenceTokenPattern.MatchString(in.VerifiedBy) ||
		!evidenceTokenPattern.MatchString(in.EvidenceRef) {
		return in, errors.New("source_id, verified_by e evidence_ref devem ser identificadores, não texto externo")
	}
	verifiedWithoutTrace := internalVerifier(in.VerifiedBy) &&
		strings.HasPrefix(in.EvidenceRef, verifierKind(in.VerifiedBy)+":") &&
		(in.SourceKind == "task" || in.SourceKind == "routine")
	if len(in.ToolNames) > 24 || (in.Success && len(in.ToolNames) == 0 && !verifiedWithoutTrace) {
		return in, errors.New("sucesso exige de 1 a 24 ferramentas observadas")
	}
	for _, name := range in.ToolNames {
		if !toolNamePattern.MatchString(name) {
			return in, fmt.Errorf("nome de ferramenta inválido: %q", name)
		}
	}
	return in, nil
}

func simpleMetadata(value string, limit int) bool {
	return value != "" && len(value) <= limit && !strings.ContainsAny(value, "\n\r\x00")
}

func normalizePattern(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func (s *Store) outcomeBySource(ctx context.Context, kind, id string) (Outcome, error) {
	return scanOutcome(s.db.QueryRowContext(ctx, `SELECT id,source_kind,source_id,pattern_key,success,
 verified_by,evidence_ref,tool_names_json,created_at FROM outcomes WHERE source_kind=? AND source_id=?`, kind, id))
}

func (s *Store) Outcomes(ctx context.Context, patternKey string) ([]Outcome, error) {
	key := normalizePattern(patternKey)
	if key == "" {
		return nil, errors.New("pattern_key é obrigatório")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,source_kind,source_id,pattern_key,success,
 verified_by,evidence_ref,tool_names_json,created_at FROM outcomes WHERE pattern_key=? ORDER BY created_at,id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var outcomes []Outcome
	for rows.Next() {
		outcome, err := scanOutcome(rows)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, rows.Err()
}

func (s *Store) Patterns(ctx context.Context) ([]Pattern, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pattern_key,SUM(success),COUNT(*) FROM outcomes
 GROUP BY pattern_key ORDER BY COUNT(*) DESC,pattern_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var patterns []Pattern
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.Key, &p.SuccessCount, &p.TotalCount); err != nil {
			return nil, err
		}
		p.Eligible = sufficientEvidence(p.SuccessCount, p.TotalCount)
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

func sufficientEvidence(successes, total int) bool {
	return successes >= minSuccesses && total > 0 && successes*4 >= total*3
}

// Propose groups repeated results by a stable category and tool sequence.
// Free-form outcome text is never copied into the executable draft.
func (s *Store) Propose(ctx context.Context, patternKey string) (Draft, error) {
	key := normalizePattern(patternKey)
	if key == "" || len(key) > 160 {
		return Draft{}, errors.New("pattern_key inválido")
	}
	if existing, err := s.draftByPattern(ctx, key); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Draft{}, err
	}
	outcomes, err := s.Outcomes(ctx, key)
	if err != nil {
		return Draft{}, err
	}
	successes := 0
	evidence := make([]Evidence, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Success {
			successes++
		}
		evidence = append(evidence, Evidence{
			OutcomeID: outcome.ID, SourceKind: outcome.SourceKind, SourceID: outcome.SourceID,
			Success: outcome.Success, VerifiedBy: outcome.VerifiedBy, EvidenceRef: outcome.EvidenceRef,
		})
	}
	if !sufficientEvidence(successes, len(outcomes)) {
		return Draft{}, ErrNotEligible
	}
	sequence, repetitions := mostFrequentSequence(outcomes)
	if len(sequence) == 0 || repetitions < 2 {
		return Draft{}, fmt.Errorf("sem sequência de ferramentas repetida: %w", ErrNotEligible)
	}
	if len(evidence) > 50 {
		evidence = evidence[len(evidence)-50:]
	}
	name := skillNameFor(key)
	body := renderDraft(name, sequence, successes, len(outcomes), evidence)
	if err := validateSkillBody(body, name); err != nil {
		return Draft{}, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return Draft{}, err
	}
	now := time.Now().UnixMilli()
	draft := Draft{ID: "draft_" + uuid.NewString(), PatternKey: key, TargetSkill: name,
		Body: body, BodySHA256: hashBody(body), Status: Candidate, Evidence: evidence,
		SuccessCount: successes, TotalCount: len(outcomes), CreatedAt: now, UpdatedAt: now}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO drafts
 (id,pattern_key,target_skill,body,body_sha256,status,evidence_json,success_count,total_count,
 created_at,updated_at,reviewed_by,reviewed_at,applied_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		draft.ID, draft.PatternKey, draft.TargetSkill, draft.Body, draft.BodySHA256, draft.Status,
		string(evidenceJSON), draft.SuccessCount, draft.TotalCount, now, now, "", 0, 0)
	if err != nil {
		return Draft{}, err
	}
	return s.draftByPattern(ctx, key)
}

func mostFrequentSequence(outcomes []Outcome) ([]string, int) {
	counts := make(map[string]int)
	for _, outcome := range outcomes {
		if outcome.Success && len(outcome.ToolNames) > 0 {
			counts[strings.Join(outcome.ToolNames, "\x1f")]++
		}
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	best, count := "", 0
	for _, key := range keys {
		if counts[key] > count {
			best, count = key, counts[key]
		}
	}
	if best == "" {
		return nil, 0
	}
	return strings.Split(best, "\x1f"), count
}

func skillNameFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "learned_" + hex.EncodeToString(sum[:6])
}

func hashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

type scanner interface{ Scan(dest ...any) error }

func scanOutcome(row scanner) (Outcome, error) {
	var out Outcome
	var success int
	var toolsJSON string
	if err := row.Scan(&out.ID, &out.SourceKind, &out.SourceID, &out.PatternKey, &success,
		&out.VerifiedBy, &out.EvidenceRef, &toolsJSON, &out.CreatedAt); err != nil {
		return Outcome{}, err
	}
	out.Success = success != 0
	if err := json.Unmarshal([]byte(toolsJSON), &out.ToolNames); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

func (s *Store) GetDraft(ctx context.Context, id string) (Draft, error) {
	return scanDraft(s.db.QueryRowContext(ctx, draftSelect+` WHERE id=?`, id))
}

func (s *Store) draftByPattern(ctx context.Context, key string) (Draft, error) {
	return scanDraft(s.db.QueryRowContext(ctx, draftSelect+` WHERE pattern_key=?`, key))
}

const draftSelect = `SELECT id,pattern_key,target_skill,body,body_sha256,status,evidence_json,
 success_count,total_count,created_at,updated_at,reviewed_by,reviewed_at,applied_at FROM drafts`

func scanDraft(row scanner) (Draft, error) {
	var d Draft
	var evidenceJSON string
	err := row.Scan(&d.ID, &d.PatternKey, &d.TargetSkill, &d.Body, &d.BodySHA256, &d.Status,
		&evidenceJSON, &d.SuccessCount, &d.TotalCount, &d.CreatedAt, &d.UpdatedAt,
		&d.ReviewedBy, &d.ReviewedAt, &d.AppliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &d.Evidence); err != nil {
		return Draft{}, err
	}
	return d, nil
}

func (s *Store) ListDrafts(ctx context.Context, limit int) ([]Draft, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("limit deve estar entre 1 e 200")
	}
	rows, err := s.db.QueryContext(ctx, draftSelect+` ORDER BY updated_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drafts []Draft
	for rows.Next() {
		draft, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

// Review records a separate approval or rejection. A revised body is accepted
// only from this explicit review step, never from observed outcome content.
func (s *Store) Review(ctx context.Context, id, decision, reviewer, revisedBody string) (Draft, error) {
	if !simpleMetadata(reviewer, 120) {
		return Draft{}, errors.New("reviewer é obrigatório")
	}
	if decision != "approve" && decision != "discard" {
		return Draft{}, errors.New("decision deve ser approve ou discard")
	}
	draft, err := s.GetDraft(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if draft.Status != Candidate && !(draft.Status == Approved && decision == "discard") {
		if decision == "approve" && draft.Status == Approved && revisedBody == "" && draft.ReviewedBy == reviewer {
			return draft, nil
		}
		if decision == "discard" && draft.Status == Discarded && draft.ReviewedBy == reviewer {
			return draft, nil
		}
		return Draft{}, ErrStateConflict
	}
	status := Approved
	body := draft.Body
	if decision == "discard" {
		status = Discarded
	} else if revisedBody != "" {
		body = revisedBody
	}
	if status == Approved {
		if err := validateSkillBody(body, draft.TargetSkill); err != nil {
			return Draft{}, err
		}
	}
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `UPDATE drafts SET body=?,body_sha256=?,status=?,reviewed_by=?,
 reviewed_at=?,updated_at=? WHERE id=? AND status=?`, body, hashBody(body), status, reviewer,
		now, now, id, draft.Status)
	if err != nil {
		return Draft{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Draft{}, err
	} else if n != 1 {
		return Draft{}, ErrStateConflict
	}
	return s.GetDraft(ctx, id)
}

// AppliedSkillNames lets the runtime restore agent visibility after restart.
func (s *Store) AppliedSkillNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT target_skill,body_sha256 FROM drafts WHERE status='applied' ORDER BY target_skill`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	var invalid []error
	for rows.Next() {
		var name, expectedHash string
		if err := rows.Scan(&name, &expectedHash); err != nil {
			return nil, err
		}
		body, err := os.ReadFile(filepath.Join(s.skillsDir, name, "SKILL.md"))
		if err != nil {
			invalid = append(invalid, fmt.Errorf("skill aplicada %q indisponível: %w", name, err))
			continue
		}
		if hashBody(string(body)) != expectedHash {
			invalid = append(invalid, fmt.Errorf("skill aplicada %q diverge do corpo aprovado", name))
			continue
		}
		names = append(names, name)
	}
	return names, errors.Join(rows.Err(), errors.Join(invalid...))
}

// ValidateAppliedSkillFile prevents a filesystem or plugin reload from
// replacing an applied skill with bytes other than the reviewed SKILL.md.
// Unrelated skills remain free to reload normally.
func (s *Store) ValidateAppliedSkillFile(ctx context.Context, path, name string, content []byte) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	pathName := ""
	if rel, err := filepath.Rel(s.skillsDir, absPath); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		pathName = strings.Split(rel, string(filepath.Separator))[0]
	}
	names := []string{name}
	if pathName != "" && pathName != name {
		names = append(names, pathName)
	}
	validated := false
	for _, candidate := range names {
		var expectedHash string
		err := s.db.QueryRowContext(ctx, `SELECT body_sha256 FROM drafts WHERE status='applied' AND target_skill=?`, candidate).Scan(&expectedHash)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if name != candidate || filepath.Clean(absPath) != s.skillPath(candidate) || hashBody(string(content)) != expectedHash {
			return false, fmt.Errorf("skill aplicada %q diverge do corpo aprovado", candidate)
		}
		info, err := os.Lstat(absPath)
		if err != nil || !info.Mode().IsRegular() {
			return false, fmt.Errorf("skill aplicada %q não é arquivo regular: %v", candidate, err)
		}
		validated = true
	}
	return validated, nil
}

// ProtectAppliedSkillName is a registration guard: externally loaded skills
// cannot take the name of a reviewed, applied skill.
func (s *Store) ProtectAppliedSkillName(ctx context.Context, name string) error {
	var applied int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM drafts WHERE status='applied' AND target_skill=?`, name).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("skill aplicada %q exige arquivo revisado", name)
}
