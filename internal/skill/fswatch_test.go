package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillWatcherMarkdownLifecycle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeMarkdownSkill := func(name, body string) {
		t.Helper()
		content := "---\nname: " + name + "\ndescription: watched skill\n---\n" + body
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	type change struct {
		name   string
		exists bool
	}
	changes := make(chan change, 8)
	reg := NewRegistry()
	watcher, err := NewSkillWatcher([]string{root}, NewFSSkillLoader(root), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher.SetOnChange(func(name string, exists bool) { changes <- change{name, exists} })
	assertChange := func(want change) {
		t.Helper()
		select {
		case got := <-changes:
			if got != want {
				t.Fatalf("change = %+v, want %+v", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("missing change %+v", want)
		}
	}
	assertBody := func(name, want string) {
		t.Helper()
		skill, ok := reg.Get(name)
		if !ok {
			t.Fatalf("skill %q not registered", name)
		}
		body, err := skill.Execute(nil)
		if err != nil || body != want {
			t.Fatalf("skill %q body = %q, %v; want %q", name, body, err, want)
		}
	}

	writeMarkdownSkill("watched", "initial body")
	watcher.scan(context.Background())
	assertChange(change{"watched", true})
	assertBody("watched", "initial body")

	writeMarkdownSkill("watched", "updated and longer body")
	watcher.scan(context.Background())
	assertChange(change{"watched", true})
	assertBody("watched", "updated and longer body")

	writeMarkdownSkill("renamed", "replacement body")
	watcher.scan(context.Background())
	assertChange(change{"watched", false})
	assertChange(change{"renamed", true})
	if _, ok := reg.Get("watched"); ok {
		t.Fatal("old skill remained registered after rename")
	}
	assertBody("renamed", "replacement body")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	watcher.scan(context.Background())
	assertChange(change{"renamed", false})
	if _, ok := reg.Get("renamed"); ok {
		t.Fatal("deleted skill remained registered")
	}
}

func TestSkillWatcherKeepsWorkingSkillAfterInvalidEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: stable\ndescription: valid\n---\nbody"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	watcher, err := NewSkillWatcher([]string{root}, NewFSSkillLoader(root), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher.handleReload(path)
	if err := os.WriteFile(path, []byte("---\nname: broken\ndescription: [\n---\nbody"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher.handleReload(path)
	if _, ok := reg.Get("stable"); !ok {
		t.Fatal("invalid edit removed the last valid skill")
	}
	if _, ok := reg.Get("broken"); ok {
		t.Fatal("invalid skill was registered")
	}
}

func TestSkillWatcherCannotReplaceOrDeleteAnotherOrigin(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "collision", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: save_memory\ndescription: collision\n---\nuntrusted body"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register(&Skill{Name: "save_memory", Origin: "core", Execute: func(map[string]string) (string, error) {
		return "original body", nil
	}})
	watcher, err := NewSkillWatcher([]string{root}, NewFSSkillLoader(root), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher.handleReload(path)
	active, ok := reg.Get("save_memory")
	if !ok || active.Origin != "core" {
		t.Fatalf("core skill replaced by file: %+v", active)
	}
	if _, tracked := watcher.states[path]; tracked {
		t.Fatal("rejected file was tracked as registered")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	watcher.handleDelete(path)
	active, ok = reg.Get("save_memory")
	if !ok || active.Origin != "core" {
		t.Fatalf("deleting collision removed core skill: %+v", active)
	}
}
