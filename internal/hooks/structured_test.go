package hooks

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// --- ParseStructuredResponse ---

func TestParseStructuredResponse_MinimalValid(t *testing.T) {
	stdout := []byte(`{"continue": true}`)
	resp, err := ParseStructuredResponse(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Continue == nil || !*resp.Continue {
		t.Errorf("expected Continue=true, got %v", resp.Continue)
	}
}

func TestParseStructuredResponse_PermissionDecisionDeny(t *testing.T) {
	stdout := []byte(`{
		"hookSpecificOutput": {
			"hookEventName": "PreToolUse",
			"permissionDecision": "deny",
			"permissionDecisionReason": "path is outside workspace"
		}
	}`)
	resp, err := ParseStructuredResponse(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("expected HookEventName=PreToolUse, got %q", resp.HookSpecificOutput.HookEventName)
	}
	if resp.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("expected PermissionDecision=deny, got %q", resp.HookSpecificOutput.PermissionDecision)
	}
	if !resp.ShouldBlock() {
		t.Error("expected ShouldBlock()=true for permissionDecision=deny")
	}
	if got := resp.PermissionBehavior(); got != "deny" {
		t.Errorf("expected PermissionBehavior()=deny, got %q", got)
	}
}

func TestParseStructuredResponse_WithUpdatedInput(t *testing.T) {
	stdout := []byte(`{
		"hookSpecificOutput": {
			"hookEventName": "PreToolUse",
			"updatedInput": {"path": "/tmp/safe", "mode": "read"}
		}
	}`)
	resp, err := ParseStructuredResponse(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.HookSpecificOutput.UpdatedInput) == 0 {
		t.Fatal("expected non-empty UpdatedInput")
	}
	// Round-trip the opaque payload to confirm it is a valid object.
	var m map[string]interface{}
	if err := json.Unmarshal(resp.HookSpecificOutput.UpdatedInput, &m); err != nil {
		t.Fatalf("UpdatedInput is not a valid object: %v", err)
	}
	if m["path"] != "/tmp/safe" {
		t.Errorf("expected path=/tmp/safe, got %v", m["path"])
	}
	if m["mode"] != "read" {
		t.Errorf("expected mode=read, got %v", m["mode"])
	}
}

func TestParseStructuredResponse_InvalidJSON(t *testing.T) {
	// Line-by-line scan rejects a broken JSON-looking line when there are
	// no other candidates — ParseStructuredResponse should surface an error.
	stdout := []byte(`{not valid json at all`)
	resp, err := ParseStructuredResponse(stdout)
	if err == nil {
		t.Fatalf("expected error, got resp=%+v", resp)
	}
	if resp != nil {
		t.Errorf("expected nil response on error, got %+v", resp)
	}
	// Must be either a decode error or the "not structured" sentinel.
	if !errors.Is(err, ErrNotStructured) && !strings.Contains(err.Error(), "parse structured response") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

func TestParseStructuredResponse_PlainTextFallsBack(t *testing.T) {
	stdout := []byte("All good, nothing to report.\n")
	_, err := ParseStructuredResponse(stdout)
	if !errors.Is(err, ErrNotStructured) {
		t.Errorf("expected ErrNotStructured for plain text, got %v", err)
	}
}

func TestParseStructuredResponse_EmptyStdout(t *testing.T) {
	_, err := ParseStructuredResponse([]byte("   \n\t  "))
	if !errors.Is(err, ErrNotStructured) {
		t.Errorf("expected ErrNotStructured for empty stdout, got %v", err)
	}
}

func TestParseStructuredResponse_SkipsLogLinesBeforeJSON(t *testing.T) {
	// Real-world hooks often print log lines before emitting the JSON result.
	stdout := []byte("[hook] starting\n[hook] evaluating...\n{\"decision\":\"block\",\"reason\":\"nope\"}\n")
	resp, err := ParseStructuredResponse(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Decision != "block" {
		t.Errorf("expected Decision=block, got %q", resp.Decision)
	}
	if resp.Reason != "nope" {
		t.Errorf("expected Reason=nope, got %q", resp.Reason)
	}
}

// --- ShouldBlock ---

func TestShouldBlock_DecisionDeny(t *testing.T) {
	cases := []struct {
		name     string
		decision string
		want     bool
	}{
		{"deny", "deny", true},
		{"block", "block", true},
		{"DENY uppercase", "DENY", true},
		{"allow", "allow", false},
		{"approve", "approve", false},
		{"ask", "ask", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &StructuredHookResponse{Decision: tc.decision}
			if got := r.ShouldBlock(); got != tc.want {
				t.Errorf("ShouldBlock() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldBlock_ContinueFalse(t *testing.T) {
	f := false
	r := &StructuredHookResponse{Continue: &f}
	if !r.ShouldBlock() {
		t.Error("expected ShouldBlock()=true when Continue=false")
	}
}

func TestShouldBlock_NilReceiver(t *testing.T) {
	var r *StructuredHookResponse
	if r.ShouldBlock() {
		t.Error("nil receiver should return false")
	}
}

// --- ApplyInputModification ---

func TestApplyInputModification_ShallowMerge(t *testing.T) {
	original := json.RawMessage(`{"path": "/old", "mode": "read", "recursive": true}`)
	updated := json.RawMessage(`{"path": "/new", "extra": 42}`)

	merged, err := ApplyInputModification(original, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("merged result is not valid JSON: %v", err)
	}

	if m["path"] != "/new" {
		t.Errorf("expected path=/new (overwritten), got %v", m["path"])
	}
	if m["mode"] != "read" {
		t.Errorf("expected mode=read (preserved), got %v", m["mode"])
	}
	if m["recursive"] != true {
		t.Errorf("expected recursive=true (preserved), got %v", m["recursive"])
	}
	if m["extra"] != float64(42) {
		t.Errorf("expected extra=42 (added), got %v", m["extra"])
	}
}

func TestApplyInputModification_EmptyUpdatedReturnsOriginal(t *testing.T) {
	original := json.RawMessage(`{"a": 1}`)
	merged, err := ApplyInputModification(original, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(merged) != string(original) {
		t.Errorf("expected original unchanged, got %s", string(merged))
	}
}

func TestApplyInputModification_EmptyOriginalReturnsUpdated(t *testing.T) {
	updated := json.RawMessage(`{"a": 1, "b": "x"}`)
	merged, err := ApplyInputModification(nil, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if m["a"] != float64(1) || m["b"] != "x" {
		t.Errorf("expected {a:1,b:x}, got %v", m)
	}
}

func TestApplyInputModification_UpdatedNotObjectReturnsError(t *testing.T) {
	original := json.RawMessage(`{"a": 1}`)
	updated := json.RawMessage(`[1, 2, 3]`)
	_, err := ApplyInputModification(original, updated)
	if err == nil {
		t.Error("expected error for non-object updated payload")
	}
}

func TestApplyInputModification_NestedValuesReplacedNotMerged(t *testing.T) {
	// Shallow-merge semantics: nested objects are replaced wholesale,
	// not deep-merged.
	original := json.RawMessage(`{"cfg": {"x": 1, "y": 2}}`)
	updated := json.RawMessage(`{"cfg": {"x": 99}}`)

	merged, err := ApplyInputModification(original, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]map[string]interface{}
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	if m["cfg"]["x"] != float64(99) {
		t.Errorf("expected cfg.x=99, got %v", m["cfg"]["x"])
	}
	if _, ok := m["cfg"]["y"]; ok {
		t.Error("expected cfg.y to be gone (shallow merge replaces nested)")
	}
}

// --- ApplyOutputModification ---

func TestApplyOutputModification_DelegatesToInputMod(t *testing.T) {
	original := json.RawMessage(`{"result": "ok", "count": 1}`)
	updated := json.RawMessage(`{"count": 2}`)

	merged, err := ApplyOutputModification(original, updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if m["result"] != "ok" {
		t.Errorf("expected result=ok preserved, got %v", m["result"])
	}
	if m["count"] != float64(2) {
		t.Errorf("expected count=2, got %v", m["count"])
	}
}

// --- ValidateUpdatedInput ---

func TestValidateUpdatedInput(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"count":{"type":"integer"}}}`)
	if err := ValidateUpdatedInput(schema, json.RawMessage(`{"name":"poly","count":2}`)); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`{"count":2}`),
		json.RawMessage(`{"name":"poly","count":2.5}`),
		json.RawMessage(`[1,2,3]`),
	} {
		if err := ValidateUpdatedInput(schema, invalid); err == nil {
			t.Errorf("invalid input accepted: %s", invalid)
		}
	}
}
