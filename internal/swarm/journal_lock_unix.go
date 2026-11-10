//go:build !windows

package swarm

import (
	"errors"
	"os"
	"syscall"
)

func lockTeamJournalFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func teamJournalLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func unlockTeamJournalFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func teamJournalOwnerLive(path string) (bool, error) {
	live, missing, err := teamJournalLockState(path)
	if missing {
		return false, nil
	}
	return live, err
}
