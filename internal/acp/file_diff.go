package acp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/costa/polypod/internal/workdir"
)

const maxACPFileDiffBytes = 256 << 10

type acpFileSnapshot struct {
	path  string
	old   *string
	valid bool
}

func (s *Server) toolFilePath(sessionID string, args map[string]any) string {
	var path string
	for _, key := range []string{"path", "file_path", "filePath", "notebook_path"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			path = v
			break
		}
	}
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir.Dir(s.scoped(sessionID), ""), path)
	}
	return filepath.Clean(path)
}

func isACPFileMutationTool(name string) bool {
	normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "skill_")
	switch normalized {
	case "edit_file", "write_file", "file_edit", "file_write", "multi_edit", "undo_edit", "notebook_edit", "apply_patch":
		return true
	default:
		return false
	}
}

func readACPText(path string) (*string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxACPFileDiffBytes+1))
	if err != nil || len(data) > maxACPFileDiffBytes || !utf8.Valid(data) {
		return nil, io.ErrShortBuffer
	}
	value := string(data)
	return &value, nil
}

func snapshotACPFile(path string) acpFileSnapshot {
	snapshot := acpFileSnapshot{path: path, valid: true}
	old, err := readACPText(path)
	if err == nil {
		snapshot.old = old
	} else if !os.IsNotExist(err) {
		snapshot.valid = false
	}
	return snapshot
}

func (snapshot acpFileSnapshot) completedDiff() map[string]any {
	if !snapshot.valid {
		return nil
	}
	newText, err := readACPText(snapshot.path)
	if err != nil || newText == nil || (snapshot.old != nil && *snapshot.old == *newText) {
		return nil
	}
	return map[string]any{"type": "diff", "path": snapshot.path, "oldText": snapshot.old, "newText": *newText}
}
