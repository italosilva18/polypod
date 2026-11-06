package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

const validSkill = `---
name: alpha
description: first skill
---
# Alpha body
`

const validSkill2 = `---
name: beta
description: second skill
---
# Beta body
`

func TestFSSkillLoader_LoadAllFromSubdirs(t *testing.T) {
	root := t.TempDir()

	writeSkill(t, filepath.Join(root, "alpha"), "SKILL.md", validSkill)
	writeSkill(t, filepath.Join(root, "nested", "beta"), "SKILL.md", validSkill2)
	// A stray markdown file that is NOT a skill file — should be ignored.
	writeSkill(t, filepath.Join(root, "alpha"), "README.md", "# just a readme")

	loader := NewFSSkillLoader(root)
	skills, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", len(skills), skillNames(skills))
	}
	names := map[string]bool{}
	for _, s := range skills {
		names[s.Frontmatter.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing skills: %v", names)
	}
}

func TestFSSkillLoader_SkipsFilesWithoutSkillName(t *testing.T) {
	root := t.TempDir()

	// Wrong name — should be skipped.
	writeSkill(t, root, "notes.md", validSkill)
	writeSkill(t, root, "readme.md", validSkill)
	// Sidecar format — should be picked up.
	writeSkill(t, filepath.Join(root, "gamma"), "thing.skill.md", validSkill)
	// Canonical name nested — should be picked up.
	writeSkill(t, filepath.Join(root, "delta"), "SKILL.md", validSkill2)

	loader := NewFSSkillLoader(root)
	skills, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d: %v", len(skills), skillNames(skills))
	}
}

func TestFSSkillLoader_JoinsErrorsOnPartialFailure(t *testing.T) {
	root := t.TempDir()

	// Good file.
	writeSkill(t, filepath.Join(root, "good"), "SKILL.md", validSkill)
	// Broken YAML.
	writeSkill(t, filepath.Join(root, "broken"), "SKILL.md", `---
name: broken
description: "unterminated
  nested: [oops
---
body
`)

	loader := NewFSSkillLoader(root)
	skills, err := loader.LoadAll(context.Background())
	if err == nil {
		t.Fatalf("expected joined error")
	}
	if !strings.Contains(err.Error(), "parse frontmatter") {
		t.Errorf("error should mention parse frontmatter: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 good skill to still load, got %d", len(skills))
	}
	if skills[0].Frontmatter.Name != "alpha" {
		t.Errorf("wrong skill loaded: %q", skills[0].Frontmatter.Name)
	}
}

func TestFSSkillLoader_MissingRootIsNotAnError(t *testing.T) {
	loader := NewFSSkillLoader(filepath.Join(t.TempDir(), "does-not-exist"))
	skills, err := loader.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("missing roots should be silent, got %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(skills))
	}
}

func TestFSSkillLoader_AddRootAndDefaults(t *testing.T) {
	loader := NewFSSkillLoader()
	if len(loader.Roots) != 0 {
		t.Fatalf("expected empty roots")
	}
	loader.AddRoot("/tmp/one")
	loader.AddRoot("/tmp/two")
	if len(loader.Roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(loader.Roots))
	}

	dirs := DefaultSkillDirs()
	if len(dirs) == 0 {
		t.Fatal("DefaultSkillDirs returned empty")
	}
}

func TestFSSkillLoader_Watch(t *testing.T) {
	root := t.TempDir()
	loader := NewFSSkillLoader(root)
	loader.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan *ParsedSkill, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- loader.Watch(ctx, func(s *ParsedSkill) { changes <- s }) }()

	time.Sleep(20 * time.Millisecond)
	writeSkill(t, root, "SKILL.md", validSkill)
	select {
	case got := <-changes:
		if got.Frontmatter.Name != "alpha" {
			t.Fatalf("changed skill = %q; want alpha", got.Frontmatter.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not report the new skill")
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Watch: %v", err)
	}
}

func skillNames(ss []*ParsedSkill) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Frontmatter.Name)
	}
	return out
}
