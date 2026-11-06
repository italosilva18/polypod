// Package commands — batch18_sandbox_test.go
//
// Smoke tests for the batch18 sandbox-toggle plus IDE / chrome / desktop /
// mobile placeholder commands. The /sandbox-toggle test exercises the real
// session-JSON round trip; the placeholder tests just lock the exact
// English placeholder strings the task spec requires.
package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// withTempConfigHome redirects POLYPOD_CONFIG_DIR to a unique tmp dir so
// session JSON writes from /sandbox-toggle land in a sandbox the test
// fully owns. Mirrors the helper used elsewhere in the package.
func withTempConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("POLYPOD_CONFIG_DIR", dir)
	return dir
}

// newBatch18Ctx returns a CommandContext suitable for batch18 tests.
func newBatch18Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch18Sandbox_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch18Sandbox(reg)

	for _, name := range []string{"sandbox-toggle", "ide", "chrome", "desktop", "mobile"} {
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
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// /sandbox-toggle — round-trip via session JSON
// ---------------------------------------------------------------------------

func TestCmdSandboxToggleB18_TogglesSessionFlag(t *testing.T) {
	dir := withTempConfigHome(t)
	ctx := newBatch18Ctx("batch18-toggle-session")

	// Initial toggle: from unset (false) to true.
	res, err := cmdSandboxToggleB18().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdSandboxToggleB18 error on first call: %v", err)
	}
	if res == nil || !strings.Contains(res.Display, "LIGADO") {
		t.Errorf("expected LIGADO after first toggle, got %q", res.Display)
	}
	if !readSandboxFlag(ctx.SessionID) {
		t.Error("readSandboxFlag should be true after first toggle")
	}

	// Confirm the JSON file actually landed under POLYPOD_CONFIG_DIR.
	wantPath := filepath.Join(dir, "sessions", ctx.SessionID+".json")
	if _, err := readSessionJSON(ctx.SessionID); err != nil {
		t.Errorf("session JSON not found at %s: %v", wantPath, err)
	}

	// Second toggle: from true back to false.
	res, err = cmdSandboxToggleB18().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdSandboxToggleB18 error on second call: %v", err)
	}
	if res == nil || !strings.Contains(res.Display, "DESLIGADO") {
		t.Errorf("expected DESLIGADO after second toggle, got %q", res.Display)
	}
	if readSandboxFlag(ctx.SessionID) {
		t.Error("readSandboxFlag should be false after second toggle")
	}
}

func TestCmdSandboxToggleB18_ExplicitOnOff(t *testing.T) {
	withTempConfigHome(t)
	ctx := newBatch18Ctx("batch18-explicit-session")

	// Explicit "on".
	res, err := cmdSandboxToggleB18().Execute([]string{"on"}, ctx)
	if err != nil {
		t.Fatalf("explicit on error: %v", err)
	}
	if !strings.Contains(res.Display, "LIGADO") {
		t.Errorf("explicit 'on' should yield LIGADO, got %q", res.Display)
	}
	if !readSandboxFlag(ctx.SessionID) {
		t.Error("flag should be true after explicit on")
	}

	// Explicit "off".
	res, err = cmdSandboxToggleB18().Execute([]string{"off"}, ctx)
	if err != nil {
		t.Fatalf("explicit off error: %v", err)
	}
	if !strings.Contains(res.Display, "DESLIGADO") {
		t.Errorf("explicit 'off' should yield DESLIGADO, got %q", res.Display)
	}
	if readSandboxFlag(ctx.SessionID) {
		t.Error("flag should be false after explicit off")
	}

	// Invalid value should be reported as an error result, not panic.
	res, err = cmdSandboxToggleB18().Execute([]string{"banana"}, ctx)
	if err != nil {
		t.Fatalf("invalid arg should not return Go error, got %v", err)
	}
	if !strings.Contains(res.Display, "valor invalido") {
		t.Errorf("invalid arg should mention 'valor invalido', got %q", res.Display)
	}
}

func TestCmdSandboxToggleB18_NoSession(t *testing.T) {
	withTempConfigHome(t)
	ctx := newBatch18Ctx("") // empty session

	res, err := cmdSandboxToggleB18().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("empty-session call should not return Go error, got %v", err)
	}
	if !strings.Contains(res.Display, "sessao indisponivel") {
		t.Errorf("expected 'sessao indisponivel' message, got %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// Placeholder commands — exact strings from the task spec
// ---------------------------------------------------------------------------

func TestCmdIDEB18_Placeholder(t *testing.T) {
	res, err := cmdIDEB18().Execute(nil, newBatch18Ctx("batch18-ide-session"))
	if err != nil {
		t.Fatalf("cmdIDEB18 error: %v", err)
	}
	if res == nil || res.Display != "IDE handoff pending wiring" {
		t.Errorf("unexpected /ide placeholder: %q", res.Display)
	}
}

func TestCmdChromeB18_Placeholder(t *testing.T) {
	res, err := cmdChromeB18().Execute(nil, newBatch18Ctx("batch18-chrome-session"))
	if err != nil {
		t.Fatalf("cmdChromeB18 error: %v", err)
	}
	if res == nil || res.Display != "chrome integration pending wiring" {
		t.Errorf("unexpected /chrome placeholder: %q", res.Display)
	}
}

func TestCmdDesktopB18_Placeholder(t *testing.T) {
	res, err := cmdDesktopB18().Execute(nil, newBatch18Ctx("batch18-desktop-session"))
	if err != nil {
		t.Fatalf("cmdDesktopB18 error: %v", err)
	}
	if res == nil || res.Display != "desktop app handoff pending wiring" {
		t.Errorf("unexpected /desktop placeholder: %q", res.Display)
	}
}

func TestCmdMobileB18_Placeholder(t *testing.T) {
	res, err := cmdMobileB18().Execute(nil, newBatch18Ctx("batch18-mobile-session"))
	if err != nil {
		t.Fatalf("cmdMobileB18 error: %v", err)
	}
	if res == nil || res.Display != "mobile handoff pending wiring" {
		t.Errorf("unexpected /mobile placeholder: %q", res.Display)
	}
}
