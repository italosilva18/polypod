// Package commands — batch10_env_test.go
//
// Tests for the batch10 env/remote/files slash commands. The /env tests
// clear the environment to a deterministic subset via t.Setenv so the
// outcome is independent of the developer's real shell state.
package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch10Env_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch10Env(reg)

	want := []string{"env", "remote-env", "remote-setup", "add-dir", "files"}
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
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
	}
}

// ---------------------------------------------------------------------------
// /env — masking of secret-looking values
// ---------------------------------------------------------------------------

func TestB10CmdEnv_MasksSecrets(t *testing.T) {
	// A POLYPOD_ var that should be shown verbatim.
	t.Setenv("POLYPOD_MODE", "development")
	// A CLAUDE_ var that should be shown verbatim.
	t.Setenv("CLAUDE_LOG_LEVEL", "info")
	// ANTHROPIC_API_KEY must be masked.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-supersecrettoken12345")
	// A generic API_KEY-ish variable must also be masked via substring match.
	t.Setenv("SOMETHING_API_KEY", "abcdefghij")

	reg := command.NewRegistry()
	RegisterBatch10Env(reg)

	cmd, ok := reg.Get("env")
	if !ok {
		t.Fatal("/env not registered")
	}
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("execute /env: %v", err)
	}
	out := res.Display

	// Non-secret POLYPOD_/CLAUDE_ values appear verbatim.
	if !strings.Contains(out, "POLYPOD_MODE") || !strings.Contains(out, "development") {
		t.Errorf("expected POLYPOD_MODE=development in output, got:\n%s", out)
	}
	if !strings.Contains(out, "CLAUDE_LOG_LEVEL") || !strings.Contains(out, "info") {
		t.Errorf("expected CLAUDE_LOG_LEVEL=info in output, got:\n%s", out)
	}

	// Secret values must be masked and never echoed verbatim.
	if strings.Contains(out, "sk-ant-supersecrettoken12345") {
		t.Errorf("ANTHROPIC_API_KEY leaked in /env output:\n%s", out)
	}
	if !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Errorf("expected ANTHROPIC_API_KEY row in /env output, got:\n%s", out)
	}
	// "sk-a" + "***" is the expected mask (first 4 chars + "***").
	if !strings.Contains(out, "sk-a***") {
		t.Errorf("expected masked value sk-a*** in /env output, got:\n%s", out)
	}

	if strings.Contains(out, "abcdefghij") {
		t.Errorf("SOMETHING_API_KEY leaked in /env output:\n%s", out)
	}
	if !strings.Contains(out, "abcd***") {
		t.Errorf("expected masked value abcd*** in /env output, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// /env — short secrets fully replaced
// ---------------------------------------------------------------------------

func TestB10CmdEnv_ShortSecretFullyMasked(t *testing.T) {
	t.Setenv("POLYPOD_TOKEN", "abc")

	reg := command.NewRegistry()
	RegisterBatch10Env(reg)

	cmd, _ := reg.Get("env")
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("execute /env: %v", err)
	}
	if strings.Contains(res.Display, " abc ") || strings.Contains(res.Display, "abc\n") {
		t.Errorf("short secret leaked in /env output:\n%s", res.Display)
	}
	if !strings.Contains(res.Display, "POLYPOD_TOKEN") {
		t.Errorf("expected POLYPOD_TOKEN row in /env output:\n%s", res.Display)
	}
	if !strings.Contains(res.Display, "***") {
		t.Errorf("expected masked value *** in /env output:\n%s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /remote-env and /remote-setup placeholders
// ---------------------------------------------------------------------------

func TestB10Placeholders(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch10Env(reg)

	cases := []struct {
		name string
		want string
	}{
		{"remote-env", "remote env sync pending wiring"},
		{"remote-setup", "remote setup pending"},
		{"files", "files listing pending session wiring"},
	}
	for _, tc := range cases {
		cmd, ok := reg.Get(tc.name)
		if !ok {
			t.Errorf("/%s not registered", tc.name)
			continue
		}
		res, err := cmd.Execute(nil, command.CommandContext{})
		if err != nil {
			t.Errorf("execute /%s: %v", tc.name, err)
			continue
		}
		if res.Display != tc.want {
			t.Errorf("/%s: expected %q, got %q", tc.name, tc.want, res.Display)
		}
	}
}

// ---------------------------------------------------------------------------
// /add-dir resolves paths via filepath.Abs
// ---------------------------------------------------------------------------

func TestB10CmdAddDir_ResolvesAbsPath(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch10Env(reg)

	cmd, ok := reg.Get("add-dir")
	if !ok {
		t.Fatal("/add-dir not registered")
	}

	// Missing arg -> error.
	res, err := cmd.Execute(nil, command.CommandContext{})
	if err != nil {
		t.Fatalf("execute /add-dir (no args): %v", err)
	}
	if !strings.HasPrefix(res.Display, "erro:") {
		t.Errorf("expected error prefix for missing arg, got: %q", res.Display)
	}

	// Relative path -> resolved to absolute.
	res, err = cmd.Execute([]string{"."}, command.CommandContext{})
	if err != nil {
		t.Fatalf("execute /add-dir .: %v", err)
	}
	if !strings.HasPrefix(res.Display, "added (pending session wiring): ") {
		t.Errorf("expected 'added (pending session wiring): ' prefix, got: %q", res.Display)
	}
	gotPath := strings.TrimPrefix(res.Display, "added (pending session wiring): ")
	if !filepath.IsAbs(gotPath) {
		t.Errorf("expected absolute path, got: %q", gotPath)
	}

	// Explicit absolute path -> echoed verbatim after Abs.
	abs := filepath.Join(t.TempDir(), "sub")
	res, err = cmd.Execute([]string{abs}, command.CommandContext{})
	if err != nil {
		t.Fatalf("execute /add-dir <abs>: %v", err)
	}
	wantAbs, _ := filepath.Abs(abs)
	if !strings.HasSuffix(res.Display, wantAbs) {
		t.Errorf("expected output to end with %q, got: %q", wantAbs, res.Display)
	}
}
