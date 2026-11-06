package approval

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/costa/polypod/internal/permissions"
)

// -------------------------------------------------------------------
// FileAuditLogger
// -------------------------------------------------------------------

func TestFileAuditLogger_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	defer logger.Close()

	entries := []AuditEntry{
		{
			ToolName:  "write_file",
			RiskLevel: "medium",
			Decision:  "denied",
			Reason:    "requires explicit override",
			Input:     map[string]any{"file_path": "/tmp/x.txt"},
		},
		{
			ToolName:  "bash",
			RiskLevel: "critical",
			Decision:  "denied",
			Reason:    "critical risk auto-denied in headless mode",
			Input:     map[string]any{"command": "rm -rf /"},
		},
	}

	for _, e := range entries {
		logger.Log(e)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read the file back and verify it is valid JSON-lines.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()

	var decoded []AuditEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var e AuditEntry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("unmarshal line %q: %v", string(line), err)
		}
		decoded = append(decoded, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if len(decoded) != len(entries) {
		t.Fatalf("decoded %d entries, want %d", len(decoded), len(entries))
	}

	if decoded[0].ToolName != "write_file" || decoded[0].RiskLevel != "medium" {
		t.Errorf("entry[0] = %+v", decoded[0])
	}
	if decoded[1].ToolName != "bash" || decoded[1].Decision != "denied" {
		t.Errorf("entry[1] = %+v", decoded[1])
	}
	if decoded[0].Timestamp.IsZero() {
		t.Error("entry[0].Timestamp should have been auto-populated")
	}
}

func TestFileAuditLogger_RequiresNonEmptyPath(t *testing.T) {
	if _, err := NewFileAuditLogger(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestFileAuditLogger_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}
	defer logger.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Only the owner bits matter on some filesystems; check that
	// group/other have no permissions.
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		t.Errorf("audit file mode = %o, want 0600-ish (no group/other perms)", mode)
	}
}

func TestFileAuditLogger_ConcurrentLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	logger, err := NewFileAuditLogger(path)
	if err != nil {
		t.Fatalf("NewFileAuditLogger: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logger.Log(AuditEntry{
				ToolName:  "tool",
				RiskLevel: "low",
				Decision:  "allowed",
				Reason:    "test",
			})
		}(i)
	}
	wg.Wait()
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	// Each line must be valid JSON — interleaved writes would corrupt.
	for i, line := range lines {
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
		}
	}
}

// -------------------------------------------------------------------
// NoopAuditLogger
// -------------------------------------------------------------------

func TestNoopAuditLogger_Inert(t *testing.T) {
	var l AuditLogger = NoopAuditLogger{}
	// Should not panic, no side effect observable.
	l.Log(AuditEntry{ToolName: "x"})
}

// -------------------------------------------------------------------
// Global logger swap
// -------------------------------------------------------------------

// capturingAuditLogger records everything it receives for assertions.
type capturingAuditLogger struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (c *capturingAuditLogger) Log(e AuditEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *capturingAuditLogger) snapshot() []AuditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]AuditEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

// withCapture installs a capturing logger for the duration of the test
// and restores the previous logger on cleanup.
func withCapture(t *testing.T) *capturingAuditLogger {
	t.Helper()
	prev := CurrentAuditLogger()
	cap := &capturingAuditLogger{}
	SetAuditLogger(cap)
	t.Cleanup(func() { SetAuditLogger(prev) })
	return cap
}

func TestSetAuditLogger_NilResetsToNoop(t *testing.T) {
	prev := CurrentAuditLogger()
	t.Cleanup(func() { SetAuditLogger(prev) })

	SetAuditLogger(nil)
	if _, ok := CurrentAuditLogger().(NoopAuditLogger); !ok {
		t.Errorf("SetAuditLogger(nil) should install NoopAuditLogger, got %T", CurrentAuditLogger())
	}
}

func TestDefaultAuditLogPath_NotEmpty(t *testing.T) {
	if DefaultAuditLogPath() == "" {
		t.Error("DefaultAuditLogPath returned empty string")
	}
}

func TestDefaultAuditLogPath_XDGOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state-fake")
	got := DefaultAuditLogPath()
	want := filepath.Join("/tmp/xdg-state-fake", "polypod", "approval-audit.jsonl")
	if got != want {
		t.Errorf("DefaultAuditLogPath = %q, want %q", got, want)
	}
}

// -------------------------------------------------------------------
// HeadlessApprovalHandler ↔ audit wiring
// -------------------------------------------------------------------

func TestHeadlessApprovalHandler_Medium_RequiresConfirmation_DeniedAndAudited(t *testing.T) {
	cap := withCapture(t)

	h := NewHeadlessApprovalHandler(HeadlessPolicy{
		AllowMedium:                     true,
		AllowMediumRequiresConfirmation: true,
	})
	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Args:     map[string]any{"file_path": "/tmp/x.txt"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}

	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if resp.Allowed {
		t.Fatal("medium risk with AllowMediumRequiresConfirmation=true should be denied")
	}
	if !strings.Contains(resp.Feedback, "MediumRisk auto-approval requires explicit override") {
		t.Errorf("feedback = %q, want override notice", resp.Feedback)
	}

	entries := cap.snapshot()
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}
	e := entries[0]
	if e.ToolName != "write_file" {
		t.Errorf("entry.ToolName = %q", e.ToolName)
	}
	if e.Decision != "denied" {
		t.Errorf("entry.Decision = %q, want denied", e.Decision)
	}
	if e.RiskLevel != permissions.ToolRiskMedium.String() {
		t.Errorf("entry.RiskLevel = %q", e.RiskLevel)
	}
	if e.Input["file_path"] != "/tmp/x.txt" {
		t.Errorf("entry.Input did not round-trip args: %+v", e.Input)
	}
}

func TestHeadlessApprovalHandler_Medium_ExplicitOverride_AllowedAndAudited(t *testing.T) {
	cap := withCapture(t)

	h := NewHeadlessApprovalHandler(HeadlessPolicy{
		AllowMedium:                     true,
		AllowMediumRequiresConfirmation: false,
	})
	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Args:     map[string]any{"file_path": "/tmp/y.txt"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}

	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("medium risk with explicit override should be allowed")
	}

	entries := cap.snapshot()
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}
	if entries[0].Decision != "allowed" {
		t.Errorf("entry.Decision = %q, want allowed", entries[0].Decision)
	}
}

func TestHeadlessApprovalHandler_Critical_AlwaysDeniedAndAudited(t *testing.T) {
	cap := withCapture(t)

	// Even with the most permissive policy, critical must be denied.
	h := NewHeadlessApprovalHandler(HeadlessPolicy{
		AllowMedium:                     true,
		AllowMediumRequiresConfirmation: false,
		AllowHigh:                       true,
	})
	req := permissions.ApprovalRequest{
		ToolName: "bash",
		Args:     map[string]any{"command": "rm -rf /"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskCritical},
	}

	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if resp.Allowed {
		t.Fatal("critical risk must always be denied")
	}
	if resp.Feedback == "" {
		t.Error("critical risk denial should include feedback")
	}

	entries := cap.snapshot()
	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Decision != "denied" {
		t.Errorf("entry.Decision = %q, want denied", e.Decision)
	}
	if e.RiskLevel != permissions.ToolRiskCritical.String() {
		t.Errorf("entry.RiskLevel = %q", e.RiskLevel)
	}
	if e.Input["command"] != "rm -rf /" {
		t.Errorf("entry.Input command lost: %+v", e.Input)
	}
}

func TestHeadlessApprovalHandler_Safe_AuditedAsAllowed(t *testing.T) {
	cap := withCapture(t)

	h := NewHeadlessApprovalHandler(HeadlessPolicy{})
	req := permissions.ApprovalRequest{
		ToolName: "read_file",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskSafe},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Fatal("safe risk should be allowed")
	}
	entries := cap.snapshot()
	if len(entries) != 1 || entries[0].Decision != "allowed" {
		t.Errorf("expected one allowed audit entry, got %+v", entries)
	}
}

func TestHeadlessApprovalHandler_High_Denied_Audited(t *testing.T) {
	cap := withCapture(t)

	h := NewHeadlessApprovalHandler(HeadlessPolicy{AllowHigh: false})
	req := permissions.ApprovalRequest{
		ToolName: "bash",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskHigh},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if resp.Allowed {
		t.Fatal("high risk should be denied when AllowHigh=false")
	}
	entries := cap.snapshot()
	if len(entries) != 1 || entries[0].Decision != "denied" {
		t.Errorf("expected one denied audit entry, got %+v", entries)
	}
}
