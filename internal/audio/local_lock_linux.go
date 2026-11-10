//go:build linux

package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// acquireModelLock uses the model inode as an advisory lock shared by a root
// service and an independently launched user CLI. The file or directory only
// needs to be readable; waiting ends when the transcription context expires.
func acquireModelLock(ctx context.Context, model string) (func(), error) {
	file, err := os.Open(model)
	if err != nil {
		return nil, fmt.Errorf("abrindo modelo para posse: %w", err)
	}
	cleanup := func() { _ = file.Close() }
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				cleanup()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			cleanup()
			return nil, fmt.Errorf("posse do modelo local: %w", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			cleanup()
			return nil, ctx.Err()
		}
	}
}
