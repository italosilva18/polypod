package turncheckpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var ErrBranchBusy = errors.New("outra instância do CLI está usando esta sessão")
var ErrAICheckpointConflict = errors.New("checkpoint de IA alterado por outra instância")

// AcquireBranchLease serializes full CLI turns across processes. Callers must
// acquire it before loading the conversation and hold it until the assistant
// message and session snapshot have been durably published. A process crash
// releases the operating-system lock automatically.
func (m *Manager) AcquireBranchLease(ctx context.Context, branchID string) (func() error, error) {
	if branchID == "" {
		return nil, errors.New("branch de turno vazia")
	}
	path := filepath.Join(m.root, digest([]byte(branchID)), ".branch.lock")
	release, err := acquireCheckpointFileLock(ctx, path, time.Second)
	if errors.Is(err, errCheckpointLockTimeout) {
		return nil, ErrBranchBusy
	}
	return release, err
}

var errCheckpointLockTimeout = errors.New("checkpoint lock timeout")

func acquireCheckpointFileLock(ctx context.Context, path string, timeout time.Duration) (func() error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("criando diretório de lock: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("lock de checkpoint não é arquivo regular: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("abrindo lock de checkpoint: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	release, err := lockCheckpointFile(ctx, file, timeout)
	if err != nil {
		file.Close()
		return nil, err
	}
	return func() error { return errors.Join(release(), file.Close()) }, nil
}
