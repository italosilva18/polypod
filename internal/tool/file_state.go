package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/costa/polypod/internal/lsp"
	"github.com/costa/polypod/internal/projectchecks"
	"github.com/costa/polypod/internal/types"
)

// fileReadTracker keeps the version last shown to each session. It is bounded
// so long-lived agents cannot retain an unlimited number of file identities.
type fileReadTracker struct {
	mu       sync.Mutex
	versions map[fileReadKey][32]byte
	preWrite map[string][]byte
}

type fileReadKey struct {
	session types.SessionID
	path    string
}

func newFileReadTracker() *fileReadTracker {
	return &fileReadTracker{
		versions: make(map[fileReadKey][32]byte),
		preWrite: make(map[string][]byte),
	}
}

func (r *Registry) stashPreWrite(path string, original []byte, existed bool) {
	if r == nil || r.fileReads == nil {
		return
	}
	tracker := r.fileReads
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.preWrite) >= 256 {
		clear(tracker.preWrite)
	}
	id := fileIdentity(path)
	if !existed || len(original) == 0 {
		delete(tracker.preWrite, id)
		return
	}
	cp := make([]byte, len(original))
	copy(cp, original)
	tracker.preWrite[id] = cp
}

func (r *Registry) popPreWrite(path string) []byte {
	if r == nil || r.fileReads == nil {
		return nil
	}
	tracker := r.fileReads
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	id := fileIdentity(path)
	prev := tracker.preWrite[id]
	delete(tracker.preWrite, id)
	return prev
}

func fileIdentity(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func (r *Registry) recordFileVersion(session types.SessionID, path string, content []byte) {
	if session == "" || r.fileReads == nil {
		return
	}
	tracker := r.fileReads
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if len(tracker.versions) >= 4096 {
		clear(tracker.versions)
	}
	tracker.versions[fileReadKey{session: session, path: fileIdentity(path)}] = sha256.Sum256(content)
}

func (r *Registry) hasStaleFileVersion(session types.SessionID, path string, content []byte) bool {
	if !fileWriteGuardEnabled() || session == "" || r.fileReads == nil {
		return false
	}
	tracker := r.fileReads
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	want, ok := tracker.versions[fileReadKey{session: session, path: fileIdentity(path)}]
	return ok && want != sha256.Sum256(content)
}

// fileStillMatches narrows the time between validation and a write. This is
// also useful when the caller did not previously use the read_file tool.
func fileStillMatches(path string, existed bool, original []byte) (bool, error) {
	if !fileWriteGuardEnabled() {
		return true, nil
	}
	current, err := os.ReadFile(path)
	if !existed {
		if os.IsNotExist(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(current, original), nil
}

func writeNewFileExclusive(path string, content []byte, mode os.FileMode) error {
	if !fileWriteGuardEnabled() {
		return os.WriteFile(path, content, mode)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err = file.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func fileWriteGuardEnabled() bool {
	return os.Getenv("POLYPOD_FILE_WRITE_GUARD") == "1"
}

func staleFileResult(path string) types.ToolResult {
	return types.ToolResult{
		Content: fmt.Sprintf("O arquivo mudou desde a leitura: %s. Leia-o novamente antes de editar.", path),
		IsError: true,
	}
}

func beforeFileWriteError(err error) types.ToolResult {
	return types.ToolResult{Content: fmt.Sprintf("Erro ao preparar gravacao do arquivo: %v", err), IsError: true}
}

// SetBeforeFileWrite installs a hook for checkpointing the exact pre-write
// state. A nil hook restores the default direct-write behavior.
func (r *Registry) SetBeforeFileWrite(hook func(types.ToolContext, string, []byte, bool) error) {
	r.mu.Lock()
	r.beforeFileWrite = hook
	r.mu.Unlock()
}

// SetAfterFileWrite installs an optional formatter and verifier callback.
func (r *Registry) SetAfterFileWrite(hook func(context.Context, string) (string, error)) {
	r.mu.Lock()
	r.afterFileWriteHook = hook
	r.mu.Unlock()
}

func (r *Registry) runBeforeFileWrite(ctx types.ToolContext, path string, original []byte, existed bool) error {
	r.stashPreWrite(path, original, existed)
	r.mu.RLock()
	hook := r.beforeFileWrite
	r.mu.RUnlock()
	if hook == nil {
		return nil
	}
	return hook(ctx, path, original, existed)
}

// AttachLSP connects live file writes and the LSP query tool to a manager.
// It is safe to call after the file tools have already been registered.
func (r *Registry) AttachLSP(manager *lsp.LSPServerManager) {
	r.mu.Lock()
	r.lsp = manager
	r.mu.Unlock()
	if manager != nil {
		r.Register(lsp.NewLSPTool(manager, manager.Diagnostics()))
	}
}

func (r *Registry) notifyFileWritten(path, content string) string {
	r.mu.RLock()
	manager := r.lsp
	r.mu.RUnlock()
	if manager == nil {
		return ""
	}
	if err := manager.ChangeFile(path, content); err != nil {
		return fmt.Sprintf("Aviso LSP: %v", err)
	}
	if err := manager.SaveFile(path); err != nil {
		return fmt.Sprintf("Aviso LSP: %v", err)
	}
	if files := manager.Diagnostics().CheckPending(); len(files) > 0 {
		return lsp.FormatDiagnostics(files, "")
	}
	return ""
}

func (r *Registry) afterFileWrite(ctx types.ToolContext, path, content string, result types.ToolResult) types.ToolResult {
	prevOriginal := r.popPreWrite(path)
	r.mu.RLock()
	hook := r.afterFileWriteHook
	r.mu.RUnlock()
	if hook != nil {
		notice, err := hook(ctx.Context, path)
		if err != nil {
			result.Content += "\n\nAviso de verificação: " + err.Error()
		} else if notice != "" {
			result.Content += "\n\n" + notice
		}
		if updated, readErr := os.ReadFile(path); readErr == nil {
			content = string(updated)
		}
	}
	if fastDiag := projectchecks.FastDiagnoseFile(ctx.Context, path); fastDiag != "" {
		result.Content += "\n\n" + fastDiag
	}
	if len(prevOriginal) > 0 {
		if impact := projectchecks.AnalyzeEditImpact(ctx.Context, path, prevOriginal, []byte(content)); impact != "" {
			result.Content += "\n\n" + impact
		}
	}
	r.recordFileVersion(ctx.SessionID, path, []byte(content))
	if notice := r.notifyFileWritten(path, content); notice != "" {
		result.Content += "\n\n" + notice
	}
	return result
}
