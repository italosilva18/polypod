package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/costa/polypod/internal/types"
)

// copyFixture copies the sample notebook fixture into a temp dir so tests can
// mutate it without touching testdata.
func copyFixture(t *testing.T) (workingDir, nbPath string) {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "sample_notebook.ipynb"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dst := filepath.Join(dir, "notebook.ipynb")
	if err := os.WriteFile(dst, src, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, dst
}

func TestLoadNotebook_Roundtrip(t *testing.T) {
	_, path := copyFixture(t)

	nb, err := LoadNotebook(path)
	if err != nil {
		t.Fatalf("LoadNotebook: %v", err)
	}
	if got := len(nb.Cells); got != 2 {
		t.Fatalf("expected 2 cells, got %d", got)
	}
	if nb.Cells[0].CellType != "code" {
		t.Errorf("cell[0] cell_type = %q, want code", nb.Cells[0].CellType)
	}
	if nb.Cells[1].CellType != "markdown" {
		t.Errorf("cell[1] cell_type = %q, want markdown", nb.Cells[1].CellType)
	}
	if nb.NBFormat != 4 {
		t.Errorf("nbformat = %d, want 4", nb.NBFormat)
	}
	if nb.NBFormatMinor != 5 {
		t.Errorf("nbformat_minor = %d, want 5", nb.NBFormatMinor)
	}

	// Save and reload — structure should be stable.
	if err := SaveNotebook(path, nb); err != nil {
		t.Fatalf("SaveNotebook: %v", err)
	}
	nb2, err := LoadNotebook(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(nb2.Cells) != len(nb.Cells) {
		t.Errorf("cell count changed after roundtrip: %d → %d", len(nb.Cells), len(nb2.Cells))
	}
	if nb2.NBFormat != nb.NBFormat || nb2.NBFormatMinor != nb.NBFormatMinor {
		t.Errorf("nbformat drift: %d.%d → %d.%d",
			nb.NBFormat, nb.NBFormatMinor, nb2.NBFormat, nb2.NBFormatMinor)
	}
	if _, ok := nb2.Metadata["kernelspec"]; !ok {
		t.Errorf("metadata.kernelspec missing after roundtrip")
	}
}

func execNotebookEdit(t *testing.T, wd string, args map[string]any) types.ToolResult {
	t.Helper()
	reg := NewRegistry()
	RegisterNotebookEditTool(reg, wd)
	tool, ok := reg.Get("notebook_edit")
	if !ok {
		t.Fatalf("notebook_edit not registered")
	}
	if v := tool.ValidateInput(args); !v.Valid {
		t.Fatalf("validate failed: %s", v.Message)
	}
	if p := tool.CheckPermissions(args, types.ToolContext{WorkingDir: wd}); p.Behavior != types.PermissionAllow {
		t.Fatalf("permission denied: %s", p.Message)
	}
	res, err := tool.Execute(types.ToolContext{WorkingDir: wd}, args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("execute returned error: %s", res.Content)
	}
	return res
}

func TestNotebookEdit_Replace(t *testing.T) {
	wd, path := copyFixture(t)

	execNotebookEdit(t, wd, map[string]any{
		"notebook_path": path,
		"cell_number":   0,
		"new_source":    "print('world')",
		"edit_mode":     "replace",
	})

	nb, err := LoadNotebook(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("expected 2 cells after replace, got %d", len(nb.Cells))
	}
	var src string
	if err := json.Unmarshal(nb.Cells[0].Source, &src); err != nil {
		t.Fatalf("source unmarshal: %v", err)
	}
	if src != "print('world')" {
		t.Errorf("source = %q, want %q", src, "print('world')")
	}
	// execution_count must be reset to null and outputs cleared on code replace
	if string(nb.Cells[0].ExecutionCount) != "null" {
		t.Errorf("execution_count = %s, want null", string(nb.Cells[0].ExecutionCount))
	}
	if len(nb.Cells[0].Outputs) != 0 {
		t.Errorf("outputs not cleared, got %d", len(nb.Cells[0].Outputs))
	}
}

func TestNotebookEdit_Insert(t *testing.T) {
	wd, path := copyFixture(t)

	execNotebookEdit(t, wd, map[string]any{
		"notebook_path": path,
		"cell_number":   1,
		"new_source":    "x = 42",
		"edit_mode":     "insert",
		"cell_type":     "code",
	})

	nb, err := LoadNotebook(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(nb.Cells) != 3 {
		t.Fatalf("expected 3 cells after insert, got %d", len(nb.Cells))
	}
	// New cell is at index 1, original markdown pushed to index 2
	if nb.Cells[1].CellType != "code" {
		t.Errorf("inserted cell type = %q, want code", nb.Cells[1].CellType)
	}
	var src string
	if err := json.Unmarshal(nb.Cells[1].Source, &src); err != nil {
		t.Fatalf("source unmarshal: %v", err)
	}
	if src != "x = 42" {
		t.Errorf("inserted source = %q, want %q", src, "x = 42")
	}
	if nb.Cells[2].CellType != "markdown" {
		t.Errorf("cell[2] type = %q, want markdown", nb.Cells[2].CellType)
	}
}

func TestNotebookEdit_Delete(t *testing.T) {
	wd, path := copyFixture(t)

	execNotebookEdit(t, wd, map[string]any{
		"notebook_path": path,
		"cell_number":   0,
		"edit_mode":     "delete",
	})

	nb, err := LoadNotebook(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(nb.Cells) != 1 {
		t.Fatalf("expected 1 cell after delete, got %d", len(nb.Cells))
	}
	if nb.Cells[0].CellType != "markdown" {
		t.Errorf("remaining cell type = %q, want markdown", nb.Cells[0].CellType)
	}
}
