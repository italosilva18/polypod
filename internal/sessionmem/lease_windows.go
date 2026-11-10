//go:build windows

package sessionmem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func lockStoreDirectory(dir string) (func() error, error) {
	file, err := os.OpenFile(filepath.Join(dir, ".store.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sessionmem: abrindo arquivo de lock: %w", err)
	}
	var overlapped windows.Overlapped
	deadline := time.Now().Add(storeLeaseTimeout)
	for {
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			return func() error {
				return errors.Join(windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped), file.Close())
			}, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.Join(fmt.Errorf("sessionmem: adquirindo lock: %w", err), file.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(errors.New("sessionmem: tempo de espera do lock excedido"), file.Close())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
