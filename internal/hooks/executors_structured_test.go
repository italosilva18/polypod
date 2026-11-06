package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// fakeLegacyExecutor is a test double implementing LegacyExecutor with a
// pre-canned HookResult and error.
type fakeLegacyExecutor struct {
	result *HookResult
	err    error

	lastCtx   context.Context
	lastInput HookInput
	called    int
}

func (f *fakeLegacyExecutor) Execute(ctx context.Context, input HookInput) (*HookResult, error) {
	f.called++
	f.lastCtx = ctx
	f.lastInput = input
	return f.result, f.err
}

func newTestInput() HookInput {
	return HookInput{
		Hook:      HookCommand{Command: "test.sh"},
		HookName:  "test-hook",
		HookEvent: EventPreToolUse,
		ToolName:  "Bash",
		ToolUseID: "tu_123",
		JSONInput: `{"tool_name":"Bash"}`,
	}
}

// newCaptureLogger returns a logger + buffer so tests can assert warnings.
func newCaptureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

func TestStructuredHookExecutor_UpgradesStructuredStdout(t *testing.T) {
	stdout := `{
		"continue": true,
		"systemMessage": "hello from hook",
		"decision": "approve",
		"hookSpecificOutput": {
			"hookEventName": "PreToolUse",
			"permissionDecision": "allow",
			"permissionDecisionReason": "looks fine",
			"updatedInput": {"command": "ls -la"}
		}
	}`
	inner := &fakeLegacyExecutor{
		result: &HookResult{
			Hook:    &HookCommand{Command: "test.sh"},
			Outcome: HookOutcomeSuccess,
			Stdout:  stdout,
		},
	}

	exec := NewStructuredHookExecutor(inner)
	out, err := exec.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil StructuredHookResult")
	}
	if out.Structured == nil {
		t.Fatalf("expected Structured field to be populated")
	}
	if out.Structured.SystemMessage != "hello from hook" {
		t.Errorf("systemMessage not parsed: %q", out.Structured.SystemMessage)
	}
	if out.Message != "hello from hook" {
		t.Errorf("Message not propagated to HookResult: %q", out.Message)
	}
	if out.PermissionBehavior != "allow" {
		t.Errorf("PermissionBehavior expected 'allow', got %q", out.PermissionBehavior)
	}
	if out.HookPermissionDecisionReason != "looks fine" {
		t.Errorf("HookPermissionDecisionReason not propagated: %q", out.HookPermissionDecisionReason)
	}
	if out.UpdatedInput == nil || out.UpdatedInput["command"] != "ls -la" {
		t.Errorf("UpdatedInput not decoded: %#v", out.UpdatedInput)
	}
	if inner.called != 1 {
		t.Errorf("expected inner to be called once, got %d", inner.called)
	}
}

func TestStructuredHookExecutor_NonJSONPassesThrough(t *testing.T) {
	inner := &fakeLegacyExecutor{
		result: &HookResult{
			Hook:    &HookCommand{Command: "test.sh"},
			Outcome: HookOutcomeSuccess,
			Stdout:  "plain text response\nno json here",
		},
	}

	logger, buf := newCaptureLogger()
	exec := NewStructuredHookExecutor(inner).WithLogger(logger)
	out, err := exec.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil result")
	}
	if out.Structured != nil {
		t.Errorf("expected Structured to be nil for plain text stdout, got %#v", out.Structured)
	}
	if out.Stdout != "plain text response\nno json here" {
		t.Errorf("raw stdout not preserved: %q", out.Stdout)
	}
	if out.Outcome != HookOutcomeSuccess {
		t.Errorf("outcome mutated unexpectedly: %v", out.Outcome)
	}
	// No warning should have been logged for plain text.
	if strings.Contains(buf.String(), "parse failed") {
		t.Errorf("unexpected warning for plain text stdout: %s", buf.String())
	}
}

func TestStructuredHookExecutor_InvalidJSONLogsAndPassesThrough(t *testing.T) {
	// A line that looks like JSON (starts with '{') but is not valid JSON.
	inner := &fakeLegacyExecutor{
		result: &HookResult{
			Hook:    &HookCommand{Command: "test.sh"},
			Outcome: HookOutcomeSuccess,
			Stdout:  `{"decision": "approve", this is broken}`,
		},
	}

	logger, buf := newCaptureLogger()
	exec := NewStructuredHookExecutor(inner).WithLogger(logger)
	out, err := exec.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Structured != nil {
		t.Errorf("expected Structured to be nil on parse error, got %#v", out.Structured)
	}
	if out.Outcome != HookOutcomeSuccess {
		t.Errorf("outcome should not be mutated on parse error: %v", out.Outcome)
	}
	if !strings.Contains(buf.String(), "parse failed") {
		t.Errorf("expected warning log on invalid JSON, got: %s", buf.String())
	}
}

func TestStructuredHookResult_ApplyToToolInputMergesUpdatedInput(t *testing.T) {
	stdout := `{
		"hookSpecificOutput": {
			"hookEventName": "PreToolUse",
			"updatedInput": {"command": "ls -la", "timeout": 30}
		}
	}`
	inner := &fakeLegacyExecutor{
		result: &HookResult{
			Hook:    &HookCommand{Command: "test.sh"},
			Outcome: HookOutcomeSuccess,
			Stdout:  stdout,
		},
	}
	exec := NewStructuredHookExecutor(inner)
	out, err := exec.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Structured == nil {
		t.Fatalf("expected Structured field to be populated")
	}

	original := json.RawMessage(`{"command":"ls","description":"list"}`)
	merged, err := out.ApplyToToolInput(original)
	if err != nil {
		t.Fatalf("ApplyToToolInput failed: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("merged result is not valid JSON: %v (%s)", err, merged)
	}
	if got["command"] != "ls -la" {
		t.Errorf("command not overwritten: %#v", got["command"])
	}
	if got["description"] != "list" {
		t.Errorf("description not preserved from original: %#v", got["description"])
	}
	// timeout is a JSON number which unmarshals as float64.
	if tv, ok := got["timeout"].(float64); !ok || tv != 30 {
		t.Errorf("timeout not added from updatedInput: %#v", got["timeout"])
	}
}

func TestStructuredHookResult_ApplyToToolInputNoStructured(t *testing.T) {
	r := &StructuredHookResult{}
	orig := json.RawMessage(`{"a":1}`)
	out, err := r.ApplyToToolInput(orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(orig) {
		t.Errorf("expected unchanged original, got %s", out)
	}
}

func TestStructuredHookResult_ApplyToToolOutputMerges(t *testing.T) {
	stdout := `{"hookSpecificOutput":{"hookEventName":"PostToolUse","updatedMCPToolOutput":{"result":"patched"}}}`
	inner := &fakeLegacyExecutor{
		result: &HookResult{
			Hook:    &HookCommand{Command: "test.sh"},
			Outcome: HookOutcomeSuccess,
			Stdout:  stdout,
		},
	}
	exec := NewStructuredHookExecutor(inner)
	out, err := exec.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	original := json.RawMessage(`{"result":"raw","duration":10}`)
	merged, err := out.ApplyToToolOutput(original)
	if err != nil {
		t.Fatalf("ApplyToToolOutput failed: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got["result"] != "patched" {
		t.Errorf("result not overwritten: %#v", got["result"])
	}
	if _, ok := got["duration"]; !ok {
		t.Errorf("duration not preserved: %#v", got)
	}
}

func TestStructuredHookExecutor_InnerErrorPropagated(t *testing.T) {
	boom := errors.New("boom")
	inner := &fakeLegacyExecutor{
		result: &HookResult{Hook: &HookCommand{}, Outcome: HookOutcomeCancelled, Stderr: "boom"},
		err:    boom,
	}
	exec := NewStructuredHookExecutor(inner)
	out, err := exec.Execute(context.Background(), newTestInput())
	if !errors.Is(err, boom) {
		t.Errorf("expected boom error to propagate, got %v", err)
	}
	if out == nil {
		t.Fatalf("expected non-nil result even on inner error")
	}
	if out.Outcome != HookOutcomeCancelled {
		t.Errorf("outcome not preserved on error: %v", out.Outcome)
	}
}

func TestLegacyExecutorFunc_Adapter(t *testing.T) {
	want := &HookResult{Stdout: "ok"}
	var fn LegacyExecutorFunc = func(ctx context.Context, input HookInput) (*HookResult, error) {
		return want, nil
	}
	got, err := fn.Execute(context.Background(), newTestInput())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("adapter did not pass through result")
	}
	// silence unused imports in case a helper is trimmed later
	_ = io.Discard
}
