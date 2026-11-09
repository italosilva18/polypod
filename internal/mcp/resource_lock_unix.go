//go:build !windows

package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock the shared directory instead of a root-owned lock file. Root and the
// directory owner can both acquire this advisory lock, and a crash releases
// it automatically.
func withSubscriptionFileLock(path string, action func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lockDir, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer lockDir.Close()
	if err := syscall.Flock(int(lockDir.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("mcp: bloqueando arquivo de assinaturas: %w", err)
	}
	defer syscall.Flock(int(lockDir.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return action()
}
