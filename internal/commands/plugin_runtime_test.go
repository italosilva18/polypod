package commands

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/skill"
)

func TestPluginSlashLifecycle(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Exemplo","version":"1.0.0","skills":["SKILL.md"],"enabled":true}`
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	skillFile := "---\nname: sample-skill\ndescription: Exemplo local\n---\n\n# Instruções\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(skillFile), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := skill.NewPluginManager([]string{filepath.Join(root, "plugins")}, skill.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	reg := command.NewRegistry()
	BindPluginRuntime(reg, manager)
	run := func(args ...string) string {
		t.Helper()
		cmd, ok := reg.Get("plugin")
		if !ok {
			t.Fatal("plugin command not registered")
		}
		result, err := cmd.Execute(args, command.CommandContext{WorkingDir: root})
		if err != nil {
			t.Fatal(err)
		}
		return result.Display
	}
	if got := run("install", source); !strings.Contains(got, "instalado") {
		t.Fatalf("install: %s", got)
	}
	plugins, err := manager.List(t.Context())
	if err != nil || len(plugins) != 1 {
		t.Fatalf("list: %+v, %v", plugins, err)
	}
	id := plugins[0].ID
	if got := run("info", id); !strings.Contains(got, "sample-skill") {
		t.Fatalf("info: %s", got)
	}
	if got := run("disable", id); !strings.Contains(got, "false") {
		t.Fatalf("disable: %s", got)
	}
	if got := run("enable", id); !strings.Contains(got, "true") {
		t.Fatalf("enable: %s", got)
	}
	if got := run("reload", id); !strings.Contains(got, "recarregados") {
		t.Fatalf("reload: %s", got)
	}
	if got := run("remove", id); !strings.Contains(got, "removido") {
		t.Fatalf("remove: %s", got)
	}
	if got := run("list"); !strings.Contains(got, "Nenhum plugin") {
		t.Fatalf("list after removal: %s", got)
	}
}
