package tool

import (
	"testing"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

func TestDynamicSkillChangesReachToolRegistry(t *testing.T) {
	skills := skill.NewRegistry()
	tools := NewRegistry()
	RegisterAllSkills(tools, skills)
	skills.SetOnChange(func(name string, exists bool) {
		if exists {
			RegisterAllSkills(tools, skills)
		} else {
			tools.Remove("skill_" + name)
		}
	})
	register := func(text string) {
		skills.Register(&skill.Skill{Name: "dynamic_example", Description: text,
			Parameters: jsonschema.Definition{Type: jsonschema.Object},
			Execute:    func(map[string]string) (string, error) { return text, nil }})
	}
	register("primeira versão")
	first, ok := tools.Get("skill_dynamic_example")
	if !ok || first.Description() != "primeira versão" {
		t.Fatal("nova skill não entrou no ToolRegistry")
	}
	register("segunda versão")
	second, ok := tools.Get("skill_dynamic_example")
	if !ok || second.Description() != "segunda versão" {
		t.Fatal("atualização da skill não entrou no ToolRegistry")
	}
	skills.Unregister("dynamic_example")
	if _, ok := tools.Get("skill_dynamic_example"); ok {
		t.Fatal("skill removida ainda aparece no ToolRegistry")
	}
}
