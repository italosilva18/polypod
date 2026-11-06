// Package commands — batch3_core_test.go
// Smoke + happy/error path tests for /model, /config, /doctor.
package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch3Ctx returns a CommandContext rooted in an isolated temp dir and
// redirects POLYPOD_CONFIG_DIR / HOME so on-disk helpers never touch the
// developer's real state.
func newBatch3Ctx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("POLYPOD_CONFIG_DIR", filepath.Join(tmp, "polypod-home"))
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	if err := os.MkdirAll(filepath.Join(tmp, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "polypod-home"), 0o755); err != nil {
		t.Fatal(err)
	}
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "batch3-test-session",
	}
}

// writeBatch3Settings writes a fake settings.json into POLYPOD_CONFIG_DIR.
func writeBatch3Settings(t *testing.T, data map[string]any) {
	t.Helper()
	path := filepath.Join(configHomeDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch3Core_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch3Core(reg)

	for _, name := range []string{"model", "config", "doctor"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("command %q not registered", name)
		}
	}
	// /config should also resolve via the "settings" alias.
	if _, ok := reg.Get("settings"); !ok {
		t.Error("alias 'settings' not registered for /config")
	}
}

// ---------------------------------------------------------------------------
// /model
// ---------------------------------------------------------------------------

func TestCmdModel_NoArgs_ListsAvailable(t *testing.T) {
	ctx := newBatch3Ctx(t)
	writeBatch3Settings(t, map[string]any{
		"ai": map[string]any{
			"provider": "deepseek",
			"model":    "deepseek-chat",
		},
	})

	res, err := cmdModel().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdModel no-args error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdModel returned empty display")
	}
	if !strings.Contains(res.Display, "Modelo ativo") {
		t.Errorf("missing 'Modelo ativo' header: %s", res.Display)
	}
	if !strings.Contains(res.Display, "deepseek-chat") {
		t.Errorf("expected deepseek-chat in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Modelos disponiveis") {
		t.Errorf("expected 'Modelos disponiveis' section: %s", res.Display)
	}
}

func TestCmdModel_SwitchKnownModel_Persisted(t *testing.T) {
	ctx := newBatch3Ctx(t)
	writeBatch3Settings(t, map[string]any{
		"ai": map[string]any{
			"provider": "deepseek",
			"model":    "deepseek-chat",
		},
	})

	res, err := cmdModel().Execute([]string{"gpt-4o-mini"}, ctx)
	if err != nil {
		t.Fatalf("cmdModel switch error: %v", err)
	}
	if !strings.Contains(res.Display, "alterado para gpt-4o-mini") {
		t.Errorf("expected confirmation message, got: %s", res.Display)
	}

	// Verify the per-session JSON was updated.
	m, err := readSessionJSON(ctx.SessionID)
	if err != nil {
		t.Fatalf("readSessionJSON after switch: %v", err)
	}
	if m["model"] != "gpt-4o-mini" {
		t.Errorf("session model = %v, want gpt-4o-mini", m["model"])
	}
	if m["provider"] != "openai" {
		t.Errorf("session provider = %v, want openai", m["provider"])
	}

	// A subsequent /model (no args) should mark the new model as active.
	res2, err := cmdModel().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdModel no-args after switch: %v", err)
	}
	if !strings.Contains(res2.Display, "Modelo ativo: gpt-4o-mini") {
		t.Errorf("expected active-model marker on gpt-4o-mini: %s", res2.Display)
	}
}

func TestCmdModel_UnknownModel_Error(t *testing.T) {
	ctx := newBatch3Ctx(t)
	writeBatch3Settings(t, map[string]any{
		"ai": map[string]any{
			"provider": "deepseek",
			"model":    "deepseek-chat",
		},
	})

	res, err := cmdModel().Execute([]string{"not-a-real-model"}, ctx)
	if err != nil {
		t.Fatalf("cmdModel unknown error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "error:") {
		t.Errorf("expected error display prefix, got: %s", res.Display)
	}
	if !strings.Contains(res.Display, "desconhecido") {
		t.Errorf("expected 'desconhecido' in error: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /config
// ---------------------------------------------------------------------------

func TestCmdConfig_WithSettings(t *testing.T) {
	ctx := newBatch3Ctx(t)
	writeBatch3Settings(t, map[string]any{
		"ai": map[string]any{
			"provider": "openai",
			"model":    "gpt-4o",
		},
		"data": map[string]any{
			"dir": "/tmp/polypod-data-xyz",
		},
		"hooks": []any{
			map[string]any{"name": "h1", "enabled": true},
			map[string]any{"name": "h2", "enabled": false},
			map[string]any{"name": "h3"}, // default true
		},
		"mcp": []any{
			map[string]any{"name": "s1"},
			map[string]any{"name": "s2"},
		},
	})

	res, err := cmdConfig().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdConfig error: %v", err)
	}
	display := res.Display
	if !strings.Contains(display, "Configuracao do Polypod") {
		t.Errorf("missing header: %s", display)
	}
	if !strings.Contains(display, "openai") {
		t.Errorf("missing provider: %s", display)
	}
	if !strings.Contains(display, "gpt-4o") {
		t.Errorf("missing model: %s", display)
	}
	if !strings.Contains(display, "/tmp/polypod-data-xyz") {
		t.Errorf("missing data dir: %s", display)
	}
	// 2 explicitly enabled + 1 with default → 2 counted (only enabled=true
	// or missing 'enabled' field are counted).
	if !strings.Contains(display, "Hooks ativos:    2") {
		t.Errorf("expected 2 hooks active: %s", display)
	}
	if !strings.Contains(display, "Servidores MCP:  2") {
		t.Errorf("expected 2 MCP servers: %s", display)
	}
}

func TestCmdConfig_MissingFile_StillRenders(t *testing.T) {
	ctx := newBatch3Ctx(t)
	// Intentionally do NOT write settings.json.

	res, err := cmdConfig().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdConfig missing file error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdConfig returned empty result")
	}
	if !strings.Contains(res.Display, "Configuracao do Polypod") {
		t.Errorf("missing header: %s", res.Display)
	}
	if !strings.Contains(res.Display, "nenhum config encontrado") {
		t.Errorf("expected missing-file notice: %s", res.Display)
	}
	if !strings.Contains(res.Display, "(nao definido)") {
		t.Errorf("expected '(nao definido)' for provider/model: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /doctor
// ---------------------------------------------------------------------------

func TestCmdDoctor_HappyPath(t *testing.T) {
	ctx := newBatch3Ctx(t)
	writeBatch3Settings(t, map[string]any{
		"ai": map[string]any{
			"provider": "deepseek",
			"model":    "deepseek-chat",
		},
	})

	res, err := cmdDoctor().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdDoctor error: %v", err)
	}
	display := res.Display
	if !strings.Contains(display, "Polypod Doctor") {
		t.Errorf("missing header: %s", display)
	}
	if !strings.Contains(display, "Go runtime") {
		t.Errorf("missing Go runtime check: %s", display)
	}
	if !strings.Contains(display, "Binario") {
		t.Errorf("missing Binario check: %s", display)
	}
	if !strings.Contains(display, "Provider ativo") {
		t.Errorf("missing Provider ativo check: %s", display)
	}
	if !strings.Contains(display, "deepseek") {
		t.Errorf("expected deepseek in provider detail: %s", display)
	}
	if !strings.Contains(display, "[OK]") {
		t.Errorf("expected at least one [OK] marker: %s", display)
	}
}

func TestCmdDoctor_NoConfig_ReportsFailures(t *testing.T) {
	ctx := newBatch3Ctx(t)
	// No settings.json written → config + provider should fail.

	res, err := cmdDoctor().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdDoctor no-config error: %v", err)
	}
	display := res.Display
	if !strings.Contains(display, "[XX]") {
		t.Errorf("expected at least one [XX] failure marker: %s", display)
	}
	if !strings.Contains(display, "nenhum arquivo de configuracao") {
		t.Errorf("expected 'nenhum arquivo de configuracao' message: %s", display)
	}
	if !strings.Contains(display, "Corrija as falhas") {
		t.Errorf("expected failure footer: %s", display)
	}
}
