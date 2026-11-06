// Package goals stores operational plans. It never executes work or schedules jobs.
package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

type Transition struct {
	At   time.Time `json:"at"`
	From Status    `json:"from,omitempty"`
	To   Status    `json:"to"`
	Note string    `json:"note"`
}
type Goal struct {
	ID              string       `json:"id"`
	ParentID        string       `json:"parent_id,omitempty"`
	Title           string       `json:"title"`
	SuccessCriteria string       `json:"success_criteria"`
	Dependencies    []string     `json:"dependencies"`
	Status          Status       `json:"status"`
	NextAction      string       `json:"next_action"`
	Evidence        string       `json:"evidence,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	History         []Transition `json:"history"`
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
	Goals   []Goal              `json:"goals"`
	Ready   []string            `json:"ready"`
	Waiting map[string][]string `json:"waiting"`
}
type diskState struct {
	Version int    `json:"version"`
	Goals   []Goal `json:"goals"`
}

// Store is owned by one process. mu protects all goal state; returned values are copies.
type Store struct {
	mu    sync.Mutex
	path  string
	goals []Goal
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("caminho de metas vazio")
	}
	s := &Store{path: path, goals: []Goal{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lendo metas: %w", err)
	}
	var disk diskState
	if err = json.Unmarshal(raw, &disk); err != nil {
		return nil, fmt.Errorf("decodificando metas: %w", err)
	}
	if disk.Version != 1 {
		return nil, fmt.Errorf("versao de metas desconhecida: %d", disk.Version)
	}
	if err = validate(disk.Goals); err != nil {
		return nil, fmt.Errorf("metas invalidas: %w", err)
	}
	s.goals = clone(disk.Goals)
	return s, nil
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
	r := Review{Goals: clone(s.goals), Ready: []string{}, Waiting: map[string][]string{}}
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
	f, err := os.CreateTemp(filepath.Dir(s.path), ".goals-*")
	if err != nil {
		return fmt.Errorf("criando metas temporarias: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return fmt.Errorf("gravando metas: %w", err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sincronizando metas: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("fechando metas: %w", err)
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return fmt.Errorf("publicando metas: %w", err)
	}
	s.goals = next
	return nil
}
