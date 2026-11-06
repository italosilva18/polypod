package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/types"
)

func TestMonitorStoreAddListRemove(t *testing.T) {
	store := NewMonitorStore()

	a := store.Add(MonitorTarget{URL: "https://example.com", Pattern: "Example"})
	b := store.Add(MonitorTarget{FilePath: "/tmp/some.log", Pattern: "error"})

	if a.ID == "" || b.ID == "" || a.ID == b.ID {
		t.Fatalf("expected distinct auto-generated IDs, got %q and %q", a.ID, b.ID)
	}

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(list))
	}

	if !store.Remove(a.ID) {
		t.Fatalf("Remove(%q) returned false", a.ID)
	}
	if store.Remove(a.ID) {
		t.Fatalf("Remove(%q) should return false on second call", a.ID)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("expected 1 target after remove, got %d", got)
	}
}

func TestMonitorStoreCheckURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello monitor world"))
	}))
	defer srv.Close()

	store := NewMonitorStore()
	target := store.Add(MonitorTarget{URL: srv.URL, Pattern: "monitor"})

	status, matched, err := store.Check(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if !matched {
		t.Fatalf("expected pattern to match, status=%q", status)
	}
	if status == "" {
		t.Fatalf("expected non-empty status")
	}

	got, ok := store.Get(target.ID)
	if !ok {
		t.Fatalf("target disappeared after check")
	}
	if got.LastStatus == "" || got.LastCheck.IsZero() {
		t.Fatalf("expected LastCheck/LastStatus to be populated: %+v", got)
	}

	// Negative case: a pattern that is absent should not match.
	noMatch := store.Add(MonitorTarget{URL: srv.URL, Pattern: "absent-xyz"})
	_, matched2, err := store.Check(context.Background(), noMatch.ID)
	if err != nil {
		t.Fatalf("Check() error on no-match: %v", err)
	}
	if matched2 {
		t.Fatalf("expected no match for absent pattern")
	}
}

func TestMonitorStoreCheckFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.log")
	if err := os.WriteFile(path, []byte("line one\nfatal: disk full\nline three\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	store := NewMonitorStore()
	target := store.Add(MonitorTarget{FilePath: path, Pattern: "fatal"})

	status, matched, err := store.Check(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("Check() error: %v", err)
	}
	if !matched {
		t.Fatalf("expected pattern to match, status=%q", status)
	}

	// Missing file should produce an error but not panic.
	missing := store.Add(MonitorTarget{FilePath: filepath.Join(dir, "nope.log"), Pattern: "x"})
	if _, _, err := store.Check(context.Background(), missing.ID); err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

func TestMonitorToolExecute(t *testing.T) {
	store := NewMonitorStore()
	tool := NewMonitorTool(store)

	if tool.Name() != "monitor" {
		t.Fatalf("unexpected name: %q", tool.Name())
	}

	// add
	addRes, err := tool.Execute(types.ToolContext{Context: context.Background()}, map[string]any{
		"action":  "add",
		"url":     "https://example.com",
		"pattern": "Example",
	})
	if err != nil || addRes.IsError {
		t.Fatalf("add failed: err=%v res=%+v", err, addRes)
	}

	// list
	listRes, err := tool.Execute(types.ToolContext{Context: context.Background()}, map[string]any{
		"action": "list",
	})
	if err != nil || listRes.IsError {
		t.Fatalf("list failed: err=%v res=%+v", err, listRes)
	}

	// validation: add without url/file
	if v := tool.ValidateInput(map[string]any{"action": "add"}); v.Valid {
		t.Fatalf("expected validation failure for add without url/file")
	}
	// validation: remove without id
	if v := tool.ValidateInput(map[string]any{"action": "remove"}); v.Valid {
		t.Fatalf("expected validation failure for remove without id")
	}
}
