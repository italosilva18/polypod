// Package commands — batch11_theme_test.go
//
// Tests for the batch11 theme/preference slash commands. Each test points
// XDG_STATE_HOME at a t.TempDir() so the on-disk JSON layout
// ($XDG_STATE_HOME/polypod/sessions/<id>.json) is exercised end-to-end
// without touching the developer's real ~/.local/state.
package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// b11TestCtx returns a CommandContext with a deterministic session id and
// also redirects XDG_STATE_HOME to a temp dir so persistence is sandboxed.
func b11TestCtx(t *testing.T, sessionID string) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	// Also redirect macOS/Windows fallbacks so the test stays sandboxed on
	// any GOOS we may be running on.
	t.Setenv("HOME", tmp)
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localappdata"))
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  sessionID,
	}
}

// readPersistedSession is a tiny test helper that loads the on-disk JSON
// blob written by the batch11 commands.
func readPersistedSession(t *testing.T, sessionID string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(b11SessionPath(sessionID))
	if err != nil {
		t.Fatalf("expected session file at %s: %v", b11SessionPath(sessionID), err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON in session file: %v", err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch11Theme_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch11Theme(reg)

	want := []string{"effort", "output-style", "color", "theme", "good-claude", "passes"}
	for _, name := range want {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Errorf("command %q not registered", name)
			continue
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute handler", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
	}
}

// ---------------------------------------------------------------------------
// /effort
// ---------------------------------------------------------------------------

func TestB11CmdEffort_PersistsValidLevel(t *testing.T) {
	ctx := b11TestCtx(t, "sess-effort-1")

	res, err := b11CmdEffort().Execute([]string{"high"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "high") {
		t.Errorf("expected output to mention level, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-effort-1")
	if got, _ := m["effort"].(string); got != "high" {
		t.Errorf("effort = %q, want high", got)
	}
}

func TestB11CmdEffort_MedAlias(t *testing.T) {
	ctx := b11TestCtx(t, "sess-effort-2")

	res, err := b11CmdEffort().Execute([]string{"medium"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("medium should be accepted as alias for med, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-effort-2")
	if got, _ := m["effort"].(string); got != "med" {
		t.Errorf("effort = %q, want med", got)
	}
}

func TestB11CmdEffort_InvalidLevel(t *testing.T) {
	ctx := b11TestCtx(t, "sess-effort-3")

	res, err := b11CmdEffort().Execute([]string{"ultra"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error result for invalid level, got: %s", res.Display)
	}
	if _, err := os.Stat(b11SessionPath("sess-effort-3")); !os.IsNotExist(err) {
		t.Errorf("session file should not have been created on invalid input, got err=%v", err)
	}
}

// ---------------------------------------------------------------------------
// /output-style
// ---------------------------------------------------------------------------

func TestB11CmdOutputStyle_PersistsValidStyle(t *testing.T) {
	ctx := b11TestCtx(t, "sess-style")

	res, err := b11CmdOutputStyle().Execute([]string{"verbose"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "verbose") {
		t.Errorf("expected output to mention style, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-style")
	if got, _ := m["output_style"].(string); got != "verbose" {
		t.Errorf("output_style = %q, want verbose", got)
	}
}

// ---------------------------------------------------------------------------
// /color
// ---------------------------------------------------------------------------

func TestB11CmdColor_PersistsValidMode(t *testing.T) {
	ctx := b11TestCtx(t, "sess-color")

	res, err := b11CmdColor().Execute([]string{"off"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "off") {
		t.Errorf("expected output to mention mode, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-color")
	if got, _ := m["color"].(string); got != "off" {
		t.Errorf("color = %q, want off", got)
	}
}

// ---------------------------------------------------------------------------
// /theme
// ---------------------------------------------------------------------------

func TestB11CmdTheme_PersistsValidTheme(t *testing.T) {
	ctx := b11TestCtx(t, "sess-theme")

	res, err := b11CmdTheme().Execute([]string{"dark"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "dark") {
		t.Errorf("expected output to mention theme, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-theme")
	if got, _ := m["theme"].(string); got != "dark" {
		t.Errorf("theme = %q, want dark", got)
	}
}

// ---------------------------------------------------------------------------
// /good-claude
// ---------------------------------------------------------------------------

func TestB11CmdGoodClaude_TogglesFlag(t *testing.T) {
	ctx := b11TestCtx(t, "sess-good")

	// First call: false -> true.
	res, err := b11CmdGoodClaude().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "ativado") {
		t.Errorf("expected first toggle to activate, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-good")
	if v, _ := m["safety_focused"].(bool); !v {
		t.Errorf("safety_focused should be true after first toggle, got %v", v)
	}

	// Second call: true -> false.
	res, err = b11CmdGoodClaude().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "desativado") {
		t.Errorf("expected second toggle to deactivate, got: %s", res.Display)
	}
	m = readPersistedSession(t, "sess-good")
	if v, _ := m["safety_focused"].(bool); v {
		t.Errorf("safety_focused should be false after second toggle, got %v", v)
	}
}

// ---------------------------------------------------------------------------
// /passes
// ---------------------------------------------------------------------------

func TestB11CmdPasses_PersistsPositiveInt(t *testing.T) {
	ctx := b11TestCtx(t, "sess-passes")

	res, err := b11CmdPasses().Execute([]string{"7"}, ctx)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(res.Display, "7") {
		t.Errorf("expected output to mention count, got: %s", res.Display)
	}
	m := readPersistedSession(t, "sess-passes")
	// JSON numbers decode as float64.
	if got, _ := m["max_passes"].(float64); got != 7 {
		t.Errorf("max_passes = %v, want 7", got)
	}
}

func TestB11CmdPasses_RejectsNonPositive(t *testing.T) {
	ctx := b11TestCtx(t, "sess-passes-bad")

	for _, bad := range []string{"0", "-3", "abc"} {
		res, err := b11CmdPasses().Execute([]string{bad}, ctx)
		if err != nil {
			t.Fatalf("execute error for %q: %v", bad, err)
		}
		if !strings.HasPrefix(res.Display, "erro:") {
			t.Errorf("expected error result for %q, got: %s", bad, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// "session id not available" path
// ---------------------------------------------------------------------------

func TestB11_NoSessionID_NotPersisted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	ctx := command.CommandContext{WorkingDir: tmp, SessionID: ""}

	cases := []struct {
		name string
		run  func() (*command.CommandResult, error)
	}{
		{"effort", func() (*command.CommandResult, error) { return b11CmdEffort().Execute([]string{"low"}, ctx) }},
		{"output-style", func() (*command.CommandResult, error) {
			return b11CmdOutputStyle().Execute([]string{"compact"}, ctx)
		}},
		{"color", func() (*command.CommandResult, error) { return b11CmdColor().Execute([]string{"auto"}, ctx) }},
		{"theme", func() (*command.CommandResult, error) { return b11CmdTheme().Execute([]string{"auto"}, ctx) }},
		{"good-claude", func() (*command.CommandResult, error) { return b11CmdGoodClaude().Execute(nil, ctx) }},
		{"passes", func() (*command.CommandResult, error) { return b11CmdPasses().Execute([]string{"3"}, ctx) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.run()
			if err != nil {
				t.Fatalf("execute error: %v", err)
			}
			if !strings.Contains(res.Display, b11NotPersistedMsg) {
				t.Errorf("expected %q in output, got: %s", b11NotPersistedMsg, res.Display)
			}
		})
	}

	// And nothing should have been written for the empty session id.
	dir := b11SessionsDir()
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Errorf("expected no session files for empty session id, found %d entries in %s", len(entries), dir)
	}
}
