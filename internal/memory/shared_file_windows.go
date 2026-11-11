//go:build windows

package memory

import (
	"os"

	"golang.org/x/sys/windows"
)

func ensureSharedDirectory(dir string) (int, int, error) {
	return 0, 0, os.MkdirAll(dir, 0700)
}

func setSharedFileOwner(file *os.File, _, _ int) error { return nil }

func lockSharedMemory(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}

func unlockSharedMemory(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func syncSharedDirectory(string) error { return nil }
