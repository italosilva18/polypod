//go:build !windows

package goals

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// The lock file is stable across atomic replacements of goals.json.
func withGoalFileLock(path string, action func() error) error {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("abrindo lock de metas: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("adquirindo lock de metas: %w", err)
	}
	actionErr := action()
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return errors.Join(actionErr, unlockErr)
}
