package tool

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// TestRegisterAllSkillsDedup verifies that RegisterAllSkills:
//   - skips skills with safer real-tool equivalents (read_file, list_directory,
//     run_command, search_files), so the AI never sees the unsafe duplicate
//   - prefixes the rest with "skill_" to avoid colliding with builtins
func TestRegisterAllSkillsDedup(t *testing.T) {
	skillReg := skill.NewRegistry() // ships with builtin skills
	toolReg := NewRegistry()

	RegisterAllSkills(toolReg, skillReg)
	RegisterBuiltinTools(toolReg, ".")

	// None of the dangerous unprefixed skill names may appear via the
	// SkillAdapter — only the safer builtins are allowed under those names.
	for _, name := range toolReg.List() {
		switch name {
		case "read_file", "list_directory", "run_command", "search_files":
			tl, _ := toolReg.Get(name)
			if _, isAdapter := tl.(*SkillAdapter); isAdapter {
				t.Errorf("dangerous skill duplicate exposed: %q (must be filtered or skill_-prefixed)", name)
			}
		}
	}

	// Any skill that does NOT have a real equivalent must be exposed
	// under the "skill_" prefix, never under its raw name.
	for _, name := range skillReg.List() {
		if hasRealToolEquivalent(name) {
			continue
		}
		if _, ok := toolReg.Get("skill_" + name); !ok {
			t.Errorf("expected skill %q to be exposed as %q, missing", name, "skill_"+name)
		}
		// And the bare name must not collide with anything coming from
		// the skill bridge (a real builtin under the same bare name is OK).
		if got, ok := toolReg.Get(name); ok {
			if _, isAdapter := got.(*SkillAdapter); isAdapter {
				t.Errorf("skill %q exposed under bare name via SkillAdapter; should be skill_%s", name, name)
			}
		}
	}

	// Sanity: builtins are present.
	for _, name := range []string{"read_file", "bash", "grep", "glob", "write_file", "edit_file"} {
		if _, ok := toolReg.Get(name); !ok {
			t.Errorf("expected builtin %q to be registered", name)
		}
		if strings.HasPrefix(name, "skill_") {
			t.Errorf("builtin %q must not be skill_-prefixed", name)
		}
	}
}
