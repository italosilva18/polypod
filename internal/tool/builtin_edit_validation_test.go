package tool

import (
	"testing"

	"github.com/costa/polypod/internal/types"
)

func TestEditFileRejectsMissingOrDirectoryPathBeforeExecution(t *testing.T) {
	edit := types.BuildTool(newEditFileTool(t.TempDir()))
	for _, args := range []map[string]any{
		{"old_text": "before", "new_text": "after"},
		{"path": ".", "old_text": "before", "new_text": "after"},
		{"path": "math.go", "old_text": "", "new_text": "after"},
	} {
		if result := edit.ValidateInput(args); result.Valid || result.Message == "" {
			t.Fatalf("invalid edit accepted: %+v -> %+v", args, result)
		}
	}
	if result := edit.ValidateInput(map[string]any{"path": "math.go", "old_text": "before", "new_text": "after"}); !result.Valid {
		t.Fatalf("valid edit rejected: %+v", result)
	}
}
