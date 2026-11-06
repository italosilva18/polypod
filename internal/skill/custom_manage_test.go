package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai/jsonschema"
)

func TestCreateSkillDoesNotReplaceExistingSkill(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	reg.Register(&Skill{Name: "reviewed", Description: "approved", Parameters: jsonschema.Definition{Type: jsonschema.Object},
		Execute: func(map[string]string) (string, error) { return "original", nil }})
	RegisterDynamicManagement(reg, dir)
	result, err := reg.ExecuteResult("create_skill", `{"name":"reviewed","description":"replacement","script":"echo replaced"}`)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "ja existe") {
		t.Fatalf("duplicate creation result = %+v, %v", result, err)
	}
	got, err := reg.Execute("reviewed", `{}`)
	if err != nil || got != "original" {
		t.Fatalf("registered skill was overwritten: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviewed.yaml")); !os.IsNotExist(err) {
		t.Fatalf("duplicate skill file was written: %v", err)
	}
}

func TestDeleteCustomSkillLeavesOtherRuntimeSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reviewed.yaml"), []byte("name: reviewed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&Skill{Name: "reviewed", Description: "approved", Parameters: jsonschema.Definition{Type: jsonschema.Object},
		Execute: func(map[string]string) (string, error) { return "original", nil }})
	RegisterDynamicManagement(reg, dir)
	result, err := reg.ExecuteResult("delete_custom_skill", `{"name":"reviewed"}`)
	if err != nil || result.IsError {
		t.Fatalf("deletion result = %+v, %v", result, err)
	}
	got, ok := reg.Get("reviewed")
	if !ok || got.Description != "approved" {
		t.Fatalf("unrelated runtime skill was removed: %+v", got)
	}
}

func TestDeleteCustomSkillRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, "data", "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "agents", "default.yaml")
	if err := os.MkdirAll(filepath.Dir(outside), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("name: default\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	RegisterDynamicManagement(reg, skillsDir)
	result, err := reg.ExecuteResult("delete_custom_skill", `{"name":"../../agents/default"}`)
	if err != nil || !result.IsError {
		t.Fatalf("path traversal was accepted: %+v, %v", result, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside agent file was touched: %v", err)
	}
}
