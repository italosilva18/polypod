//go:build !windows

package automemory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Lock the stable parent directory inode. The root service and user CLI can
// both open it even when a root-owned private state file cannot be opened for
// writing by the user. A crash releases the advisory lock automatically.
func withDreamStateLease(ctx context.Context, stateFile string, action func() error) error {
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("locking dream state: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	actionErr := action()
	unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return errors.Join(actionErr, unlockErr)
}
