//go:build windows

package turncheckpoint

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func lockCheckpointFile(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	var overlapped windows.Overlapped
	deadline := time.Now().Add(timeout)
	for {
		err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			return func() error { return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped) }, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, errCheckpointLockTimeout
		}
		time.Sleep(25 * time.Millisecond)
	}
}
