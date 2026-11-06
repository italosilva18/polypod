package undoredo

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestManager() *Manager {
	return NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestUndoRedoPackage(t *testing.T) {
	t.Log("undoredo package loaded")
}

func TestNewManager(t *testing.T) {
	m := newTestManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.CanUndo() {
		t.Error("fresh manager should not have anything to undo")
	}
	if len(m.history) != 0 {
		t.Errorf("fresh history len = %d; want 0", len(m.history))
	}
	if len(m.redoBuf) != 0 {
		t.Errorf("fresh redoBuf len = %d; want 0", len(m.redoBuf))
	}
}

func TestBeforeChange_ExistingFile(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	id := m.BeforeChange("edit a", []string{path})
	if id != 1 {
		t.Errorf("first action id = %d; want 1", id)
	}
	if !m.CanUndo() {
		t.Error("CanUndo should be true after BeforeChange")
	}
	if got := string(m.history[0].Files[path]); got != "original" {
		t.Errorf("snapshot = %q; want %q", got, "original")
	}
}

func TestBeforeChange_NewFile(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	m.BeforeChange("create new", []string{path})

	if len(m.history[0].NewFiles) != 1 {
		t.Errorf("NewFiles len = %d; want 1", len(m.history[0].NewFiles))
	}
	if len(m.history[0].Files) != 0 {
		t.Errorf("Files map should be empty for non-existent file, got %d", len(m.history[0].Files))
	}
}

func TestUndo_RestoresFileContent(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m.BeforeChange("modify f", []string{path})

	// Simulate the modification
	if err := os.WriteFile(path, []byte("after"), 0644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	msg, err := m.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if !strings.Contains(msg, "Desfeito") {
		t.Errorf("undo message missing 'Desfeito': %q", msg)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(got) != "before" {
		t.Errorf("restored content = %q; want %q", got, "before")
	}

	if m.CanUndo() {
		t.Error("history should be empty after the single undo")
	}
}

func TestUndo_RemovesNewFile(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "created.txt")

	// Snapshot before file exists
	m.BeforeChange("create created.txt", []string{path})

	// Simulate the creation
	if err := os.WriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should have been removed on undo, stat err = %v", err)
	}
}

func TestUndo_EmptyHistory(t *testing.T) {
	m := newTestManager()
	_, err := m.Undo()
	if err == nil {
		t.Error("Undo on empty history should return error")
	}
}

func TestRedo_ReappliesAction(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "r.txt")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m.BeforeChange("change r", []string{path})
	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	if len(m.redoBuf) != 1 {
		t.Fatalf("redoBuf len = %d; want 1", len(m.redoBuf))
	}

	msg, err := m.Redo()
	if err != nil {
		t.Fatalf("Redo: %v", err)
	}
	if !strings.Contains(msg, "Refeito") {
		t.Errorf("redo message missing 'Refeito': %q", msg)
	}
	if len(m.history) != 1 {
		t.Errorf("history len after redo = %d; want 1", len(m.history))
	}
	if len(m.redoBuf) != 0 {
		t.Errorf("redoBuf len after redo = %d; want 0", len(m.redoBuf))
	}
}

func TestRedo_EmptyBuffer(t *testing.T) {
	m := newTestManager()
	_, err := m.Redo()
	if err == nil {
		t.Error("Redo on empty buffer should return error")
	}
}

func TestBeforeChange_ClearsRedoBuffer(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	p1 := filepath.Join(dir, "1.txt")
	p2 := filepath.Join(dir, "2.txt")
	os.WriteFile(p1, []byte("one"), 0644)
	os.WriteFile(p2, []byte("two"), 0644)

	m.BeforeChange("first", []string{p1})
	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(m.redoBuf) != 1 {
		t.Fatalf("redoBuf should have 1 entry, got %d", len(m.redoBuf))
	}

	// New action should wipe the redo buffer
	m.BeforeChange("second", []string{p2})
	if len(m.redoBuf) != 0 {
		t.Errorf("redoBuf should be cleared after new BeforeChange; got %d", len(m.redoBuf))
	}
}

func TestHistoryLimit(t *testing.T) {
	m := newTestManager()

	dir := t.TempDir()
	// Push 55 actions; history should cap at 50
	for i := 0; i < 55; i++ {
		p := filepath.Join(dir, "x.txt")
		os.WriteFile(p, []byte("v"), 0644)
		m.BeforeChange("act", []string{p})
	}

	if len(m.history) != 50 {
		t.Errorf("history len = %d; want 50 (limit)", len(m.history))
	}
	// nextID is never reset, so the most recent id should be 55
	if m.history[len(m.history)-1].ID != 55 {
		t.Errorf("last action ID = %d; want 55", m.history[len(m.history)-1].ID)
	}
	// The first remaining action should be ID 6 (1..5 were dropped)
	if m.history[0].ID != 6 {
		t.Errorf("first retained action ID = %d; want 6", m.history[0].ID)
	}
}

func TestCanUndo(t *testing.T) {
	m := newTestManager()
	if m.CanUndo() {
		t.Error("empty manager: CanUndo should be false")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "c.txt")
	os.WriteFile(p, []byte("x"), 0644)

	m.BeforeChange("c", []string{p})
	if !m.CanUndo() {
		t.Error("after BeforeChange: CanUndo should be true")
	}

	if _, err := m.Undo(); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if m.CanUndo() {
		t.Error("after undo on single-item history: CanUndo should be false")
	}
}

func TestHistory_FormattedOutput(t *testing.T) {
	m := newTestManager()

	// Empty history
	if got := m.History(); !strings.Contains(got, "Nenhuma") {
		t.Errorf("empty History() = %q; want 'Nenhuma...'", got)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "h.txt")
	os.WriteFile(p, []byte("hi"), 0644)
	m.BeforeChange("hello action", []string{p})

	out := m.History()
	if !strings.Contains(out, "hello action") {
		t.Errorf("History() output missing description; got %q", out)
	}
	if !strings.Contains(out, "Historico") {
		t.Errorf("History() output missing header; got %q", out)
	}
}
