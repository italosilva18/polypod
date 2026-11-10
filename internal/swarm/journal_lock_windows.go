//go:build windows

package swarm

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockTeamJournalFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|
		windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

func teamJournalLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func unlockTeamJournalFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func teamJournalOwnerLive(path string) (bool, error) {
	live, missing, err := teamJournalLockState(path)
	if missing {
		return false, nil
	}
	return live, err
}
