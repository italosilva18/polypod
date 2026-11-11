package goals

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/costa/polypod/internal/evolution"
)

// The plan and goals.json live in different stores. This lock serializes plan
// registration, attempt start, and completion across local processes.
func withVerificationLock(path string, action func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("criando diretorio de metas: %w", err)
	}
	return withGoalFileLock(path+".verification", action)
}

// RegisterVerification attaches an immutable, independently observed check
// before the first attempt. A partially published plan in evolution.db can be
// attached by repeating the same request after reloading goals.json.
func (s *Store) RegisterVerification(ctx context.Context, goalID string,
	input evolution.VerificationPlanInput) (Goal, error) {
	var goal Goal
	err := withVerificationLock(s.path, func() error {
		var registerErr error
		goal, registerErr = s.registerVerification(ctx, goalID, input)
		return registerErr
	})
	return goal, err
}

func (s *Store) registerVerification(ctx context.Context, goalID string,
	input evolution.VerificationPlanInput) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verifier == nil {
		return Goal{}, errors.New("verificador de metas indisponivel")
	}
	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != goalID {
			continue
		}
		if g.Status != Pending && g.Status != Active || len(g.Attempts) != 0 {
			return Goal{}, errors.New("configure a verificacao antes da primeira tentativa")
		}
		input.SourceKind = "goal"
		input.SubjectID = g.ID
		if input.PatternKey == "" {
			input.PatternKey = g.Title
		}
		plan, err := s.verifier.RegisterVerificationPlan(ctx, input)
		if err != nil {
			return Goal{}, fmt.Errorf("registrando verificacao da meta: %w", err)
		}
		if g.Verification != nil {
			if *g.Verification != plan {
				return Goal{}, errors.New("meta ja possui outra verificacao")
			}
			return clone([]Goal{*g})[0], nil
		}
		now := time.Now().UTC()
		g.Verification = &plan
		g.UpdatedAt = now
		g.History = append(g.History, Transition{
			At: now, From: g.Status, To: g.Status,
			Note: "Plano de verificacao objetiva registrado",
		})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, goalID)
}

// VerifyAttempt observes the planned postcondition after an attempt has been
// recorded as finished. It records the independent outcome before a review can
// accept a successful effect. Repeated calls retain the original observation.
func (s *Store) VerifyAttempt(ctx context.Context, goalID, attemptID string) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verifier == nil {
		return Goal{}, errors.New("verificador de metas indisponivel")
	}
	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != goalID {
			continue
		}
		if g.Verification == nil {
			return Goal{}, errors.New("meta sem plano de verificacao objetiva")
		}
		if len(g.Attempts) == 0 || g.Attempts[len(g.Attempts)-1].ID != attemptID {
			return Goal{}, errors.New("apenas a ultima tentativa da meta pode ser verificada")
		}
		attempt := &g.Attempts[len(g.Attempts)-1]
		if attempt.Status == AttemptRunning {
			return Goal{}, errors.New("encerre a tentativa antes de verificar seu efeito")
		}
		if attempt.Verification != nil {
			return clone([]Goal{*g})[0], nil
		}
		storedPlan, err := s.verifier.GetVerificationPlan(ctx, "goal", g.ID)
		if err != nil {
			return Goal{}, fmt.Errorf("carregando plano de verificacao: %w", err)
		}
		if storedPlan != *g.Verification {
			return Goal{}, errors.New("plano de verificacao difere do registro da meta")
		}
		if attempt.Status != AttemptReportedSuccess {
			return Goal{}, errors.New("falha ou interrupcao deve receber revisao negativa, sem verificacao de sucesso")
		}
		outcome, recorded, err := s.verifier.VerifyAndRecordAt(ctx, "goal", g.ID, attempt.ID, nil, attempt.StartedAt)
		if err != nil {
			return Goal{}, fmt.Errorf("registrando resultado verificado: %w", err)
		}
		if !recorded {
			// Older attempts that closed without a final snapshot cannot be
			// credited from a later observation. Reject the review and start
			// another attempt to establish a fresh evidence boundary.
			return Goal{}, errors.New("tentativa sem prova objetiva valida no encerramento; revise como rejeitada e inicie outra tentativa")
		}
		now := time.Now().UTC()
		attempt.Verification = &AttemptVerification{
			OutcomeID: outcome.ID, Success: outcome.Success, VerifiedBy: outcome.VerifiedBy,
			EvidenceRef: outcome.EvidenceRef, ObservedAt: now,
		}
		g.UpdatedAt = now
		g.History = append(g.History, Transition{
			At: now, From: g.Status, To: g.Status,
			Note: fmt.Sprintf("Tentativa %s verificada: sucesso=%t", attemptID, outcome.Success),
		})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, goalID)
}
