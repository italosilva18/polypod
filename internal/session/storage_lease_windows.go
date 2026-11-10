//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func lockSessionDirectory(dir string) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(dir, ".sessions.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("abrindo lock das sessões: %w", err)
	}
	var overlapped windows.Overlapped
	deadline := time.Now().Add(sessionLeaseTimeout)
	for {
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			return func() error {
				return errors.Join(windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped), file.Close())
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.Join(fmt.Errorf("adquirindo lock das sessões: %w", err), file.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(fmt.Errorf("tempo de espera do lock das sessões excedido"), file.Close())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
