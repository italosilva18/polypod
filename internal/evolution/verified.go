package evolution

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	FileSHA256Check      = "file_sha256"
	HTTPStatusCheck      = "http_status"
	HTTPJSONCheck        = "http_json"
	SQLiteScalarCheck    = "sqlite_scalar"
	missingVerifiedFile  = "missing"
	maxVerifiedFileBytes = 1 << 30
	verificationSettle   = 150 * time.Millisecond
)

var ErrVerificationUnstable = errors.New("alvo mudou durante a verificação")
var ErrVerificationStartUnknown = errors.New("verificação exige início durável da execução")

// RegisterVerificationPlan stores an immutable check for a task or routine.
// The plan is registered when work is created, before the work can satisfy it.
// No agent narrative or exit status can satisfy a plan; only a later state read
// can do so. A matching preexisting state is rejected to avoid learning from a
// task that did not produce the expected state transition.
func (s *Store) RegisterVerificationPlan(ctx context.Context, input VerificationPlanInput) (VerificationPlan, error) {
	input, err := normalizeVerificationPlan(input)
	if err != nil {
		return VerificationPlan{}, err
	}
	if existing, err := s.verificationPlan(ctx, input.SourceKind, input.SubjectID); err == nil {
		return sameVerificationPlan(existing, input)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return VerificationPlan{}, err
	}
	baseline, err := verificationState(ctx, input)
	if err != nil {
		return VerificationPlan{}, fmt.Errorf("ler estado inicial da verificação: %w", err)
	}
	if baseline == input.ExpectedSHA256 {
		return VerificationPlan{}, errors.New("alvo já possui o estado esperado antes da tarefa")
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO verification_plans
	 (source_kind,subject_id,pattern_key,kind,path,selector,expected_status,expected_sha256,baseline_sha256,created_at)
	 VALUES(?,?,?,?,?,?,?,?,?,?)`, input.SourceKind, input.SubjectID, input.PatternKey,
		input.Kind, input.Path, input.Selector, input.ExpectedStatus, input.ExpectedSHA256, baseline, time.Now().UnixMilli())
	if err != nil {
		return VerificationPlan{}, fmt.Errorf("registrar verificação: %w", err)
	}
	stored, err := s.verificationPlan(ctx, input.SourceKind, input.SubjectID)
	if err != nil {
		return VerificationPlan{}, err
	}
	return sameVerificationPlan(stored, input)
}

func normalizeVerificationPlan(input VerificationPlanInput) (VerificationPlanInput, error) {
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SubjectID = strings.TrimSpace(input.SubjectID)
	input.PatternKey = normalizePattern(input.PatternKey)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Kind == "" {
		input.Kind = FileSHA256Check
	}
	input.Path = strings.TrimSpace(input.Path)
	input.Selector = strings.TrimSpace(input.Selector)
	input.ExpectedSHA256 = strings.ToLower(strings.TrimSpace(input.ExpectedSHA256))
	if input.SourceKind != "task" && input.SourceKind != "routine" {
		return input, errors.New("verificação exige fonte task ou routine")
	}
	if !evidenceTokenPattern.MatchString(input.SubjectID) || len(input.SubjectID) > 160 ||
		input.PatternKey == "" || len(input.PatternKey) > 160 {
		return input, errors.New("identificador e padrão de verificação inválidos")
	}
	switch input.Kind {
	case FileSHA256Check:
		input.Path = filepath.Clean(input.Path)
		if !validVerificationPath(input.Path) || input.Selector != "" || input.ExpectedStatus != 0 || input.ExpectedJSON != "" {
			return input, errors.New("file_sha256 exige caminho absoluto, sem seletor ou valor JSON")
		}
		decoded, err := hex.DecodeString(input.ExpectedSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return input, errors.New("hash SHA-256 esperado inválido")
		}
	case HTTPStatusCheck, HTTPJSONCheck:
		url, err := validateVerificationURL(input.Path)
		if err != nil {
			return input, err
		}
		input.Path = url
		if input.ExpectedStatus < 100 || input.ExpectedStatus > 599 || input.ExpectedSHA256 != "" {
			return input, errors.New("HTTP exige expected_status entre 100 e 599, sem expected_sha256")
		}
		if input.Kind == HTTPStatusCheck {
			if input.Selector != "" || input.ExpectedJSON != "" {
				return input, errors.New("http_status não aceita seletor ou valor JSON")
			}
			input.ExpectedSHA256 = stateDigest(HTTPStatusCheck, input.ExpectedStatus, nil)
		} else {
			if err := validateJSONPointer(input.Selector); err != nil {
				return input, err
			}
			value, err := canonicalExpectedJSON(input.ExpectedJSON)
			if err != nil {
				return input, err
			}
			input.ExpectedSHA256 = stateDigest(HTTPJSONCheck, input.ExpectedStatus, value)
			input.ExpectedJSON = ""
		}
	case SQLiteScalarCheck:
		input.Path = filepath.Clean(input.Path)
		if !validVerificationPath(input.Path) || input.ExpectedStatus != 0 || input.ExpectedSHA256 != "" {
			return input, errors.New("sqlite_scalar exige caminho absoluto, sem status ou expected_sha256")
		}
		if err := validateScalarQuery(input.Selector); err != nil {
			return input, err
		}
		value, err := canonicalExpectedJSON(input.ExpectedJSON)
		if err != nil {
			return input, err
		}
		if !isJSONScalar(value) {
			return input, errors.New("sqlite_scalar exige expected_json escalar")
		}
		input.ExpectedSHA256 = stateDigest(SQLiteScalarCheck, 0, value)
		input.ExpectedJSON = ""
	default:
		return input, errors.New("tipo de verificação desconhecido")
	}
	return input, nil
}

func validVerificationPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 4096 && !strings.ContainsRune(path, '\x00')
}

func sameVerificationPlan(stored VerificationPlan, input VerificationPlanInput) (VerificationPlan, error) {
	if stored.VerificationPlanInput != input {
		return VerificationPlan{}, fmt.Errorf("plano %s/%s já registrado com outra verificação: %w",
			input.SourceKind, input.SubjectID, ErrStateConflict)
	}
	return stored, nil
}

func (s *Store) verificationPlan(ctx context.Context, kind, subjectID string) (VerificationPlan, error) {
	var plan VerificationPlan
	err := s.db.QueryRowContext(ctx, `SELECT source_kind,subject_id,pattern_key,kind,path,selector,
	 expected_status,expected_sha256,baseline_sha256,created_at FROM verification_plans
	 WHERE source_kind=? AND subject_id=?`, kind, subjectID).Scan(
		&plan.SourceKind, &plan.SubjectID, &plan.PatternKey, &plan.Kind, &plan.Path, &plan.Selector,
		&plan.ExpectedStatus, &plan.ExpectedSHA256, &plan.BaselineSHA256, &plan.CreatedAt)
	return plan, err
}

// GetVerificationPlan returns a registered check for diagnostics and callers
// that need to confirm the plan exists before publishing work.
func (s *Store) GetVerificationPlan(ctx context.Context, sourceKind, subjectID string) (VerificationPlan, error) {
	plan, err := s.verificationPlan(ctx, sourceKind, subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return VerificationPlan{}, ErrNotFound
	}
	return plan, err
}

// HasVerificationPlan avoids a history lookup for work that does not have an
// independent verifier.
func (s *Store) HasVerificationPlan(ctx context.Context, sourceKind, subjectID string) (bool, error) {
	_, err := s.verificationPlan(ctx, strings.TrimSpace(sourceKind), strings.TrimSpace(subjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

type verificationRun struct {
	subjectID      string
	baselineSHA256 string
	finalSHA256    sql.NullString
	finishedAt     int64
}

// BeginVerificationRun captures a durable baseline before this run invokes
// any tools. Repeated calls for the same run keep the original baseline, even
// when a routine retries after partially producing its artifact.
func (s *Store) BeginVerificationRun(ctx context.Context, sourceKind, subjectID, runID string) (bool, error) {
	sourceKind, subjectID, runID = strings.TrimSpace(sourceKind), strings.TrimSpace(subjectID), strings.TrimSpace(runID)
	if sourceKind != "task" && sourceKind != "routine" ||
		!evidenceTokenPattern.MatchString(subjectID) || len(subjectID) > 160 ||
		!evidenceTokenPattern.MatchString(runID) || len(runID) > 160 {
		return false, errors.New("identificadores da execução de verificação inválidos")
	}
	plan, err := s.verificationPlan(ctx, sourceKind, subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing, err := s.verificationRun(ctx, sourceKind, runID); err == nil {
		if existing.subjectID != subjectID {
			return false, fmt.Errorf("execução %s/%s pertence a outra tarefa: %w", sourceKind, runID, ErrStateConflict)
		}
		return true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	baseline, err := verificationState(ctx, plan.VerificationPlanInput)
	if err != nil {
		return false, fmt.Errorf("ler estado inicial da execução: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO verification_runs
	 (source_kind,run_id,subject_id,baseline_sha256,created_at) VALUES(?,?,?,?,?)`,
		sourceKind, runID, subjectID, baseline, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	stored, err := s.verificationRun(ctx, sourceKind, runID)
	if err != nil {
		return false, err
	}
	if stored.subjectID != subjectID || stored.baselineSHA256 != baseline {
		return false, fmt.Errorf("estado inicial da execução %s/%s mudou: %w", sourceKind, runID, ErrStateConflict)
	}
	return true, nil
}

func (s *Store) verificationRun(ctx context.Context, sourceKind, runID string) (verificationRun, error) {
	var run verificationRun
	err := s.db.QueryRowContext(ctx, `SELECT subject_id,baseline_sha256,final_sha256,finished_at FROM verification_runs
	 WHERE source_kind=? AND run_id=?`, sourceKind, runID).Scan(
		&run.subjectID, &run.baselineSHA256, &run.finalSHA256, &run.finishedAt)
	return run, err
}

// FinishVerificationRun captures the postcondition immediately after the
// terminal handler returns. The delayed notice callback uses only this durable
// snapshot. Before an outcome is recorded, conflicting final reads make the
// run inconclusive. After an outcome exists, repeated calls preserve it.
func (s *Store) FinishVerificationRun(ctx context.Context, sourceKind, subjectID, runID string) (bool, error) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	sourceKind, subjectID, runID = strings.TrimSpace(sourceKind), strings.TrimSpace(subjectID), strings.TrimSpace(runID)
	if sourceKind != "task" && sourceKind != "routine" ||
		!evidenceTokenPattern.MatchString(subjectID) || len(subjectID) > 160 ||
		!evidenceTokenPattern.MatchString(runID) || len(runID) > 160 {
		return false, errors.New("identificadores do fim da verificação inválidos")
	}
	plan, err := s.verificationPlan(ctx, sourceKind, subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	run, err := s.verificationRun(ctx, sourceKind, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.subjectID != subjectID {
		return false, fmt.Errorf("execução %s/%s pertence a outra tarefa: %w", sourceKind, runID, ErrStateConflict)
	}
	// Once an outcome exists, delivery retries cannot change its recorded proof.
	if existing, err := s.outcomeBySource(ctx, sourceKind, runID); err == nil {
		if existing.VerifiedBy != verifierName(plan.Kind) || existing.PatternKey != plan.PatternKey {
			return false, ErrStateConflict
		}
		return true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if run.finishedAt > 0 && !run.finalSHA256.Valid {
		return false, nil
	}
	actual, err := verificationState(ctx, plan.VerificationPlanInput)
	if err != nil {
		return false, fmt.Errorf("capturar estado final da execução: %w", err)
	}
	if actual != plan.ExpectedSHA256 {
		// A missing or mismatched target is evidence of failure only if the
		// state remains the same after a short settling window.
		timer := time.NewTimer(verificationSettle)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timer.C:
		}
		confirmed, err := verificationState(ctx, plan.VerificationPlanInput)
		if err != nil {
			return false, fmt.Errorf("confirmar estado final da execução: %w", err)
		}
		if confirmed != actual {
			return false, ErrVerificationUnstable
		}
	}
	if run.finishedAt == 0 {
		_, err = s.db.ExecContext(ctx, `UPDATE verification_runs SET final_sha256=?,finished_at=?
		 WHERE source_kind=? AND run_id=? AND subject_id=? AND finished_at=0`,
			actual, time.Now().UnixMilli(), sourceKind, runID, subjectID)
		if err != nil {
			return false, err
		}
		run, err = s.verificationRun(ctx, sourceKind, runID)
		if err != nil {
			return false, err
		}
	}
	if run.finalSHA256.Valid && run.finalSHA256.String == actual {
		return true, nil
	}
	if run.finalSHA256.Valid {
		if _, err := s.db.ExecContext(ctx, `UPDATE verification_runs SET final_sha256=NULL,finished_at=?
		 WHERE source_kind=? AND run_id=? AND subject_id=? AND final_sha256 IS NOT NULL`,
			time.Now().UnixMilli(), sourceKind, runID, subjectID); err != nil {
			return false, err
		}
	}
	return false, nil
}

// VerifyAndRecord is retained for compatibility but cannot verify a run without
// its durable start time. Use VerifyAndRecordAt instead.
func (s *Store) VerifyAndRecord(ctx context.Context, sourceKind, subjectID, runID string,
	observedTools []string) (Outcome, bool, error) {
	return Outcome{}, false, ErrVerificationStartUnknown
}

// VerifyAndRecordAt checks an immutable plan after one durable run. The caller
// supplies tool names only if they were observed by the tool execution path;
// an empty trace is recorded for outcome metrics but cannot generate a skill
// draft. Repeated notices return the original outcome even if the target later
// changes, so delivery retries never rewrite learning history.
// startedAt must come from the task/routine's persisted attempt history, never
// the callback time or a timestamp supplied by the agent.
func (s *Store) VerifyAndRecordAt(ctx context.Context, sourceKind, subjectID, runID string,
	observedTools []string, startedAt time.Time) (Outcome, bool, error) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	sourceKind, subjectID, runID = strings.TrimSpace(sourceKind), strings.TrimSpace(subjectID), strings.TrimSpace(runID)
	if !evidenceTokenPattern.MatchString(runID) || len(runID) > 160 {
		return Outcome{}, false, errors.New("ID da execução para verificação inválido")
	}
	plan, err := s.verificationPlan(ctx, sourceKind, subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	// A plan registered after a task began could incorrectly credit effects
	// that already happened. Millisecond timestamps in older queue histories
	// cannot order events within one millisecond, so equality is inconclusive.
	if startedAt.IsZero() || plan.CreatedAt >= startedAt.UnixMilli() {
		return Outcome{}, false, nil
	}
	run, err := s.verificationRun(ctx, sourceKind, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	if run.subjectID != subjectID {
		return Outcome{}, false, fmt.Errorf("execução %s/%s pertence a outra tarefa: %w", sourceKind, runID, ErrStateConflict)
	}
	if existing, err := s.outcomeBySource(ctx, sourceKind, runID); err == nil {
		if existing.VerifiedBy != verifierName(plan.Kind) || existing.PatternKey != plan.PatternKey {
			return Outcome{}, false, fmt.Errorf("execução %s/%s já tem resultado de outra origem: %w",
				sourceKind, runID, ErrStateConflict)
		}
		return existing, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, err
	}
	if run.baselineSHA256 == plan.ExpectedSHA256 {
		return Outcome{}, false, nil
	}
	if !run.finalSHA256.Valid || run.finishedAt == 0 {
		return Outcome{}, false, nil
	}
	actual := run.finalSHA256.String
	success := actual == plan.ExpectedSHA256
	observedTools, err = s.observedOrStoredTools(ctx, sourceKind, runID, observedTools)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("carregar trace da execução: %w", err)
	}
	if len(observedTools) > maxObservedTools {
		observedTools = observedTools[:maxObservedTools]
	}
	tools := append([]string(nil), observedTools...)
	if tools == nil {
		tools = []string{}
	}
	out, err := s.recordOutcome(ctx, OutcomeInput{
		SourceKind: sourceKind, SourceID: runID, PatternKey: plan.PatternKey,
		Success: success, VerifiedBy: verifierName(plan.Kind), EvidenceRef: plan.Kind + ":" + actual,
		ToolNames: tools,
	})
	if err != nil {
		return Outcome{}, false, err
	}
	return out, true, nil
}

func fileStateSHA256(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return missingVerifiedFile, nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxVerifiedFileBytes {
		return "", errors.New("alvo da verificação não é arquivo regular permitido")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > maxVerifiedFileBytes || !os.SameFile(info, opened) {
		return "", ErrVerificationUnstable
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := hash.Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() ||
		!after.ModTime().Equal(opened.ModTime()) {
		return "", ErrVerificationUnstable
	}
	// The descriptor can still refer to the original inode after the path has
	// been replaced. Recheck the pathname itself before accepting the hash.
	latest, err := os.Lstat(path)
	if err != nil || !latest.Mode().IsRegular() || !os.SameFile(opened, latest) ||
		latest.Size() != opened.Size() || !latest.ModTime().Equal(opened.ModTime()) {
		return "", ErrVerificationUnstable
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
