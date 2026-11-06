// Package commands — batch12_export_test.go
//
// Tests for the batch12 export/copy/diff/rewind/rename slash commands.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch12Export_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch12Export(reg)

	want := []string{"export", "copy", "diff", "rewind", "rename"}
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
// /export
// ---------------------------------------------------------------------------

func TestB12CmdExport_Placeholder(t *testing.T) {
	cmd := b12CmdExport()

	// No args — should still return the placeholder with the <path> marker.
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "session export pending wiring") {
		t.Errorf("missing placeholder text: %q", res.Display)
	}
	if !strings.Contains(res.Display, "<path>") {
		t.Errorf("expected default <path> marker, got: %q", res.Display)
	}

	// With an explicit path — should echo it.
	res, err = cmd.Execute([]string{"/tmp/session.json"}, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "/tmp/session.json") {
		t.Errorf("expected path in output, got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /copy
// ---------------------------------------------------------------------------

func TestB12CmdCopy_Placeholder(t *testing.T) {
	cmd := b12CmdCopy()
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Display != "copy last N messages pending wiring" {
		t.Errorf("unexpected display: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /diff
// ---------------------------------------------------------------------------

func TestB12CmdDiff_UsageWithoutArgs(t *testing.T) {
	cmd := b12CmdDiff()
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "uso:") {
		t.Errorf("expected usage message, got: %q", res.Display)
	}

	// One arg only: also usage.
	res, _ = cmd.Execute([]string{"only-one"}, command.CommandContext{})
	if !strings.Contains(res.Display, "uso:") {
		t.Errorf("expected usage with single arg, got: %q", res.Display)
	}
}

func TestB12CmdDiff_RunsDiffOnRealFiles(t *testing.T) {
	// Skip if `diff` is not available on the test host.
	tmp := t.TempDir()
	f1 := filepath.Join(tmp, "a.txt")
	f2 := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(f1, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(f2, []byte("hello\nbrave new world\n"), 0o600); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	cmd := b12CmdDiff()
	res, err := cmd.Execute([]string{f1, f2}, command.CommandContext{WorkingDir: tmp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A unified diff should include either the "---"/"+++" file headers or
	// the altered line. If `diff` is missing, b12Err prefixes with "erro:".
	if strings.HasPrefix(res.Display, "erro:") {
		t.Skipf("diff binary unavailable or errored: %s", res.Display)
	}
	if !strings.Contains(res.Display, "brave new world") {
		t.Errorf("expected diff output to contain changed line, got: %q", res.Display)
	}
}

func TestB12CmdDiff_IdenticalFiles(t *testing.T) {
	tmp := t.TempDir()
	f1 := filepath.Join(tmp, "a.txt")
	f2 := filepath.Join(tmp, "b.txt")
	content := []byte("same\ncontent\n")
	if err := os.WriteFile(f1, content, 0o600); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(f2, content, 0o600); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	cmd := b12CmdDiff()
	res, err := cmd.Execute([]string{f1, f2}, command.CommandContext{WorkingDir: tmp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasPrefix(res.Display, "erro:") {
		t.Skipf("diff binary unavailable: %s", res.Display)
	}
	if !strings.Contains(res.Display, "identicos") {
		t.Errorf("expected 'identicos' for matching files, got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /rewind
// ---------------------------------------------------------------------------

func TestB12CmdRewind_DefaultAndExplicit(t *testing.T) {
	cmd := b12CmdRewind()

	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "rewind 1 turns pending wiring") {
		t.Errorf("expected default rewind=1, got: %q", res.Display)
	}

	res, err = cmd.Execute([]string{"5"}, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "rewind 5 turns pending wiring") {
		t.Errorf("expected rewind=5, got: %q", res.Display)
	}

	// Invalid: non-integer and non-positive.
	res, _ = cmd.Execute([]string{"abc"}, command.CommandContext{})
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error for non-integer, got: %q", res.Display)
	}
	res, _ = cmd.Execute([]string{"-3"}, command.CommandContext{})
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error for non-positive, got: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /rename
// ---------------------------------------------------------------------------

func TestB12CmdRename_UsageWithoutArgs(t *testing.T) {
	cmd := b12CmdRename()
	res, err := cmd.Execute(nil, command.CommandContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") || !strings.Contains(res.Display, "uso:") {
		t.Errorf("expected usage error, got: %q", res.Display)
	}
}

func TestB12CmdRename_WithSession(t *testing.T) {
	cmd := b12CmdRename()
	res, err := cmd.Execute([]string{"novo", "nome"}, command.CommandContext{SessionID: "sess-42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "sess-42") {
		t.Errorf("expected session id in output, got: %q", res.Display)
	}
	if !strings.Contains(res.Display, "novo nome") {
		t.Errorf("expected joined new name, got: %q", res.Display)
	}
}

func TestB12CmdRename_WithoutSessionID(t *testing.T) {
	cmd := b12CmdRename()
	res, err := cmd.Execute([]string{"minha-sessao"}, command.CommandContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Display, "nao persistida") {
		t.Errorf("expected 'nao persistida' notice, got: %q", res.Display)
	}
	if !strings.Contains(res.Display, "minha-sessao") {
		t.Errorf("expected requested name in output, got: %q", res.Display)
	}
}
