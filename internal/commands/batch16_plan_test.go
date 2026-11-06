// Package commands — batch16_plan_test.go
//
// Tests for batch16 planning slash commands. Tests are hermetic: they
// never touch the real $HOME and always redirect to t.TempDir().
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/agent"
	"github.com/costa/polypod/internal/command"
)

// b16TestCtx returns a CommandContext scoped to a fresh temp dir. It also
// points $HOME and $POLYPOD_CONFIG_DIR at that dir so helpers which probe
// the user's home directory (b16KeybindingsPath in particular) see a
// predictable state.
func b16TestCtx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("POLYPOD_CONFIG_DIR", tmp)
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "batch16-test",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch16Plan_RegistersPlanOnly(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch16Plan(reg)

	cmd, ok := reg.Get("plan")
	if !ok {
		t.Fatalf("command %q not registered", "plan")
	}
	if cmd.Execute == nil {
		t.Errorf("command %q has nil Execute handler", "plan")
	}
	if cmd.Type != command.CommandLocal {
		t.Errorf("command %q: expected CommandLocal, got %q", "plan", cmd.Type)
	}
	if cmd.Source != command.SourceBuiltin {
		t.Errorf("command %q: expected SourceBuiltin, got %q", "plan", cmd.Source)
	}

	// /agents and /keybindings must NOT be registered by batch16 — they
	// are owned by batch1. A fresh registry should therefore not see them.
	if _, ok := reg.Get("agents"); ok {
		t.Errorf("batch16 must not register /agents (owned by batch1)")
	}
	if _, ok := reg.Get("keybindings"); ok {
		t.Errorf("batch16 must not register /keybindings (owned by batch1)")
	}
}

func TestRegisterBatch16Plan_DoesNotClashWithBatch1(t *testing.T) {
	// Registering batch1 first and then batch16 must not overwrite any of
	// the batch1 handlers. This is the key invariant: batch16 is
	// additive-only with respect to batch1's /agents and /keybindings.
	reg := command.NewRegistry()
	RegisterBatch1(reg)

	preAgents, agentsOK := reg.Get("agents")
	preKeybindings, kbOK := reg.Get("keybindings")
	if !agentsOK || !kbOK {
		t.Fatalf("sanity check: batch1 should already own /agents and /keybindings")
	}

	RegisterBatch16Plan(reg)

	postAgents, _ := reg.Get("agents")
	postKeybindings, _ := reg.Get("keybindings")

	if postAgents != preAgents {
		t.Errorf("/agents command was overwritten by batch16 — collision!")
	}
	if postKeybindings != preKeybindings {
		t.Errorf("/keybindings command was overwritten by batch16 — collision!")
	}

	if _, ok := reg.Get("plan"); !ok {
		t.Errorf("/plan not registered after RegisterBatch16Plan")
	}
}

// ---------------------------------------------------------------------------
// /plan
// ---------------------------------------------------------------------------

func TestB16CmdPlan_ReturnsPortuguesePlaceholder(t *testing.T) {
	ctx := b16TestCtx(t)

	res, err := b16CmdPlan().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatalf("expected non-empty Display")
	}
	if res.Display != b16PlanMsg {
		t.Errorf("placeholder drift: got %q, want %q", res.Display, b16PlanMsg)
	}
	if !strings.Contains(strings.ToLower(res.Display), "plano") {
		t.Errorf("expected Portuguese phrasing (\"plano\"), got: %s", res.Display)
	}
	if !strings.Contains(strings.ToLower(res.Display), "pendente") {
		t.Errorf("expected placeholder to mention pending wiring, got: %s", res.Display)
	}
}

func TestB16CmdPlan_IgnoresExtraArgs(t *testing.T) {
	ctx := b16TestCtx(t)
	for _, args := range [][]string{nil, {}, {"now"}, {"foo", "bar"}} {
		res, err := b16CmdPlan().Execute(args, ctx)
		if err != nil {
			t.Errorf("args=%v: unexpected error: %v", args, err)
			continue
		}
		if res == nil || res.Display != b16PlanMsg {
			t.Errorf("args=%v: placeholder should be stable, got: %+v", args, res)
		}
	}
}

// ---------------------------------------------------------------------------
// /agents (batch16 variant)
// ---------------------------------------------------------------------------

func TestB16ListAgents_FallbackStatic(t *testing.T) {
	names := b16ListAgents(nil)
	if len(names) != len(b16StaticAgents) {
		t.Fatalf("expected %d fallback agents, got %d: %v", len(b16StaticAgents), len(names), names)
	}
	// All four canonical personas must be present.
	wanted := map[string]bool{
		"default":              false,
		"verification-persona": false,
		"explore-persona":      false,
		"plan-persona":         false,
	}
	for _, n := range names {
		if _, ok := wanted[n]; ok {
			wanted[n] = true
		}
	}
	for k, seen := range wanted {
		if !seen {
			t.Errorf("expected fallback agent %q in %v", k, names)
		}
	}
}

func TestB16ListAgents_PrefersLiveRegistry(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Register(&agent.Agent{Name: "alpha"})
	reg.Register(&agent.Agent{Name: "beta"})

	names := b16ListAgents(reg)
	if len(names) != 2 {
		t.Fatalf("expected 2 live agents, got %d: %v", len(names), names)
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("expected sorted [alpha beta], got %v", names)
	}

	// Verify the static fallback is NOT used when a live registry is
	// supplied.
	for _, n := range names {
		if n == "default" || n == "verification-persona" {
			t.Errorf("live registry result should not contain fallback name %q", n)
		}
	}
}

func TestB16CmdAgents_SmokeRendersPortuguese(t *testing.T) {
	ctx := b16TestCtx(t)
	res, err := b16CmdAgents().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatalf("expected non-empty Display")
	}
	if !strings.Contains(res.Display, "Agentes disponíveis") {
		t.Errorf("expected Portuguese header, got: %s", res.Display)
	}
	// Every canonical persona from the fallback list must be rendered.
	for _, name := range b16StaticAgents {
		if !strings.Contains(res.Display, name) {
			t.Errorf("expected agent %q in output, got: %s", name, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// /keybindings (batch16 variant)
// ---------------------------------------------------------------------------

func TestB16CmdKeybindings_MissingFileReturnsFallback(t *testing.T) {
	ctx := b16TestCtx(t)
	// t.Setenv("HOME", tmp) in b16TestCtx guarantees ~/.claude/keybindings.json
	// does not exist inside the hermetic tmp dir.

	res, err := b16CmdKeybindings().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatalf("expected non-empty Display")
	}
	if res.Display != b16NoKeybindingsMsg {
		t.Errorf("expected fallback %q, got %q", b16NoKeybindingsMsg, res.Display)
	}
}

func TestB16CmdKeybindings_ReadsExistingFile(t *testing.T) {
	ctx := b16TestCtx(t)

	// Write a fake ~/.claude/keybindings.json inside the hermetic HOME.
	home := os.Getenv("HOME")
	kbDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(kbDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	payload := `{"bindings": {"ctrl+p": "plan"}}`
	kbPath := filepath.Join(kbDir, "keybindings.json")
	if err := os.WriteFile(kbPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := b16CmdKeybindings().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatalf("expected non-empty Display")
	}
	if !strings.Contains(res.Display, "ctrl+p") {
		t.Errorf("expected keybinding payload in output, got: %s", res.Display)
	}
	if !strings.Contains(res.Display, "Keybindings customizadas") {
		t.Errorf("expected Portuguese header, got: %s", res.Display)
	}
	if res.Display == b16NoKeybindingsMsg {
		t.Errorf("should not return fallback when file exists")
	}
}

func TestB16KeybindingsPath_HonoursHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got := b16KeybindingsPath()
	want := filepath.Join(tmp, ".claude", "keybindings.json")
	if got != want {
		t.Errorf("b16KeybindingsPath() = %q, want %q", got, want)
	}
}
