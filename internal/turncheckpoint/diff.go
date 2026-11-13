package turncheckpoint

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pmezard/go-difflib/difflib"
)

// FileDiff is a read-only view of one file changed by a completed turn.
type FileDiff struct {
	Path    string
	Patch   string
	Warning string
}

// DiffTurn compares a turn's private pre-write blobs with current files.
// A warning identifies files modified again after the selected turn.
func (m *Manager) DiffTurn(sessionID, turnID string) ([]FileDiff, error) {
	if turnID == "" {
		return nil, os.ErrNotExist
	}
	return m.DiffTurns(sessionID, []string{turnID})
}

// DiffTurns compares the earliest pre-write state across turnIDs (ordered
// oldest to newest) with the current files on disk.
func (m *Manager) DiffTurns(sessionID string, turnIDs []string) ([]FileDiff, error) {
	if sessionID == "" || len(turnIDs) == 0 {
		return nil, os.ErrNotExist
	}
	m.mu.Lock()
	first := make(map[string]Snapshot)
	firstOwner := make(map[string]identity)
	last := make(map[string]Snapshot)
	foundAny := false
	for _, turnID := range turnIDs {
		id := identity{sessionID: sessionID, turnID: turnID}
		record, err := m.loadRecord(id)
		if err != nil {
			if len(turnIDs) == 1 {
				m.mu.Unlock()
				return nil, err
			}
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			m.mu.Unlock()
			return nil, err
		}
		foundAny = true
		for path, snap := range record.Files {
			if _, exists := first[path]; !exists {
				first[path] = snap
				firstOwner[path] = id
			}
			last[path] = snap
		}
	}
	m.mu.Unlock()
	if !foundAny {
		if len(turnIDs) == 1 {
			return nil, os.ErrNotExist
		}
		return nil, nil
	}

	paths := make([]string, 0, len(first))
	for path := range first {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	results := make([]FileDiff, 0, len(paths))
	for _, path := range paths {
		snap := first[path]
		owner := firstOwner[path]
		expected := last[path]
		if snap.BeforeExists && (snap.Blob == "" || filepath.Base(snap.Blob) != snap.Blob) {
			return nil, fmt.Errorf("nome de blob inválido no checkpoint")
		}
		var before []byte
		if snap.BeforeExists {
			blobPath := filepath.Join(m.turnDir(owner), snap.Blob)
			if info, statErr := os.Stat(blobPath); statErr != nil || info.Size() > 2*1024*1024 {
				results = append(results, FileDiff{Path: path, Patch: "Arquivo grande ou checkpoint indisponível.", Warning: "Prévia limitada."})
				continue
			}
			var err error
			before, err = os.ReadFile(blobPath)
			if err != nil {
				return nil, fmt.Errorf("lendo versão anterior de %s: %w", path, err)
			}
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Size() > 2*1024*1024 {
			results = append(results, FileDiff{Path: path, Patch: "Arquivo grande: diff omitido.", Warning: "Prévia limitada."})
			continue
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		if bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
			results = append(results, FileDiff{Path: path, Patch: "Arquivo binário alterado.", Warning: "Prévia textual indisponível."})
			continue
		}
		warning := ""
		if expected.AfterKnown && (expected.AfterExists != (readErr == nil) || (readErr == nil && expected.AfterHash != digest(after))) {
			warning = "Arquivo alterado novamente após este turno; comparação usa a versão atual."
		}
		patch, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: difflib.SplitLines(string(before)), B: difflib.SplitLines(string(after)),
			FromFile: "a/" + path, ToFile: "b/" + path, Context: 3,
		})
		if err != nil {
			return nil, err
		}
		if patch != "" {
			results = append(results, FileDiff{Path: path, Patch: patch, Warning: warning})
		}
	}
	return results, nil
}
