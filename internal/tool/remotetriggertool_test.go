package tool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// helper: execute the remote_trigger tool with args and return result.
func runRemoteTriggerTool(t *testing.T, store *RemoteTriggerStore, args map[string]any) (types.ToolResult, types.ValidationResult) {
	t.Helper()
	tool := NewRemoteTriggerTool(store)
	vr := tool.ValidateInput(args)
	if !vr.Valid {
		return types.ToolResult{}, vr
	}
	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, args)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	return res, vr
}

func TestRemoteTriggerTool_RegisterAndTrigger(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true,"echo":"hello"}`)
	}))
	defer srv.Close()

	store := NewRemoteTriggerStore()
	store.Register("deploy", srv.URL)

	// Sanity: Names() and Lookup().
	names := store.Names()
	if len(names) != 1 || names[0] != "deploy" {
		t.Fatalf("Names() = %v, want [deploy]", names)
	}
	if url, ok := store.Lookup("deploy"); !ok || url != srv.URL {
		t.Fatalf("Lookup(deploy) = (%q,%v), want (%q,true)", url, ok, srv.URL)
	}

	res, vr := runRemoteTriggerTool(t, store, map[string]any{
		"name": "deploy",
		"payload": map[string]any{
			"env":  "prod",
			"note": "hello",
		},
	})

	if !vr.Valid {
		t.Fatalf("expected Valid=true, got %+v", vr)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}
	if !strings.Contains(res.Content, `"ok":true`) {
		t.Errorf("expected response body in content, got %q", res.Content)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", gotContentType)
	}
	if gotBody["env"] != "prod" || gotBody["note"] != "hello" {
		t.Errorf("payload not forwarded: %+v", gotBody)
	}

	// Permissions classification.
	tool := NewRemoteTriggerTool(store)
	args := map[string]any{"name": "deploy"}
	if tool.IsReadOnly(args) {
		t.Error("IsReadOnly should be false")
	}
	if tool.IsDestructive(args) {
		t.Error("IsDestructive should be false")
	}
	if !tool.IsConcurrencySafe(args) {
		t.Error("IsConcurrencySafe should be true")
	}
	if tool.Name() != "remote_trigger" {
		t.Errorf("Name() = %q, want remote_trigger", tool.Name())
	}
}

func TestRemoteTriggerTool_UnknownNameRejected(t *testing.T) {
	store := NewRemoteTriggerStore()
	store.Register("known", "http://example.invalid")

	tool := NewRemoteTriggerTool(store)

	// Unknown name should be rejected at validation.
	vr := tool.ValidateInput(map[string]any{"name": "nope"})
	if vr.Valid {
		t.Fatal("expected validation to reject unknown name")
	}
	if !strings.Contains(vr.Message, "not registered") {
		t.Errorf("unexpected validation message: %q", vr.Message)
	}
	if !strings.Contains(vr.Message, "known") {
		t.Errorf("expected available list to include 'known', got %q", vr.Message)
	}

	// Empty name also rejected.
	vr = tool.ValidateInput(map[string]any{"name": ""})
	if vr.Valid || !strings.Contains(vr.Message, "name is required") {
		t.Errorf("expected empty name to be rejected, got %+v", vr)
	}

	// Direct Trigger on unknown name yields an error.
	if _, err := store.Trigger(context.Background(), "nope", nil); err == nil {
		t.Error("expected error from Trigger on unknown name")
	}
}

func TestRemoteTriggerTool_HTTP500ErrorPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	store := NewRemoteTriggerStore()
	store.Register("flaky", srv.URL)

	res, vr := runRemoteTriggerTool(t, store, map[string]any{
		"name":    "flaky",
		"payload": map[string]any{"x": 1},
	})
	if !vr.Valid {
		t.Fatalf("validation failed: %+v", vr)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for HTTP 500")
	}
	if !strings.Contains(res.Content, "500") {
		t.Errorf("expected 500 in error content, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "boom") {
		t.Errorf("expected response body 'boom' in content, got %q", res.Content)
	}

	// Direct Trigger should also return the body plus an error.
	body, err := store.Trigger(context.Background(), "flaky", nil)
	if err == nil {
		t.Fatal("expected error from Trigger on HTTP 500")
	}
	if !strings.Contains(string(body), "boom") {
		t.Errorf("expected body to contain 'boom', got %q", string(body))
	}
}

func TestRemoteTriggerTool_RegisterOnRegistry(t *testing.T) {
	store := NewRemoteTriggerStore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	store.Register("ping", srv.URL)

	reg := NewRegistry()
	RegisterRemoteTriggerTool(reg, store)

	tool, ok := reg.Get("remote_trigger")
	if !ok {
		t.Fatal("remote_trigger not found in registry")
	}
	res, err := tool.Execute(types.ToolContext{Context: context.Background()}, map[string]any{
		"name": "ping",
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %q", res.Content)
	}
	if !strings.Contains(res.Content, "ok") {
		t.Errorf("expected 'ok' in content, got %q", res.Content)
	}
}
