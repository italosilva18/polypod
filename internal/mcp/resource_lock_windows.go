//go:build windows

package mcp

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func withSubscriptionFileLock(path string, action func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return err
	}
	defer windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped) //nolint:errcheck
	return action()
}
