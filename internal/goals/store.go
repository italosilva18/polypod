// Package goals stores operational plans. It never executes work or schedules jobs.
package goals

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/costa/polypod/internal/evolution"
)

type Status string

const (
	Pending   Status = "pending"
	Active    Status = "active"
	Blocked   Status = "blocked"
	Paused    Status = "paused"
	Completed Status = "completed"
	Cancelled Status = "cancelled"
)

var ErrNotFound = errors.New("meta nao encontrada")
var ErrStaleStore = errors.New("metas alteradas por outro processo; recarregue a instancia")

type Transition struct {
	At   time.Time `json:"at"`
	From Status    `json:"from,omitempty"`
	To   Status    `json:"to"`
	Note string    `json:"note"`
}

// AttemptStatus distinguishes an observed outcome from a verified goal.
// Reported success still requires a separate goal completion update.
type AttemptStatus string

const (
	AttemptRunning         AttemptStatus = "running"
	AttemptReportedSuccess AttemptStatus = "reported_success"
	AttemptFailed          AttemptStatus = "failed"
	AttemptInterrupted     AttemptStatus = "interrupted"
)

type Attempt struct {
	ID           string               `json:"id"`
	Action       string               `json:"action"`
	Status       AttemptStatus        `json:"status"`
	StartedAt    time.Time            `json:"started_at"`
	FinishedAt   *time.Time           `json:"finished_at,omitempty"`
	Result       string               `json:"result,omitempty"`
	Evidence     string               `json:"evidence,omitempty"`
	Review       string               `json:"review,omitempty"`
	ReviewNote   string               `json:"review_note,omitempty"`
	ReviewedAt   *time.Time           `json:"reviewed_at,omitempty"`
	Verification *AttemptVerification `json:"verification,omitempty"`
}

// AttemptVerification is a durable observation produced by the independent
// verifier, not a decision or evidence string supplied by the agent.
type AttemptVerification struct {
	OutcomeID   string    `json:"outcome_id"`
	Success     bool      `json:"success"`
	VerifiedBy  string    `json:"verified_by"`
	EvidenceRef string    `json:"evidence_ref"`
	ObservedAt  time.Time `json:"observed_at"`
}

type Goal struct {
	ID              string                      `json:"id"`
	ParentID        string                      `json:"parent_id,omitempty"`
	Title           string                      `json:"title"`
	SuccessCriteria string                      `json:"success_criteria"`
	Dependencies    []string                    `json:"dependencies"`
	Status          Status                      `json:"status"`
	NextAction      string                      `json:"next_action"`
	Evidence        string                      `json:"evidence,omitempty"`
	CreatedAt       time.Time                   `json:"created_at"`
	UpdatedAt       time.Time                   `json:"updated_at"`
	History         []Transition                `json:"history"`
	Attempts        []Attempt                   `json:"attempts,omitempty"`
	Verification    *evolution.VerificationPlan `json:"verification,omitempty"`
}
type CreateInput struct {
	Title           string
	SuccessCriteria string
	ParentID        string
	Dependencies    []string
	NextAction      string
}
type UpdateInput struct {
	Status       *Status
	NextAction   *string
	Evidence     *string
	Dependencies *[]string
	Note         string
}
type Review struct {
	Goals             []Goal              `json:"goals"`
	Ready             []string            `json:"ready"`
	InFlight          []string            `json:"in_flight"`
	NeedsVerification []string            `json:"needs_verification"`
	Waiting           map[string][]string `json:"waiting"`
}
type diskState struct {
	Version int    `json:"version"`
	Goals   []Goal `json:"goals"`
}

// Store is owned by one process. mu protects all goal state; returned values are copies.
type Store struct {
	mu          sync.Mutex
	path        string
	goals       []Goal
	fingerprint [32]byte
	present     bool
	verifier    *evolution.Store
}

// AttachVerifier enables independent checks for goals with verification plans.
// Call this before registering goal skills or starting a goal attempt.
func (s *Store) AttachVerifier(verifier *evolution.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifier = verifier
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("caminho de metas vazio")
	}
	s := &Store{path: path, goals: []Goal{}}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload replaces the in-memory snapshot with the latest valid disk state.
// Call it after ErrStaleStore before planning another action.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.goals = []Goal{}
		s.fingerprint = [32]byte{}
		s.present = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("lendo metas: %w", err)
	}
	var disk diskState
	if err = json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("decodificando metas: %w", err)
	}
	if disk.Version != 1 {
		return fmt.Errorf("versao de metas desconhecida: %d", disk.Version)
	}
	if err = validate(disk.Goals); err != nil {
		return fmt.Errorf("metas invalidas: %w", err)
	}
	s.goals = clone(disk.Goals)
	s.fingerprint = sha256.Sum256(raw)
	s.present = true
	return nil
}
func (s *Store) Create(in CreateInput) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	g := Goal{ID: "goal_" + uuid.NewString(), ParentID: strings.TrimSpace(in.ParentID), Title: strings.TrimSpace(in.Title), SuccessCriteria: strings.TrimSpace(in.SuccessCriteria), Dependencies: append([]string{}, in.Dependencies...), Status: Pending, NextAction: strings.TrimSpace(in.NextAction), CreatedAt: now, UpdatedAt: now, History: []Transition{{At: now, To: Pending, Note: "Meta criada"}}}
	next := append(clone(s.goals), g)
	if err := s.commit(next); err != nil {
		return Goal{}, err
	}
	return clone([]Goal{g})[0], nil
}
func (s *Store) Update(id string, in UpdateInput) (Goal, error) {
	var goal Goal
	err := withVerificationLock(s.path, func() error {
		var updateErr error
		goal, updateErr = s.update(id, in)
		return updateErr
	})
	return goal, err
}

func (s *Store) update(id string, in UpdateInput) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.goals)
	for i := range next {
		g := &next[i]
		if g.ID != id {
			continue
		}
		old := g.Status
		if in.Status != nil {
			g.Status = *in.Status
		}
		if old != Completed && g.Status == Completed && g.Verification == nil && s.verifier != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			planned, err := s.verifier.HasVerificationPlan(ctx, "goal", g.ID)
			cancel()
			if err != nil {
				return Goal{}, fmt.Errorf("conferindo plano de verificacao da meta: %w", err)
			}
			if planned {
				return Goal{}, errors.New("plano de verificacao externo pendente; registre-o novamente antes de concluir")
			}
		}
		if in.NextAction != nil {
			g.NextAction = strings.TrimSpace(*in.NextAction)
		}
		if in.Evidence != nil {
			g.Evidence = strings.TrimSpace(*in.Evidence)
		}
		if in.Dependencies != nil {
			g.Dependencies = append([]string{}, (*in.Dependencies)...)
		}
		if strings.TrimSpace(in.Note) == "" {
			return Goal{}, errors.New("informe uma nota para o historico da atualizacao")
		}
		g.UpdatedAt = time.Now().UTC()
		g.History = append(g.History, Transition{At: g.UpdatedAt, From: old, To: g.Status, Note: strings.TrimSpace(in.Note)})
		if err := s.commit(next); err != nil {
			return Goal{}, err
		}
		return clone([]Goal{*g})[0], nil
	}
	return Goal{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}
func (s *Store) List() []Goal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.goals)
}

// Review computes readiness without changing state. Restarting does not imply work succeeded.
func (s *Store) Review() Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reviewLocked()
}

func (s *Store) reviewLocked() Review {
	r := Review{Goals: clone(s.goals), Ready: []string{}, InFlight: []string{},
		NeedsVerification: []string{}, Waiting: map[string][]string{}}
	byID := map[string]Goal{}
	for _, g := range s.goals {
		byID[g.ID] = g
	}
	for _, g := range s.goals {
		if g.Status != Pending && g.Status != Active {
			continue
		}
		reasons := []string{}
		for _, id := range g.Dependencies {
			if byID[id].Status != Completed {
				reasons = append(reasons, "dependencia: "+id)
			}
		}
		for p := g.ParentID; p != ""; p = byID[p].ParentID {
			status := byID[p].Status
			if status == Paused || status == Blocked || status == Cancelled || status == Completed {
				reasons = append(reasons, "meta superior indisponivel: "+p)
			}
		}
		for _, child := range s.goals {
			if child.ParentID == g.ID && child.Status != Completed && child.Status != Cancelled {
				reasons = append(reasons, "subtarefa: "+child.ID)
			}
		}
		if g.NextAction == "" {
			reasons = append(reasons, "definir proxima acao")
		}
		if n := len(g.Attempts); n > 0 {
			last := g.Attempts[n-1]
			switch last.Status {
			case AttemptRunning:
				r.InFlight = append(r.InFlight, g.ID)
				if time.Since(last.StartedAt) >= time.Hour {
					r.NeedsVerification = append(r.NeedsVerification, g.ID)
					reasons = append(reasons, "tentativa antiga em andamento; confirmar processo e efeito antes de repetir")
				} else {
					reasons = append(reasons, "tentativa em andamento; verificar efeito antes de repetir")
				}
			case AttemptReportedSuccess:
				switch last.Review {
				case "rejected":
					if strings.EqualFold(strings.TrimSpace(g.NextAction), strings.TrimSpace(last.Action)) {
						reasons = append(reasons, "revisar proxima acao apos revisao negativa")
					}
				case "accepted":
					if strings.EqualFold(strings.TrimSpace(g.NextAction), strings.TrimSpace(last.Action)) {
						reasons = append(reasons, "revisao positiva registrada; concluir meta ou definir proxima etapa")
					}
				default:
					r.NeedsVerification = append(r.NeedsVerification, g.ID)
					if g.Verification != nil && last.Verification == nil {
						reasons = append(reasons, "verificacao objetiva pendente; execute goal_attempt_verify")
					} else if g.Verification != nil && !last.Verification.Success {
						reasons = append(reasons, "verificacao objetiva negativa; revise o efeito")
					} else {
						reasons = append(reasons, "sucesso declarado; revisar evidencia antes de concluir")
					}
				}
			case AttemptFailed, AttemptInterrupted:
				switch last.Review {
				case "accepted":
					if strings.EqualFold(strings.TrimSpace(g.NextAction), strings.TrimSpace(last.Action)) {
						reasons = append(reasons, "efeito confirmado; concluir meta ou definir proxima etapa")
					}
				case "rejected":
					if strings.EqualFold(strings.TrimSpace(g.NextAction), strings.TrimSpace(last.Action)) {
						reasons = append(reasons, "revisar proxima acao apos falha ou interrupcao")
					}
				default:
					r.NeedsVerification = append(r.NeedsVerification, g.ID)
					if g.Verification != nil && last.Verification == nil {
						reasons = append(reasons, "verificacao objetiva pendente; execute goal_attempt_verify")
					} else {
						reasons = append(reasons, "falha ou interrupcao; verificar efeito antes de repetir")
					}
				}
			}
		}
		if len(reasons) == 0 {
			r.Ready = append(r.Ready, g.ID)
		} else {
			r.Waiting[g.ID] = reasons
		}
	}
	return r
}
func validate(goals []Goal) error {
	byID := map[string]Goal{}
	for _, g := range goals {
		if g.ID == "" || g.Title == "" || g.SuccessCriteria == "" {
			return errors.New("meta exige ID, titulo e criterio de sucesso")
		}
		if _, ok := byID[g.ID]; ok {
			return errors.New("ID de meta duplicado")
		}
		byID[g.ID] = g
		switch g.Status {
		case Pending, Active, Blocked, Paused, Completed, Cancelled:
		default:
			return fmt.Errorf("estado invalido: %s", g.Status)
		}
		if g.Status == Completed && strings.TrimSpace(g.Evidence) == "" {
			return errors.New("conclusao exige evidencia do criterio de sucesso")
		}
		if g.Verification != nil {
			if g.Verification.SourceKind != "goal" || g.Verification.SubjectID != g.ID ||
				g.Verification.Kind == "" || g.Verification.ExpectedSHA256 == "" || g.Verification.CreatedAt <= 0 {
				return errors.New("plano de verificacao da meta invalido")
			}
			if g.Status == Completed && (len(g.Attempts) == 0 ||
				g.Attempts[len(g.Attempts)-1].Verification == nil ||
				!g.Attempts[len(g.Attempts)-1].Verification.Success) {
				return errors.New("conclusao exige verificacao objetiva positiva da ultima tentativa")
			}
		}
		if err := validateAttempts(g); err != nil {
			return fmt.Errorf("meta %s: %w", g.ID, err)
		}
	}
	edges := map[string][]string{}
	for _, g := range goals {
		if g.ParentID != "" {
			p, ok := byID[g.ParentID]
			if !ok {
				return fmt.Errorf("meta superior inexistente: %s", g.ParentID)
			}
			if p.Status == Completed && g.Status != Completed && g.Status != Cancelled {
				return errors.New("meta concluida possui subtarefa pendente")
			}
			edges[p.ID] = append(edges[p.ID], g.ID)
		}
		seen := map[string]bool{}
		for _, id := range g.Dependencies {
			dep, ok := byID[id]
			if !ok {
				return fmt.Errorf("dependencia inexistente: %s", id)
			}
			if seen[id] {
				return fmt.Errorf("dependencia duplicada: %s", id)
			}
			seen[id] = true
			if (g.Status == Active || g.Status == Completed) && dep.Status != Completed {
				return fmt.Errorf("dependencia ainda nao concluida: %s", id)
			}
			edges[g.ID] = append(edges[g.ID], id)
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return errors.New("ciclo entre metas, subtarefas ou dependencias")
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range edges[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		done[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func clone(goals []Goal) []Goal {
	out := make([]Goal, len(goals))
	copy(out, goals)
	for i := range out {
		out[i].Dependencies = append([]string{}, goals[i].Dependencies...)
		out[i].History = append([]Transition{}, goals[i].History...)
		out[i].Attempts = append([]Attempt(nil), goals[i].Attempts...)
		if goals[i].Verification != nil {
			plan := *goals[i].Verification
			out[i].Verification = &plan
		}
		for j := range out[i].Attempts {
			if goals[i].Attempts[j].FinishedAt != nil {
				finished := *goals[i].Attempts[j].FinishedAt
				out[i].Attempts[j].FinishedAt = &finished
			}
			if goals[i].Attempts[j].ReviewedAt != nil {
				reviewed := *goals[i].Attempts[j].ReviewedAt
				out[i].Attempts[j].ReviewedAt = &reviewed
			}
			if goals[i].Attempts[j].Verification != nil {
				verification := *goals[i].Attempts[j].Verification
				out[i].Attempts[j].Verification = &verification
			}
		}
	}
	return out
}
func (s *Store) commit(next []Goal) error {
	if err := validate(next); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(diskState{Version: 1, Goals: next}, "", "  ")
	if err != nil {
		return fmt.Errorf("codificando metas: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("criando diretorio de metas: %w", err)
	}
	return withGoalFileLock(s.path, func() error {
		current, readErr := os.ReadFile(s.path)
		switch {
		case errors.Is(readErr, os.ErrNotExist):
			if s.present {
				return ErrStaleStore
			}
		case readErr != nil:
			return fmt.Errorf("conferindo versao atual das metas: %w", readErr)
		case !s.present || sha256.Sum256(current) != s.fingerprint:
			return ErrStaleStore
		}
		f, createErr := os.CreateTemp(filepath.Dir(s.path), ".goals-*")
		if createErr != nil {
			return fmt.Errorf("criando metas temporarias: %w", createErr)
		}
		defer os.Remove(f.Name())
		if err := f.Chmod(0600); err != nil {
			f.Close()
			return fmt.Errorf("protegendo metas temporarias: %w", err)
		}
		if _, err := f.Write(raw); err != nil {
			f.Close()
			return fmt.Errorf("gravando metas: %w", err)
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return fmt.Errorf("sincronizando metas: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("fechando metas: %w", err)
		}
		if err := os.Rename(f.Name(), s.path); err != nil {
			return fmt.Errorf("publicando metas: %w", err)
		}
		s.goals = next
		s.fingerprint = sha256.Sum256(raw)
		s.present = true
		return nil
	})
}
