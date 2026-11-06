// Package commands — batch4_security_test.go
// Smoke tests for /hooks, /permissions, /mcp, /memory.
//
// These tests exercise the no-args and help-ish paths end-to-end, verifying
// each command registers cleanly and returns a non-nil result with a non-empty
// display string on the happy path.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newTestCtx returns a CommandContext rooted at a temp dir so the commands
// operate in isolation from the real filesystem state.
func newTestCtx(t *testing.T) command.CommandContext {
	t.Helper()
	tmp := t.TempDir()
	// Also redirect POLYPOD_CONFIG_DIR so readSessionJSON etc. from batch1
	// don't touch the real ~/.polypod.
	t.Setenv("POLYPOD_CONFIG_DIR", filepath.Join(tmp, "polypod-home"))
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	_ = os.MkdirAll(filepath.Join(tmp, "home"), 0o755)
	return command.CommandContext{
		WorkingDir: tmp,
		SessionID:  "test-session",
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch4Security_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch4Security(reg)

	want := []string{"hooks", "permissions", "mcp", "memory"}
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("command %q not registered", name)
		}
	}

	// /permissions should also resolve via its alias.
	if _, ok := reg.Get("perm"); !ok {
		t.Error("alias 'perm' not registered for /permissions")
	}
	if _, ok := reg.Get("allowed-tools"); !ok {
		t.Error("alias 'allowed-tools' not registered for /permissions")
	}
}

// ---------------------------------------------------------------------------
// /hooks
// ---------------------------------------------------------------------------

func TestCmdHooks_NoArgs_EmptyConfig(t *testing.T) {
	ctx := newTestCtx(t)
	cmd := cmdHooks()
	if cmd.Execute == nil {
		t.Fatal("cmdHooks has nil Execute")
	}
	res, err := cmd.Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdHooks Execute error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("cmdHooks returned empty result")
	}
	if !strings.Contains(res.Display, "Nenhum hook") {
		t.Errorf("expected 'Nenhum hook' message, got %q", res.Display)
	}
}

func TestCmdHooks_List_WithConfig(t *testing.T) {
	ctx := newTestCtx(t)
	// Seed a project-level settings.json with hooks.
	projDir := filepath.Join(ctx.WorkingDir, ".polypod")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{
		"hooks": {
			"PreToolUse": [{"matcher": "*", "hooks": [{"type":"command","command":"echo pre"}]}],
			"PostToolUse": [{"matcher": "*", "hooks": [
				{"type":"command","command":"echo a"},
				{"type":"command","command":"echo b"}
			]}]
		}
	}`
	if err := os.WriteFile(filepath.Join(projDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := cmdHooks().Execute([]string{"list"}, ctx)
	if err != nil {
		t.Fatalf("cmdHooks list error: %v", err)
	}
	if !strings.Contains(res.Display, "PreToolUse") {
		t.Errorf("missing PreToolUse in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "PostToolUse") {
		t.Errorf("missing PostToolUse in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "2 hook(s)") {
		t.Errorf("expected 2 hook(s) count for PostToolUse: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /permissions
// ---------------------------------------------------------------------------

func TestCmdPermissions_NoArgs(t *testing.T) {
	ctx := newTestCtx(t)
	res, err := cmdPermissions().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdPermissions error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("empty result")
	}
	if !strings.Contains(res.Display, "Modo atual") {
		t.Errorf("expected 'Modo atual' line, got %q", res.Display)
	}
	for _, key := range []string{"user", "project", "session"} {
		if !strings.Contains(res.Display, key) {
			t.Errorf("missing source %q in output: %s", key, res.Display)
		}
	}
}

func TestCmdPermissions_List_WithRules(t *testing.T) {
	ctx := newTestCtx(t)
	projDir := filepath.Join(ctx.WorkingDir, ".polypod")
	_ = os.MkdirAll(projDir, 0o755)
	settings := `{
		"mode": "plan",
		"allowed_tools": [{"pattern":"git_*"}],
		"denied_tools": ["run_command"],
		"ask_tools": []
	}`
	_ = os.WriteFile(filepath.Join(projDir, "settings.json"), []byte(settings), 0o644)

	res, err := cmdPermissions().Execute([]string{"list"}, ctx)
	if err != nil {
		t.Fatalf("cmdPermissions list error: %v", err)
	}
	if !strings.Contains(res.Display, "plan") {
		t.Errorf("expected mode 'plan' in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "git_*") {
		t.Errorf("expected rule 'git_*' in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "run_command") {
		t.Errorf("expected rule 'run_command' in output: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /mcp
// ---------------------------------------------------------------------------

func TestCmdMCP_NoArgs_NoServers(t *testing.T) {
	ctx := newTestCtx(t)
	res, err := cmdMCP().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdMCP error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("empty result")
	}
	if !strings.Contains(res.Display, "Nenhum servidor MCP") {
		t.Errorf("expected 'Nenhum servidor MCP' message: %s", res.Display)
	}
}

func TestCmdMCP_WithGlobalServers(t *testing.T) {
	ctx := newTestCtx(t)
	// Write a fake ~/.claude.json with MCP servers.
	home := os.Getenv("HOME")
	globalConfig := `{
		"mcpServers": {
			"filesystem": {"type":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"]},
			"remote": {"type":"sse","url":"https://example.com/sse"}
		}
	}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := cmdMCP().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdMCP error: %v", err)
	}
	if !strings.Contains(res.Display, "filesystem") {
		t.Errorf("expected 'filesystem' server in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "remote") {
		t.Errorf("expected 'remote' server in output: %s", res.Display)
	}
}

func TestCmdMCP_Reconnect(t *testing.T) {
	ctx := newTestCtx(t)
	// Missing arg → error result.
	res, err := cmdMCP().Execute([]string{"reconnect"}, ctx)
	if err != nil {
		t.Fatalf("cmdMCP reconnect error: %v", err)
	}
	if !strings.Contains(res.Display, "error") {
		t.Errorf("expected usage error: %s", res.Display)
	}

	// With name → pending wiring notice.
	res, err = cmdMCP().Execute([]string{"reconnect", "filesystem"}, ctx)
	if err != nil {
		t.Fatalf("cmdMCP reconnect filesystem error: %v", err)
	}
	if !strings.Contains(res.Display, "pendente de wiring") {
		t.Errorf("expected wiring notice: %s", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /memory
// ---------------------------------------------------------------------------

func TestCmdMemory_NoArgs_NoFiles(t *testing.T) {
	ctx := newTestCtx(t)
	res, err := cmdMemory().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdMemory error: %v", err)
	}
	if res == nil || res.Display == "" {
		t.Fatal("empty result")
	}
	if !strings.Contains(res.Display, "Nenhum arquivo de memoria") {
		t.Errorf("expected 'Nenhum arquivo de memoria' message: %s", res.Display)
	}
}

func TestCmdMemory_WithProjectMemoryFile(t *testing.T) {
	ctx := newTestCtx(t)
	memPath := filepath.Join(ctx.WorkingDir, ".polypod.md")
	if err := os.WriteFile(memPath, []byte("# Project memory\nThis is a note."), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := cmdMemory().Execute(nil, ctx)
	if err != nil {
		t.Fatalf("cmdMemory error: %v", err)
	}
	if !strings.Contains(res.Display, ".polypod.md") {
		t.Errorf("expected '.polypod.md' in output: %s", res.Display)
	}
	if !strings.Contains(res.Display, "1 arquivo(s) rastreado") {
		t.Errorf("expected tracking count in output: %s", res.Display)
	}
}

func TestCmdMemory_Reload(t *testing.T) {
	ctx := newTestCtx(t)
	memPath := filepath.Join(ctx.WorkingDir, ".polypod.md")
	_ = os.WriteFile(memPath, []byte("# Initial"), 0o644)

	res, err := cmdMemory().Execute([]string{"reload"}, ctx)
	if err != nil {
		t.Fatalf("cmdMemory reload error: %v", err)
	}
	if !strings.Contains(res.Display, "Memoria recarregada") {
		t.Errorf("expected reload notice: %s", res.Display)
	}
	if !strings.Contains(res.Display, "1 arquivo(s)") {
		t.Errorf("expected file count: %s", res.Display)
	}
}
