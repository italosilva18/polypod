package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/skill"
	"github.com/costa/polypod/internal/types"
)

func TestSkillCatalogToolsFindInstallAndReload(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "catalog", "newskill")
	active := filepath.Join(base, ".polypod", "skills")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: newskill\ndescription: Use nova skill local\n---\n\n# Nova skill\n"), 0644); err != nil {
		t.Fatal(err)
	}
	skillReg := skill.NewRegistry()
	toolReg := NewRegistry()
	skillReg.SetOnChange(func(name string, exists bool) {
		if exists {
			RegisterAllSkills(toolReg, skillReg)
		}
	})
	RegisterSkillCatalogTools(toolReg, skill.NewLocalCatalog(filepath.Dir(source), active, skillReg))
	find, ok := toolReg.Get("find_skills")
	if !ok || !find.IsReadOnly(nil) || !find.IsConcurrencySafe(nil) {
		t.Fatal("find_skills is not registered as a read-only tool")
	}
	install, ok := toolReg.Get("install_skill")
	if !ok || install.IsReadOnly(nil) || !install.IsDestructive(nil) {
		t.Fatal("install_skill classification is wrong")
	}
	if install.ValidateInput(map[string]any{"slug": ""}).Valid {
		t.Fatal("empty slug accepted")
	}
	ctx := types.ToolContext{Context: context.Background()}
	search, err := toolReg.Execute("find_skills", map[string]any{"query": "nova", "limit": 5}, ctx)
	if err != nil || search.IsError {
		t.Fatalf("find failed: %+v, %v", search, err)
	}
	var found struct {
		Results []skill.CatalogEntry `json:"results"`
	}
	if err := json.Unmarshal([]byte(search.Content), &found); err != nil || len(found.Results) != 1 || found.Results[0].Slug != "newskill" {
		t.Fatalf("find returned wrong skill: %+v, %v", found, err)
	}
	result, err := toolReg.Execute("install_skill", map[string]any{"slug": "newskill"}, ctx)
	if err != nil || result.IsError {
		t.Fatalf("install failed: %+v, %v", result, err)
	}
	if _, ok := toolReg.Get("skill_newskill"); !ok {
		t.Fatal("installed skill was not exposed to tool registry")
	}
	var installed skill.InstallResult
	if err := json.Unmarshal([]byte(result.Content), &installed); err != nil || installed.Status != "installed" {
		t.Fatalf("install result: %+v, %v", installed, err)
	}
	if _, err := os.Stat(filepath.Join(active, "newskill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
