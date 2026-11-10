//go:build !windows

package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const resourceWakeLockWait = 5 * time.Second

// The wake journal uses the same directory inode as subscription persistence,
// but waits with a deadline so an MCP callback cannot block indefinitely when
// another process is paused while holding the advisory lock.
func withResourceWakeFileLock(path string, action func() error) (resultErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("mcp: abrindo diretorio do diario: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	deadline := time.Now().Add(resourceWakeLockWait)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("mcp: bloqueando diario de recursos: %w", err)
		}
		if !time.Now().Before(deadline) {
			return errors.New("mcp: tempo de espera do diario de recursos excedido")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() { resultErr = errors.Join(resultErr, syscall.Flock(int(file.Fd()), syscall.LOCK_UN)) }()
	return action()
}
