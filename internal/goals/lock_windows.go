//go:build windows

package goals

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func withGoalFileLock(path string, action func() error) error {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("abrindo lock de metas: %w", err)
	}
	defer file.Close()
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("adquirindo lock de metas: %w", err)
	}
	actionErr := action()
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	return errors.Join(actionErr, unlockErr)
}
