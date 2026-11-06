package approval

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/permissions"
)

// -------------------------------------------------------------------
// ApprovalRequest / ApprovalResponse (types live in permissions pkg)
// -------------------------------------------------------------------

func TestApprovalRequest_Construction(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "read_file",
		Args:     map[string]any{"file_path": "/tmp/foo.txt"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskLow, Category: "read-only"},
		Reason:   "just reading",
	}

	if req.ToolName != "read_file" {
		t.Errorf("ToolName = %q, want %q", req.ToolName, "read_file")
	}
	if req.Args["file_path"] != "/tmp/foo.txt" {
		t.Errorf("Args[file_path] = %v, want /tmp/foo.txt", req.Args["file_path"])
	}
	if req.Risk.Level != permissions.ToolRiskLow {
		t.Errorf("Risk.Level = %v, want ToolRiskLow", req.Risk.Level)
	}
	if req.Reason != "just reading" {
		t.Errorf("Reason = %q, want %q", req.Reason, "just reading")
	}
}

func TestApprovalResponse_Fields(t *testing.T) {
	resp := permissions.ApprovalResponse{
		Allowed:   true,
		Permanent: true,
		Feedback:  "looks good",
		Abort:     false,
	}

	if !resp.Allowed {
		t.Error("Allowed should be true")
	}
	if !resp.Permanent {
		t.Error("Permanent should be true")
	}
	if resp.Feedback != "looks good" {
		t.Errorf("Feedback = %q", resp.Feedback)
	}
	if resp.Abort {
		t.Error("Abort should be false")
	}
}

// -------------------------------------------------------------------
// HeadlessApprovalHandler
// -------------------------------------------------------------------

func TestHeadlessApprovalHandler_Constructor(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{})
	if h == nil {
		t.Fatal("NewHeadlessApprovalHandler returned nil")
	}
}

func TestHeadlessApprovalHandler_SafeRisk(t *testing.T) {
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
		t.Error("safe risk should be auto-approved in headless mode")
	}
}

func TestHeadlessApprovalHandler_LowRisk(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{})
	req := permissions.ApprovalRequest{
		ToolName: "fetch",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskLow},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Error("low risk should be auto-approved in headless mode")
	}
}

func TestHeadlessApprovalHandler_CriticalRisk(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{})
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
		t.Error("critical risk should be auto-denied in headless mode")
	}
	if resp.Feedback == "" {
		t.Error("critical risk denial should include feedback")
	}
}

func TestHeadlessApprovalHandler_MediumRisk_Deny(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{AllowMedium: false})
	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if resp.Allowed {
		t.Error("medium risk should be denied when AllowMedium=false")
	}
}

func TestHeadlessApprovalHandler_MediumRisk_Allow(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{AllowMedium: true})
	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Error("medium risk should be allowed when AllowMedium=true")
	}
}

func TestHeadlessApprovalHandler_HighRisk_Deny(t *testing.T) {
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
		t.Error("high risk should be denied when AllowHigh=false")
	}
}

func TestHeadlessApprovalHandler_HighRisk_Allow(t *testing.T) {
	h := NewHeadlessApprovalHandler(HeadlessPolicy{AllowHigh: true})
	req := permissions.ApprovalRequest{
		ToolName: "bash",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskHigh},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Error("high risk should be allowed when AllowHigh=true")
	}
}

// -------------------------------------------------------------------
// CLIApprovalHandler
// -------------------------------------------------------------------

func TestCLIApprovalHandler_Constructor_Defaults(t *testing.T) {
	h := NewCLIApprovalHandler()
	if h == nil {
		t.Fatal("NewCLIApprovalHandler returned nil")
	}
	if h.timeout == 0 {
		t.Error("default timeout should be non-zero")
	}
	if h.yolo {
		t.Error("yolo should default to false")
	}
}

func TestCLIApprovalHandler_YOLO_AutoApprovesAllRisks(t *testing.T) {
	h := NewCLIApprovalHandler(WithYOLO(true))

	levels := []permissions.ToolRiskLevel{
		permissions.ToolRiskSafe,
		permissions.ToolRiskLow,
		permissions.ToolRiskMedium,
		permissions.ToolRiskHigh,
		permissions.ToolRiskCritical,
	}

	for _, lvl := range levels {
		req := permissions.ApprovalRequest{
			ToolName: "tool",
			Risk:     permissions.ToolRisk{Level: lvl},
		}
		resp, err := h.AskApproval(context.Background(), req)
		if err != nil {
			t.Fatalf("AskApproval(%v): %v", lvl, err)
		}
		if !resp.Allowed {
			t.Errorf("YOLO mode should auto-approve risk %v", lvl)
		}
	}
}

func TestCLIApprovalHandler_YOLO_Toggle_ViaOption(t *testing.T) {
	h := NewCLIApprovalHandler(WithYOLO(false))
	if h.yolo {
		t.Error("WithYOLO(false) should leave yolo=false")
	}

	h2 := NewCLIApprovalHandler(WithYOLO(true))
	if !h2.yolo {
		t.Error("WithYOLO(true) should set yolo=true")
	}
}

func TestCLIApprovalHandler_ReadsYesInput(t *testing.T) {
	in := bytes.NewBufferString("y\n")
	out := &bytes.Buffer{}
	h := NewCLIApprovalHandler(
		WithReader(in),
		WithWriter(out),
		WithTimeout(2*time.Second),
	)

	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Args:     map[string]any{"file_path": "/tmp/x.txt"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed {
		t.Error(`"y" input should produce Allowed=true`)
	}
	if out.Len() == 0 {
		t.Error("prompt should have been written to writer")
	}
}

func TestCLIApprovalHandler_ReadsNoInput(t *testing.T) {
	in := bytes.NewBufferString("n\n")
	out := &bytes.Buffer{}
	h := NewCLIApprovalHandler(
		WithReader(in),
		WithWriter(out),
		WithTimeout(2*time.Second),
	)

	req := permissions.ApprovalRequest{
		ToolName: "bash",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskHigh},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if resp.Allowed {
		t.Error(`"n" input should produce Allowed=false`)
	}
}

func TestCLIApprovalHandler_AlwaysInput_SetsPermanent(t *testing.T) {
	in := bytes.NewBufferString("always\n")
	out := &bytes.Buffer{}
	h := NewCLIApprovalHandler(
		WithReader(in),
		WithWriter(out),
		WithTimeout(2*time.Second),
	)

	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	resp, err := h.AskApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("AskApproval: %v", err)
	}
	if !resp.Allowed || !resp.Permanent {
		t.Errorf("'always' should give Allowed=true, Permanent=true; got %+v", resp)
	}
}

func TestCLIApprovalHandler_ContextCancellation(t *testing.T) {
	// Empty reader would block forever; cancel via context.
	in := &blockingReader{}
	out := &bytes.Buffer{}
	h := NewCLIApprovalHandler(
		WithReader(in),
		WithWriter(out),
		WithTimeout(0), // no timeout
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	req := permissions.ApprovalRequest{
		ToolName: "tool",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	_, err := h.AskApproval(ctx, req)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// blockingReader blocks forever on Read.
type blockingReader struct{}

func (b *blockingReader) Read(_ []byte) (int, error) {
	select {} // block forever
}

// -------------------------------------------------------------------
// FormatApprovalPrompt
// -------------------------------------------------------------------

func TestFormatApprovalPrompt_NonEmpty(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "read_file",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskLow},
	}
	out := FormatApprovalPrompt(req)
	if out == "" {
		t.Error("FormatApprovalPrompt returned empty string")
	}
}

func TestFormatApprovalPrompt_ContainsToolNameAndRisk(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "run_command",
		Args:     map[string]any{"command": "rm -rf /tmp/build"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskHigh},
	}
	out := FormatApprovalPrompt(req)

	if !strings.Contains(out, "run_command") {
		t.Errorf("output missing tool name: %q", out)
	}
	if !strings.Contains(out, "HIGH") {
		t.Errorf("output missing risk level HIGH: %q", out)
	}
	if !strings.Contains(out, "rm -rf") {
		t.Errorf("output missing command: %q", out)
	}
}

func TestFormatApprovalPrompt_ContainsFilePath(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "write_file",
		Args:     map[string]any{"file_path": "/tmp/hello.txt"},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
	}
	out := FormatApprovalPrompt(req)

	if !strings.Contains(out, "/tmp/hello.txt") {
		t.Errorf("output missing file path: %q", out)
	}
}

func TestFormatApprovalPrompt_RiskColors(t *testing.T) {
	// ANSI colour codes from approval.go:
	// red=31, yellow=33, blue=34, gray=90
	cases := []struct {
		level     permissions.ToolRiskLevel
		ansiCode  string
		labelWord string
	}{
		{permissions.ToolRiskCritical, "\033[31m", "CRITICAL"},
		{permissions.ToolRiskHigh, "\033[33m", "HIGH"},
		{permissions.ToolRiskMedium, "\033[34m", "MEDIUM"},
		{permissions.ToolRiskLow, "\033[90m", "LOW"},
	}

	for _, c := range cases {
		req := permissions.ApprovalRequest{
			ToolName: "tool",
			Risk:     permissions.ToolRisk{Level: c.level},
		}
		out := FormatApprovalPrompt(req)

		if !strings.Contains(out, c.ansiCode) {
			t.Errorf("risk %v: output missing ANSI code %q", c.level, c.ansiCode)
		}
		if !strings.Contains(out, c.labelWord) {
			t.Errorf("risk %v: output missing label %q", c.level, c.labelWord)
		}
	}
}

func TestFormatApprovalPrompt_TruncatesLongCommand(t *testing.T) {
	longCmd := strings.Repeat("x", 200)
	req := permissions.ApprovalRequest{
		ToolName: "bash",
		Args:     map[string]any{"command": longCmd},
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskHigh},
	}
	out := FormatApprovalPrompt(req)

	if strings.Contains(out, longCmd) {
		t.Error("very long command should be truncated, but wasn't")
	}
	if !strings.Contains(out, "...") {
		t.Error("truncated command should end with ellipsis")
	}
}

func TestFormatApprovalPrompt_IncludesReason(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "tool",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskMedium},
		Reason:   "because of reasons",
	}
	out := FormatApprovalPrompt(req)

	if !strings.Contains(out, "because of reasons") {
		t.Errorf("output missing Reason: %q", out)
	}
}

func TestFormatApprovalPrompt_IncludesPromptOptions(t *testing.T) {
	req := permissions.ApprovalRequest{
		ToolName: "tool",
		Risk:     permissions.ToolRisk{Level: permissions.ToolRiskLow},
	}
	out := FormatApprovalPrompt(req)

	for _, opt := range []string{"[y]es", "[n]o", "[a]lways", "[d]eny"} {
		if !strings.Contains(out, opt) {
			t.Errorf("output missing prompt option %q", opt)
		}
	}
}

// -------------------------------------------------------------------
// parseInput (unexported helper)
// -------------------------------------------------------------------

func TestParseInput_Variants(t *testing.T) {
	cases := []struct {
		input     string
		allowed   bool
		permanent bool
		feedback  string
	}{
		{"y", true, false, ""},
		{"yes", true, false, ""},
		{"n", false, false, ""},
		{"no", false, false, ""},
		{"", false, false, ""},
		{"a", true, true, ""},
		{"always", true, true, ""},
		{"d", false, true, ""},
		{"deny", false, true, ""},
		{"this is feedback", false, false, "this is feedback"},
	}

	for _, c := range cases {
		got := parseInput(c.input)
		if got.Allowed != c.allowed {
			t.Errorf("parseInput(%q).Allowed = %v, want %v", c.input, got.Allowed, c.allowed)
		}
		if got.Permanent != c.permanent {
			t.Errorf("parseInput(%q).Permanent = %v, want %v", c.input, got.Permanent, c.permanent)
		}
		if got.Feedback != c.feedback {
			t.Errorf("parseInput(%q).Feedback = %q, want %q", c.input, got.Feedback, c.feedback)
		}
	}
}
