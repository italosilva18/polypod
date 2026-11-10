package goals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// StartAttempt records the exact action before an agent executes it. A second
// attempt cannot begin while the previous one is running or awaiting review.
func (s *Store) StartAttempt(goalID, action string) (Goal, error) {
	var goal Goal
	err := withVerificationLock(s.path, func() error {
		var startErr error
		goal, startErr = s.startAttempt(goalID, action)
		return startErr
	})
	return goal, err
}

func (s *Store) startAttempt(goalID, action string) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != goalID {
			continue
		}
		if g.Status != Pending && g.Status != Active {
			return Goal{}, fmt.Errorf("meta %s nao esta ativa", goalID)
		}
		if strings.TrimSpace(action) == "" {
			action = g.NextAction
		}
		action = strings.TrimSpace(action)
		if action == "" {
			return Goal{}, errors.New("defina uma acao antes de iniciar a tentativa")
		}
		if len(action) > 4096 {
			return Goal{}, errors.New("acao da tentativa excede 4096 bytes")
		}
		if reasons := s.reviewLocked().Waiting[goalID]; len(reasons) > 0 {
			return Goal{}, fmt.Errorf("meta ainda nao pronta: %s", strings.Join(reasons, "; "))
		}
		old := g.Status
		attemptID := "attempt_" + uuid.NewString()
		if g.Verification != nil {
			if s.verifier == nil {
				return Goal{}, errors.New("verificador de metas indisponivel")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			planned, err := s.verifier.BeginVerificationRun(ctx, "goal", g.ID, attemptID)
			cancel()
			if err != nil {
				return Goal{}, fmt.Errorf("capturando estado anterior a tentativa: %w", err)
			}
			if !planned {
				return Goal{}, errors.New("plano de verificacao da meta indisponivel")
			}
		} else if s.verifier != nil {
			// A plan might have been stored externally before a failed goals.json
			// commit. Do not let that orphaned plan become an unverified run.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			planned, err := s.verifier.HasVerificationPlan(ctx, "goal", g.ID)
			cancel()
			if err != nil {
				return Goal{}, fmt.Errorf("conferindo plano de verificacao da meta: %w", err)
			}
			if planned {
				return Goal{}, errors.New("plano de verificacao externo pendente; registre-o novamente antes da tentativa")
			}
		}
		now := time.Now().UTC()
		if g.Verification != nil && now.UnixMilli() <= g.Verification.CreatedAt {
			wait := time.Until(time.UnixMilli(g.Verification.CreatedAt + 1))
			if wait > 0 {
				time.Sleep(wait)
			}
			now = time.Now().UTC()
		}
		g.Status = Active
		g.UpdatedAt = now
		g.Attempts = append(g.Attempts, Attempt{
			ID: attemptID, Action: action,
			Status: AttemptRunning, StartedAt: now,
		})
		g.History = append(g.History, Transition{At: now, From: old, To: Active, Note: "Tentativa iniciada"})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, goalID)
}

// FinishAttempt records an outcome without treating a model's report as proof
// that the goal is complete. Completion remains a separate audited transition.
func (s *Store) FinishAttempt(goalID, attemptID string, status AttemptStatus, result, evidence string,
	nextAction *string) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result = strings.TrimSpace(result)
	evidence = strings.TrimSpace(evidence)
	if result == "" || len(result) > 8192 || len(evidence) > 8192 {
		return Goal{}, errors.New("resultado obrigatorio; resultado/evidencia limitados a 8192 bytes")
	}
	if status != AttemptReportedSuccess && status != AttemptFailed && status != AttemptInterrupted {
		return Goal{}, fmt.Errorf("estado final da tentativa invalido: %s", status)
	}
	if status == AttemptReportedSuccess && evidence == "" {
		return Goal{}, errors.New("sucesso declarado exige evidencia para revisao")
	}
	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != goalID {
			continue
		}
		if len(g.Attempts) == 0 || g.Attempts[len(g.Attempts)-1].ID != attemptID {
			return Goal{}, fmt.Errorf("tentativa ativa nao encontrada para a meta %s", goalID)
		}
		attempt := &g.Attempts[len(g.Attempts)-1]
		if attempt.Status != AttemptRunning {
			return Goal{}, fmt.Errorf("tentativa %s ja encerrada", attemptID)
		}
		now := time.Now().UTC()
		if now.Before(attempt.StartedAt) {
			now = attempt.StartedAt
		}
		attempt.Status = status
		attempt.FinishedAt = &now
		attempt.Result = result
		attempt.Evidence = evidence
		if nextAction != nil {
			if len(*nextAction) > 4096 {
				return Goal{}, errors.New("proxima acao excede 4096 bytes")
			}
			g.NextAction = strings.TrimSpace(*nextAction)
		}
		g.UpdatedAt = now
		g.History = append(g.History, Transition{
			At: now, From: g.Status, To: g.Status,
			Note: fmt.Sprintf("Tentativa %s encerrada: %s", attemptID, status),
		})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, goalID)
}

// ReviewAttempt records a separate assessment of the previous effect. It
// never executes a verifier itself: the note must name the evidence inspected.
func (s *Store) ReviewAttempt(goalID, attemptID, decision, note string, nextAction *string) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	note = strings.TrimSpace(note)
	if decision != "accepted" && decision != "rejected" {
		return Goal{}, errors.New("revisao deve ser accepted ou rejected")
	}
	if note == "" || len(note) > 8192 {
		return Goal{}, errors.New("revisao exige evidencia ou motivo de ate 8192 bytes")
	}
	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != goalID {
			continue
		}
		if len(g.Attempts) == 0 || g.Attempts[len(g.Attempts)-1].ID != attemptID {
			return Goal{}, fmt.Errorf("ultima tentativa da meta %s nao encontrada", goalID)
		}
		attempt := &g.Attempts[len(g.Attempts)-1]
		if attempt.Status == AttemptRunning || attempt.Review != "" {
			return Goal{}, errors.New("apenas a ultima tentativa encerrada sem revisao pode ser revisada")
		}
		if g.Verification != nil && decision == "accepted" &&
			(attempt.Verification == nil || !attempt.Verification.Success) {
			return Goal{}, errors.New("revisao aceita exige verificacao objetiva positiva da tentativa")
		}
		if nextAction != nil {
			if len(*nextAction) > 4096 {
				return Goal{}, errors.New("proxima acao excede 4096 bytes")
			}
			g.NextAction = strings.TrimSpace(*nextAction)
		}
		now := time.Now().UTC()
		if attempt.FinishedAt != nil && now.Before(*attempt.FinishedAt) {
			now = *attempt.FinishedAt
		}
		attempt.Review = decision
		attempt.ReviewNote = note
		attempt.ReviewedAt = &now
		g.UpdatedAt = now
		g.History = append(g.History, Transition{
			At: now, From: g.Status, To: g.Status,
			Note: fmt.Sprintf("Tentativa %s revisada: %s", attemptID, decision),
		})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, goalID)
}

func validateAttempts(g Goal) error {
	seen := make(map[string]bool, len(g.Attempts))
	for i, attempt := range g.Attempts {
		if attempt.ID == "" || strings.TrimSpace(attempt.Action) == "" || attempt.StartedAt.IsZero() {
			return errors.New("tentativa exige ID, acao e inicio")
		}
		if seen[attempt.ID] {
			return errors.New("ID de tentativa duplicado")
		}
		seen[attempt.ID] = true
		if attempt.Verification != nil {
			proof := attempt.Verification
			if g.Verification == nil || attempt.Status == AttemptRunning || proof.OutcomeID == "" ||
				proof.VerifiedBy == "" || proof.EvidenceRef == "" || proof.ObservedAt.IsZero() {
				return errors.New("verificacao de tentativa inconsistente")
			}
		}
		switch attempt.Status {
		case AttemptRunning:
			if attempt.FinishedAt != nil || i != len(g.Attempts)-1 || g.Status != Active ||
				attempt.Review != "" || attempt.ReviewedAt != nil {
				return errors.New("tentativa em andamento inconsistente")
			}
		case AttemptReportedSuccess, AttemptFailed, AttemptInterrupted:
			if attempt.FinishedAt == nil || attempt.FinishedAt.Before(attempt.StartedAt) ||
				strings.TrimSpace(attempt.Result) == "" {
				return errors.New("tentativa encerrada sem horario ou resultado")
			}
			if attempt.Status == AttemptReportedSuccess && strings.TrimSpace(attempt.Evidence) == "" {
				return errors.New("sucesso declarado sem evidencia")
			}
			switch attempt.Review {
			case "":
				if attempt.ReviewedAt != nil || attempt.ReviewNote != "" {
					return errors.New("revisao incompleta")
				}
			case "accepted", "rejected":
				if attempt.ReviewedAt == nil || strings.TrimSpace(attempt.ReviewNote) == "" {
					return errors.New("revisao sem horario ou evidencia")
				}
				if g.Verification != nil && attempt.Review == "accepted" &&
					(attempt.Verification == nil || !attempt.Verification.Success) {
					return errors.New("revisao aceita sem verificacao objetiva positiva")
				}
			default:
				return fmt.Errorf("decisao de revisao invalida: %s", attempt.Review)
			}
		default:
			return fmt.Errorf("estado de tentativa invalido: %s", attempt.Status)
		}
	}
	if g.Status == Completed && len(g.Attempts) > 0 {
		latest := g.Attempts[len(g.Attempts)-1]
		if latest.Review != "accepted" {
			return errors.New("conclusao exige ultima tentativa revisada e aceita")
		}
	}
	return nil
}
