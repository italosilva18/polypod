//go:build linux || darwin

package computeruse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// acquireDesktopLock serializes observations and actions across Polypod
// processes sharing the same desktop session. The OS releases the advisory
// lock if a process exits unexpectedly, so a stale lock file is harmless.
func acquireDesktopLock(ctx context.Context) (func() error, error) {
	if os.Getenv("POLYPOD_DESKTOP_LOCK") != "1" {
		return func() error { return nil }, nil
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("locate desktop lock directory: %w", err)
		}
	}
	dir := filepath.Join(base, "polypod")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create desktop lock directory: %w", err)
	}
	display := os.Getenv("WAYLAND_DISPLAY") + "|" + os.Getenv("DISPLAY")
	hash := sha256.Sum256([]byte(display))
	path := filepath.Join(dir, "desktop-"+hex.EncodeToString(hash[:8])+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open desktop lock: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				closeErr := file.Close()
				return errors.Join(unlockErr, closeErr)
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("lock desktop: %w", err), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
