package tool

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

func TestImportedPolyClawSkillsVisibleToDefaultAgent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	agents := agent.NewRegistry()
	if err := agents.LoadDir(filepath.Join(root, "agents")); err != nil {
		t.Fatal(err)
	}
	defaultAgent := agents.Get("default")
	if defaultAgent.Name != "default" {
		t.Fatalf("loaded agent = %q, want default", defaultAgent.Name)
	}

	parsed, err := skill.NewFSSkillLoader(filepath.Join(root, ".polypod", "skills")).LoadAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	skillRegistry := skill.NewRegistry()
	imported := make(map[string]bool)
	for _, ps := range parsed {
		if !strings.HasPrefix(ps.Frontmatter.Name, "polyclaw_") {
			continue
		}
		if err := ps.Frontmatter.Validate(); err != nil {
			t.Fatalf("skill %q: %v", ps.Frontmatter.Name, err)
		}
		name, body := ps.Frontmatter.Name, ps.Body
		imported[name] = true
		skillRegistry.Register(&skill.Skill{
			Name: name, Description: ps.Frontmatter.Description,
			Parameters: jsonschema.Definition{Type: jsonschema.Object},
			Execute:    func(map[string]string) (string, error) { return body, nil },
		})
	}
	for _, name := range []string{
		"polyclaw_browser", "polyclaw_github", "polyclaw_tmux",
		"polyclaw_weather", "polyclaw_skill_creator", "polyclaw_summarize",
		"polyclaw_hardware",
	} {
		if !imported[name] {
			t.Errorf("filesystem skill %q was not loaded", name)
		}
	}

	toolRegistry := NewRegistry()
	RegisterBuiltinTools(toolRegistry, root)
	RegisterAllSkills(toolRegistry, skillRegistry)
	visible := make(map[string]bool)
	for _, tool := range toolRegistry.FilterByNames(defaultAgent.Skills) {
		visible[tool.Name()] = true
	}
	for name := range imported {
		if !visible["skill_"+name] {
			t.Errorf("imported skill %q is absent from default agent's filtered tools", name)
		}
	}
	for _, name := range []string{"bash", "write_file", "read_file_dir"} {
		if !visible[name] {
			t.Errorf("builtin tool %q is absent from default agent's filtered tools", name)
		}
	}
}
