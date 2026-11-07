package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/types"
)

func TestDocumentToolPaginatesUnicode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte("Olá mundo"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	RegisterDocumentTool(reg)
	tool, ok := reg.Get("document_extract")
	if !ok {
		t.Fatal("document tool not registered")
	}
	args := map[string]any{"path": "fixture.txt", "start": float64(0), "max_chars": float64(4)}
	if !tool.ValidateInput(args).Valid || !tool.IsReadOnly(args) || !tool.IsConcurrencySafe(args) {
		t.Fatal("tool metadata invalid")
	}
	result, err := tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, args)
	if err != nil || result.IsError || !strings.Contains(result.Content, "Olá ") || !strings.Contains(result.Content, "start=4") {
		t.Fatalf("first page = %+v, %v", result, err)
	}
	args["start"] = float64(4)
	result, err = tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, args)
	if err != nil || result.IsError || !strings.Contains(result.Content, "mund") {
		t.Fatalf("second page = %+v, %v", result, err)
	}
}

func TestDocumentToolRejectsInvalidPaging(t *testing.T) {
	reg := NewRegistry()
	RegisterDocumentTool(reg)
	tool, _ := reg.Get("document_extract")
	for _, args := range []map[string]any{
		{"path": ""},
		{"path": "a.txt", "start": -1.0},
		{"path": "a.txt", "max_chars": 0.0},
		{"path": "a.txt", "max_chars": 100001.0},
		{"path": "a.txt", "start": 1.5},
		{"path": "a.txt", "page_start": 1.0},
		{"path": "a.pdf", "page_end": 3.0},
		{"path": "a.pdf", "page_start": 3.0, "page_end": 2.0},
		{"path": "a.pdf", "page_start": 0.0},
	} {
		if tool.ValidateInput(args).Valid {
			t.Fatalf("accepted invalid arguments: %+v", args)
		}
	}
}
