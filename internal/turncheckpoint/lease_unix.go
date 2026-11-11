//go:build !windows

package turncheckpoint

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func lockCheckpointFile(ctx context.Context, file *os.File, timeout time.Duration) (func() error, error) {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error { return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
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
