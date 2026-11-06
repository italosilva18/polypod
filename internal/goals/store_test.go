package goals

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func createTest(t *testing.T, s *Store, parent string, deps ...string) Goal {
	t.Helper()
	g, err := s.Create(CreateInput{Title: "Entrega", SuccessCriteria: "Teste passou", NextAction: "Executar teste", ParentID: parent, Dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func completeTest(t *testing.T, s *Store, id string) {
	t.Helper()
	status, evidence := Completed, "Teste passou: registro local"
	if _, err := s.Update(id, UpdateInput{Status: &status, Evidence: &evidence, Note: "Validado"}); err != nil {
		t.Fatal(err)
	}
}
func TestRestartAndDependencies(t *testing.T) {
	s := openTest(t)
	a := createTest(t, s, "")
	b := createTest(t, s, "", a.ID)
	if r := s.Review(); len(r.Ready) != 1 || r.Ready[0] != a.ID {
		t.Fatalf("readiness: %+v", r)
	}
	active := Active
	if _, err := s.Update(b.ID, UpdateInput{Status: &active, Note: "Iniciar"}); err == nil {
		t.Fatal("started with unmet dependency")
	}
	completeTest(t, s, a.ID)
	if _, err := s.Update(b.ID, UpdateInput{Status: &active, Note: "Iniciar"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.List(); len(got) != 2 || got[1].Status != Active || len(got[1].History) != 2 {
		t.Fatalf("restart lost state: %+v", got)
	}
	pending := Pending
	if _, err := s.Update(a.ID, UpdateInput{Status: &pending, Note: "Reabrir"}); err == nil {
		t.Fatal("invalidated active dependency")
	}
}
func TestSubtasksAndCycles(t *testing.T) {
	s := openTest(t)
	p := createTest(t, s, "")
	c := createTest(t, s, p.ID)
	status, evidence := Completed, "feito"
	if _, err := s.Update(p.ID, UpdateInput{Status: &status, Evidence: &evidence, Note: "Concluir"}); err == nil {
		t.Fatal("completed with unfinished child")
	}
	deps := []string{p.ID}
	if _, err := s.Update(c.ID, UpdateInput{Dependencies: &deps, Note: "Ciclo"}); err == nil {
		t.Fatal("accepted parent cycle")
	}
	completeTest(t, s, c.ID)
	completeTest(t, s, p.ID)
	if _, err := s.Create(CreateInput{Title: "Nova", SuccessCriteria: "OK", ParentID: p.ID}); err == nil {
		t.Fatal("added pending child to completed parent")
	}
}
func TestFailedWriteDoesNotPublishAndCopies(t *testing.T) {
	s := openTest(t)
	g := createTest(t, s, "")
	copied := s.List()
	copied[0].History[0].Note = "changed"
	if s.List()[0].History[0].Note == "changed" {
		t.Fatal("shared slice")
	}
	s.path = filepath.Join(s.path, "invalid.json")
	status := Paused
	if _, err := s.Update(g.ID, UpdateInput{Status: &status, Note: "Pausa"}); err == nil {
		t.Fatal("expected IO error")
	}
	if s.List()[0].Status != Pending {
		t.Fatal("failed mutation published")
	}
}
func TestCorruptionAndPermissions(t *testing.T) {
	s := openTest(t)
	createTest(t, s, "")
	info, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
	if err := os.WriteFile(s.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.path); err == nil {
		t.Fatal("silently discarded corrupt state")
	}
}
func TestSkills(t *testing.T) {
	s := openTest(t)
	r := skill.NewRegistry()
	RegisterSkills(r, s)
	if _, err := r.Execute("goal_create", `{"title":"Meta","success_criteria":"Validado","next_action":"Testar"}`); err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 {
		t.Fatal("skill did not persist")
	}
	for _, name := range []string{"goal_list", "goal_review"} {
		if _, err := r.Execute(name, `{}`); err != nil {
			t.Fatal(err)
		}
	}
}
