// Package commands — batch23_security_test.go
//
// Tests for the batch23 security commands. The important coverage is:
//
//   - all three names are registered on the registry and wired as CommandLocal
//   - /secret-scan and /vuln-check return their fixed Portuguese placeholders
//   - /perm-audit handles a missing file gracefully (no error, friendly msg)
//   - /perm-audit summarises a real JSONL audit file (count by Decision,
//     correct tail length, window cap at 20)
//   - /perm-audit tolerates blank lines and malformed entries without failing
//
// XDG_STATE_HOME is redirected at t.TempDir() so DefaultAuditLogPath resolves
// under the test temp dir, keeping the developer's real state directory
// untouched.
package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/approval"
	"github.com/costa/polypod/internal/command"
)

// newBatch23Ctx returns a CommandContext suitable for batch23 tests.
func newBatch23Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// redirectXDGStateHomeB23 points $XDG_STATE_HOME at a fresh temp dir and
// returns the computed audit-log path under it. The original value (and any
// surrounding $HOME override) is restored via t.Cleanup.
func redirectXDGStateHomeB23(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	prev, had := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", tmp); err != nil {
		t.Fatalf("setenv XDG_STATE_HOME: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("XDG_STATE_HOME", prev)
		} else {
			_ = os.Unsetenv("XDG_STATE_HOME")
		}
	})
	return filepath.Join(tmp, "polypod", "approval-audit.jsonl")
}

// writeAuditFileB23 writes the given entries as JSON-lines to path, creating
// parent directories as needed. Extra raw lines (e.g. blank/malformed) are
// appended verbatim after the marshaled entries.
func writeAuditFileB23(t *testing.T, path string, entries []approval.AuditEntry, rawExtras ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	for _, extra := range rawExtras {
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteByte('\n')
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch23Security_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch23Security(reg)

	want := []string{"perm-audit", "secret-scan", "vuln-check"}
	for _, name := range want {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q not registered", name)
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
		if cmd.Description == "" {
			t.Errorf("command %q: empty description", name)
		}
	}
}

// ---------------------------------------------------------------------------
// /secret-scan and /vuln-check placeholders
// ---------------------------------------------------------------------------

func TestCmdSecretScanB23_Placeholder(t *testing.T) {
	res, err := cmdSecretScanB23().Execute(nil, newBatch23Ctx("batch23-secret-scan-session"))
	if err != nil {
		t.Fatalf("cmdSecretScanB23 error: %v", err)
	}
	if res == nil || res.Display != secretScanPlaceholderB23 {
		t.Errorf("unexpected /secret-scan placeholder: got %q, want %q",
			displayOrNilB23(res), secretScanPlaceholderB23)
	}
}

func TestCmdVulnCheckB23_Placeholder(t *testing.T) {
	res, err := cmdVulnCheckB23().Execute(
		[]string{"./..."},
		newBatch23Ctx("batch23-vuln-check-session"),
	)
	if err != nil {
		t.Fatalf("cmdVulnCheckB23 error: %v", err)
	}
	if res == nil || res.Display != vulnCheckPlaceholderB23 {
		t.Errorf("unexpected /vuln-check placeholder: got %q, want %q",
			displayOrNilB23(res), vulnCheckPlaceholderB23)
	}
}

// ---------------------------------------------------------------------------
// /perm-audit — missing file
// ---------------------------------------------------------------------------

func TestCmdPermAuditB23_MissingFile(t *testing.T) {
	path := redirectXDGStateHomeB23(t)
	// Sanity: DefaultAuditLogPath must resolve under our temp dir.
	if got := approval.DefaultAuditLogPath(); got != path {
		t.Fatalf("DefaultAuditLogPath = %q, want %q", got, path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected missing file, stat err = %v", err)
	}

	res, err := cmdPermAuditB23().Execute(nil, newBatch23Ctx("batch23-perm-audit-missing"))
	if err != nil {
		t.Fatalf("cmdPermAuditB23 error: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}
	if !strings.Contains(res.Display, "Nenhum registro encontrado") {
		t.Errorf("missing-file message missing friendly text: %q", res.Display)
	}
	if !strings.Contains(res.Display, path) {
		t.Errorf("missing-file message should include path %q: %q", path, res.Display)
	}
	if strings.HasPrefix(res.Display, "error:") {
		t.Errorf("missing file should not be an error result: %q", res.Display)
	}
}

// ---------------------------------------------------------------------------
// /perm-audit — real JSONL summary
// ---------------------------------------------------------------------------

func TestCmdPermAuditB23_SummariesAndTail(t *testing.T) {
	path := redirectXDGStateHomeB23(t)

	// Build 25 entries so we exercise the window-cap (20) branch too.
	// Decisions alternate allowed/denied/pending with a known distribution
	// across the last 20 entries (indices 5..24).
	base := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	entries := make([]approval.AuditEntry, 0, 25)
	for i := 0; i < 25; i++ {
		var decision string
		switch i % 3 {
		case 0:
			decision = "allowed"
		case 1:
			decision = "denied"
		default:
			decision = "pending"
		}
		entries = append(entries, approval.AuditEntry{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			ToolName:  "tool_" + string(rune('a'+(i%5))),
			RiskLevel: "medium",
			Decision:  decision,
			Reason:    "reason " + string(rune('0'+i%10)),
		})
	}
	writeAuditFileB23(t, path, entries)

	res, err := cmdPermAuditB23().Execute(nil, newBatch23Ctx("batch23-perm-audit-real"))
	if err != nil {
		t.Fatalf("cmdPermAuditB23 error: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}
	display := res.Display

	// Header facts.
	if !strings.Contains(display, "Auditoria de permissoes") {
		t.Errorf("missing header: %q", display)
	}
	if !strings.Contains(display, "Entradas totais: 25") {
		t.Errorf("expected total=25 in output: %q", display)
	}
	if !strings.Contains(display, "janela analisada: 20") {
		t.Errorf("expected window=20 in output: %q", display)
	}

	// Decision counts over entries 5..24:
	//   i mod 3: 5=2,6=0,7=1,8=2,9=0,10=1,11=2,12=0,13=1,14=2,
	//            15=0,16=1,17=2,18=0,19=1,20=2,21=0,22=1,23=2,24=0
	//   -> allowed = 7, denied = 6, pending = 7
	for _, want := range []string{
		"allowed    7",
		"denied     6",
		"pending    7",
	} {
		if !strings.Contains(display, want) {
			t.Errorf("missing decision summary %q in:\n%s", want, display)
		}
	}

	// Last 5 entries should be printed. The last entry is index 24
	// (tool_a at 12:24:00).
	if !strings.Contains(display, "Ultimas 5 entradas:") {
		t.Errorf("missing tail header: %q", display)
	}
	if !strings.Contains(display, "[2026-04-08 12:24:00]") {
		t.Errorf("missing last-entry timestamp: %q", display)
	}
	if !strings.Contains(display, "tool_a") {
		t.Errorf("missing last-entry tool name: %q", display)
	}
	// Second-to-last (index 23) should also be in the tail.
	if !strings.Contains(display, "[2026-04-08 12:23:00]") {
		t.Errorf("missing penultimate timestamp: %q", display)
	}
	// Index 19 is NOT in the tail of 5 (range 20..24).
	if strings.Contains(display, "[2026-04-08 12:19:00]") {
		t.Errorf("tail should not include index 19: %q", display)
	}
}

// ---------------------------------------------------------------------------
// /perm-audit — tolerates blank lines and malformed JSON
// ---------------------------------------------------------------------------

func TestCmdPermAuditB23_SkipsMalformedLines(t *testing.T) {
	path := redirectXDGStateHomeB23(t)

	entries := []approval.AuditEntry{
		{
			Timestamp: time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
			ToolName:  "fs_write",
			RiskLevel: "high",
			Decision:  "allowed",
		},
		{
			Timestamp: time.Date(2026, 4, 1, 9, 5, 0, 0, time.UTC),
			ToolName:  "shell_exec",
			RiskLevel: "high",
			Decision:  "denied",
			Reason:    "rm -rf blocked",
		},
	}
	writeAuditFileB23(t, path, entries,
		"",              // blank line
		"not a json!!!", // malformed
		"{bad json",     // malformed
	)

	res, err := cmdPermAuditB23().Execute(nil, newBatch23Ctx("batch23-perm-audit-malformed"))
	if err != nil {
		t.Fatalf("cmdPermAuditB23 error: %v", err)
	}
	if res == nil {
		t.Fatalf("nil result")
	}
	display := res.Display

	if !strings.Contains(display, "Entradas totais: 2") {
		t.Errorf("expected total=2 (malformed skipped): %q", display)
	}
	if !strings.Contains(display, "Linhas invalidas ignoradas: 2") {
		t.Errorf("expected 2 invalid lines reported: %q", display)
	}
	if !strings.Contains(display, "allowed    1") {
		t.Errorf("expected allowed=1 in output: %q", display)
	}
	if !strings.Contains(display, "denied     1") {
		t.Errorf("expected denied=1 in output: %q", display)
	}
	if !strings.Contains(display, "fs_write") {
		t.Errorf("missing tool fs_write: %q", display)
	}
	if !strings.Contains(display, "shell_exec") {
		t.Errorf("missing tool shell_exec: %q", display)
	}
	if !strings.Contains(display, `motivo="rm -rf blocked"`) {
		t.Errorf("missing reason rendering: %q", display)
	}
}

// displayOrNilB23 safely extracts res.Display for test error messages.
func displayOrNilB23(res *command.CommandResult) string {
	if res == nil {
		return "<nil>"
	}
	return res.Display
}
