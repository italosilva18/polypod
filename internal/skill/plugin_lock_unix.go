//go:build linux || darwin || freebsd || netbsd || openbsd

package skill

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

func lockPluginRoot(ctx context.Context, parent string) (func() error, error) {
	// Both the root service and the desktop user can open this existing
	// directory. Locking its inode avoids a lock file affected by their umasks.
	file, err := os.Open(parent)
	if err != nil {
		return nil, fmt.Errorf("abrindo diretorio para lock de plugins: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				closeErr := file.Close()
				if unlockErr != nil {
					return unlockErr
				}
				return closeErr
			}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = file.Close()
			return nil, fmt.Errorf("adquirindo lock de plugins: %w", err)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
