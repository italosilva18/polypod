package profiling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

func newTestRegistry(t *testing.T) *skill.Registry {
	t.Helper()
	reg := skill.NewRegistry()
	RegisterSkills(reg)
	return reg
}

func TestRegisterSkills_RegistersAllExpected(t *testing.T) {
	reg := newTestRegistry(t)

	expected := []string{
		"profile_go",
		"profile_analyze",
		"profile_flame",
		"profile_python",
	}

	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected skill %q to be registered", name)
		}
	}
}

func TestRegisterSkills_SkillMetadata(t *testing.T) {
	reg := newTestRegistry(t)

	cases := map[string][]string{
		"profile_go":      {"binary"},
		"profile_analyze": {"path"},
		"profile_flame":   {"path"},
		"profile_python":  {"script"},
	}

	for name, required := range cases {
		s, ok := reg.Get(name)
		if !ok {
			t.Fatalf("skill %q not found", name)
		}
		if s.Description == "" {
			t.Errorf("skill %q has empty description", name)
		}
		if s.Execute == nil {
			t.Errorf("skill %q has nil Execute", name)
		}
		if len(s.Parameters.Required) != len(required) {
			t.Errorf("skill %q: expected %d required params, got %d",
				name, len(required), len(s.Parameters.Required))
		}
		for _, req := range required {
			found := false
			for _, r := range s.Parameters.Required {
				if r == req {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("skill %q: missing required param %q", name, req)
			}
		}
	}
}

func TestProfileGo_RejectsNonGoTestBinary(t *testing.T) {
	reg := newTestRegistry(t)
	s, ok := reg.Get("profile_go")
	if !ok {
		t.Fatal("profile_go not registered")
	}

	// When binary != "go test", the skill returns an error instructing the
	// user to use `go tool pprof` against a running server.
	_, err := s.Execute(map[string]string{
		"binary": "/usr/local/bin/myapp",
		"type":   "cpu",
	})
	if err == nil {
		t.Fatal("expected error for non-'go test' binary, got nil")
	}
	if !strings.Contains(err.Error(), "go tool pprof") {
		t.Errorf("expected hint about 'go tool pprof' in error, got: %v", err)
	}
}

func TestProfileGo_DefaultsAreAppliedViaRegistryExecute(t *testing.T) {
	reg := newTestRegistry(t)

	// Call through the registry (JSON path) with a non-"go test" binary so
	// that we don't actually spawn `go test`. This exercises the default
	// handling branches (type empty -> "cpu", duration empty -> "30").
	out, err := reg.Execute("profile_go", `{"binary":"/nonexistent/app"}`)
	if err != nil {
		t.Fatalf("registry.Execute returned unexpected error: %v", err)
	}
	// Skill errors are surfaced as "Erro: ..." strings by Registry.Execute.
	if !strings.HasPrefix(out, "Erro:") {
		t.Errorf("expected error string output, got: %q", out)
	}
	if !strings.Contains(out, "cpu") {
		t.Errorf("expected default profile type 'cpu' to appear in error, got: %q", out)
	}
}

func TestProfileAnalyze_MissingFile(t *testing.T) {
	reg := newTestRegistry(t)
	s, ok := reg.Get("profile_analyze")
	if !ok {
		t.Fatal("profile_analyze not registered")
	}

	missing := filepath.Join(t.TempDir(), "does_not_exist.prof")
	_, err := s.Execute(map[string]string{"path": missing})
	if err == nil {
		t.Fatal("expected error for missing profile file, got nil")
	}
	if !strings.Contains(err.Error(), "nao encontrado") {
		t.Errorf("expected 'nao encontrado' in error, got: %v", err)
	}
}

func TestProfileAnalyze_InvalidFile(t *testing.T) {
	reg := newTestRegistry(t)
	s, ok := reg.Get("profile_analyze")
	if !ok {
		t.Fatal("profile_analyze not registered")
	}

	// Create an empty (invalid) profile file; pprof should fail but the
	// skill's implementation ignores pprof errors and returns formatted
	// output, so we just verify we get a markdown section back.
	tmp := filepath.Join(t.TempDir(), "bogus.prof")
	if err := os.WriteFile(tmp, []byte("not a real profile"), 0o644); err != nil {
		t.Fatalf("write temp profile: %v", err)
	}

	out, err := s.Execute(map[string]string{"path": tmp, "top": "5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Analise de Profile") {
		t.Errorf("expected 'Analise de Profile' header in output, got: %q", out)
	}
	if !strings.Contains(out, tmp) {
		t.Errorf("expected profile path %q in output", tmp)
	}
	if !strings.Contains(out, "Top 5") {
		t.Errorf("expected 'Top 5' in output (custom top value), got: %q", out)
	}
}

func TestProfileFlame_HasCorrectParameters(t *testing.T) {
	reg := newTestRegistry(t)
	s, ok := reg.Get("profile_flame")
	if !ok {
		t.Fatal("profile_flame not registered")
	}

	// Verify parameter schema advertises both 'path' and 'output'.
	props := s.Parameters.Properties
	if _, ok := props["path"]; !ok {
		t.Error("profile_flame missing 'path' parameter")
	}
	if _, ok := props["output"]; !ok {
		t.Error("profile_flame missing 'output' parameter")
	}
	if len(s.Parameters.Required) != 1 || s.Parameters.Required[0] != "path" {
		t.Errorf("profile_flame required should be [path], got %v",
			s.Parameters.Required)
	}
}

func TestProfilePython_ParameterSchema(t *testing.T) {
	reg := newTestRegistry(t)
	s, ok := reg.Get("profile_python")
	if !ok {
		t.Fatal("profile_python not registered")
	}

	props := s.Parameters.Properties
	if _, ok := props["script"]; !ok {
		t.Error("profile_python missing 'script' parameter")
	}
	if _, ok := props["top"]; !ok {
		t.Error("profile_python missing 'top' parameter")
	}
	if !strings.Contains(strings.ToLower(s.Description), "python") {
		t.Errorf("profile_python description should mention Python, got: %q",
			s.Description)
	}
}

func TestRegisterSkills_IsIdempotentOnRegistration(t *testing.T) {
	reg := skill.NewRegistry()
	before := len(reg.List())

	RegisterSkills(reg)
	afterFirst := len(reg.List())
	if afterFirst != before+4 {
		t.Errorf("expected %d skills after first register, got %d",
			before+4, afterFirst)
	}

	// Registering again should overwrite (same keys), not add more.
	RegisterSkills(reg)
	afterSecond := len(reg.List())
	if afterSecond != afterFirst {
		t.Errorf("expected stable count after re-register, got %d -> %d",
			afterFirst, afterSecond)
	}
}
