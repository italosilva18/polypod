//go:build windows

package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

const resourceWakeLockWait = 5 * time.Second

func withResourceWakeFileLock(path string, action func() error) (resultErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("mcp: abrindo lock do diario de recursos: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	var overlapped windows.Overlapped
	deadline := time.Now().Add(resourceWakeLockWait)
	for {
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return fmt.Errorf("mcp: bloqueando diario de recursos: %w", err)
		}
		if !time.Now().Before(deadline) {
			return errors.New("mcp: tempo de espera do diario de recursos excedido")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer func() {
		resultErr = errors.Join(resultErr, windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped))
	}()
	return action()
}
