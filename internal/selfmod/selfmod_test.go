package selfmod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/skill"
)

// writeAgentYAML creates a minimal agent YAML file in dir for use by tests.
func writeAgentYAML(t *testing.T, dir, name, persona string, skills []string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: test agent\n")
	sb.WriteString("persona: \"" + persona + "\"\n")
	sb.WriteString("skills:\n")
	for _, s := range skills {
		sb.WriteString("  - " + s + "\n")
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("write agent yaml: %v", err)
	}
}

// setup creates a fresh skill registry, agent registry and temp agents dir,
// registers all selfmod skills and returns the dir + registries.
func setup(t *testing.T) (string, *skill.Registry, *agent.Registry) {
	t.Helper()
	dir := t.TempDir()
	skillReg := skill.NewRegistry()
	agentReg := agent.NewRegistry()
	RegisterSkills(skillReg, agentReg, dir)
	return dir, skillReg, agentReg
}

// TestRegisterSkillsRegistersAllFourSkills verifies the constructor wires up
// every self-modification skill into the supplied registry.
func TestRegisterSkillsRegistersAllFourSkills(t *testing.T) {
	_, skillReg, _ := setup(t)

	expected := []string{
		"read_agent_config",
		"update_persona",
		"add_agent_skill",
		"remove_agent_skill",
	}
	for _, name := range expected {
		if _, ok := skillReg.Get(name); !ok {
			t.Errorf("skill %q was not registered", name)
		}
	}
}

// TestCoreSkillsMap ensures the core-skills safety guard contains the expected
// non-removable skills.
func TestCoreSkillsMap(t *testing.T) {
	if !coreSkills["read_file"] {
		t.Error("expected read_file to be a core skill")
	}
	if !coreSkills["run_command"] {
		t.Error("expected run_command to be a core skill")
	}
	if coreSkills["update_persona"] {
		t.Error("update_persona must NOT be marked core (would block self-mod)")
	}
}

// TestReadAgentConfig exercises the read_agent_config skill on an existing
// agent file.
func TestReadAgentConfig(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "you are a tester", []string{"read_file"})

	s, ok := skillReg.Get("read_agent_config")
	if !ok {
		t.Fatal("read_agent_config skill missing")
	}

	out, err := s.Execute(map[string]string{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "you are a tester") {
		t.Errorf("expected persona in output, got %q", out)
	}
	if !strings.Contains(out, "read_file") {
		t.Errorf("expected skills in output, got %q", out)
	}
}

// TestReadAgentConfigMissing checks the path validation: missing file -> error.
func TestReadAgentConfigMissing(t *testing.T) {
	_, skillReg, _ := setup(t)

	s, _ := skillReg.Get("read_agent_config")
	_, err := s.Execute(map[string]string{"agent_name": "ghost"})
	if err == nil {
		t.Fatal("expected error for missing agent file")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected error to mention agent name, got %v", err)
	}
}

// TestUpdatePersonaValidation ensures update_persona rejects empty input.
func TestUpdatePersonaValidation(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "old persona", []string{"read_file"})

	s, _ := skillReg.Get("update_persona")

	// Empty new_persona -> error
	if _, err := s.Execute(map[string]string{"new_persona": ""}); err == nil {
		t.Error("expected error for empty new_persona")
	}

	// Missing agent file -> error
	if _, err := s.Execute(map[string]string{"new_persona": "x", "agent_name": "missing"}); err == nil {
		t.Error("expected error for missing agent file")
	}
}

// TestUpdatePersonaSuccess covers the happy path: persona is rewritten,
// backup is created, and the agent registry is updated.
func TestUpdatePersonaSuccess(t *testing.T) {
	dir, skillReg, agentReg := setup(t)
	writeAgentYAML(t, dir, "default", "old persona", []string{"read_file"})

	s, _ := skillReg.Get("update_persona")
	out, err := s.Execute(map[string]string{"new_persona": "brand new persona"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "atualizada") {
		t.Errorf("unexpected success message: %q", out)
	}

	// Backup file must exist.
	if _, err := os.Stat(filepath.Join(dir, "default.yaml.bak")); err != nil {
		t.Errorf("expected backup file to exist: %v", err)
	}

	// YAML on disk must contain the new persona.
	data, err := os.ReadFile(filepath.Join(dir, "default.yaml"))
	if err != nil {
		t.Fatalf("read updated yaml: %v", err)
	}
	if !strings.Contains(string(data), "brand new persona") {
		t.Errorf("yaml does not contain new persona: %s", string(data))
	}

	// Agent registry was updated.
	ag := agentReg.Get("default")
	if ag == nil || ag.Persona != "brand new persona" {
		t.Errorf("agent registry not updated, got %+v", ag)
	}
}

// TestAddAgentSkillValidation: missing skill_name should error.
func TestAddAgentSkillValidation(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "p", []string{"read_file"})

	s, _ := skillReg.Get("add_agent_skill")

	if _, err := s.Execute(map[string]string{"skill_name": ""}); err == nil {
		t.Error("expected error for empty skill_name")
	}
}

// TestAddAgentSkillSuccess and idempotency.
func TestAddAgentSkillSuccess(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "p", []string{"read_file"})

	s, _ := skillReg.Get("add_agent_skill")

	out, err := s.Execute(map[string]string{"skill_name": "list_directory"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "adicionada") {
		t.Errorf("unexpected output: %q", out)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "default.yaml"))
	if !strings.Contains(string(data), "list_directory") {
		t.Errorf("expected list_directory in yaml: %s", string(data))
	}

	// Adding the same skill again should be a no-op (and not re-create backup).
	_ = os.Remove(filepath.Join(dir, "default.yaml.bak"))
	out2, err := s.Execute(map[string]string{"skill_name": "list_directory"})
	if err != nil {
		t.Fatalf("idempotent execute: %v", err)
	}
	if !strings.Contains(out2, "ja esta") {
		t.Errorf("expected idempotent message, got %q", out2)
	}
	if _, err := os.Stat(filepath.Join(dir, "default.yaml.bak")); !os.IsNotExist(err) {
		t.Errorf("expected no backup on idempotent add, got err=%v", err)
	}
}

// TestRemoveAgentSkillCoreGuard verifies that core skills cannot be removed.
func TestRemoveAgentSkillCoreGuard(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "p", []string{"read_file", "run_command", "list_directory"})

	s, _ := skillReg.Get("remove_agent_skill")

	for _, core := range []string{"read_file", "run_command"} {
		_, err := s.Execute(map[string]string{"skill_name": core})
		if err == nil {
			t.Errorf("expected error removing core skill %q", core)
			continue
		}
		if !strings.Contains(err.Error(), "core") {
			t.Errorf("expected 'core' in error for %q, got %v", core, err)
		}
	}

	// Yaml must still contain the core skills (untouched).
	data, _ := os.ReadFile(filepath.Join(dir, "default.yaml"))
	if !strings.Contains(string(data), "read_file") || !strings.Contains(string(data), "run_command") {
		t.Errorf("core skills were removed from yaml: %s", string(data))
	}
}

// TestRemoveAgentSkillSuccess covers the normal removal path and the
// "skill not found" branch.
func TestRemoveAgentSkillSuccess(t *testing.T) {
	dir, skillReg, _ := setup(t)
	writeAgentYAML(t, dir, "default", "p", []string{"read_file", "list_directory"})

	s, _ := skillReg.Get("remove_agent_skill")

	// Removing existing non-core skill works.
	out, err := s.Execute(map[string]string{"skill_name": "list_directory"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "removida") {
		t.Errorf("unexpected message: %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "default.yaml"))
	if strings.Contains(string(data), "list_directory") {
		t.Errorf("skill still present after removal: %s", string(data))
	}

	// Removing a skill that does not exist returns a friendly message, not error.
	out2, err := s.Execute(map[string]string{"skill_name": "nope"})
	if err != nil {
		t.Fatalf("execute not-found: %v", err)
	}
	if !strings.Contains(out2, "nao encontrada") {
		t.Errorf("expected 'nao encontrada', got %q", out2)
	}

	// Empty skill_name -> error.
	if _, err := s.Execute(map[string]string{"skill_name": ""}); err == nil {
		t.Error("expected error for empty skill_name")
	}
}
