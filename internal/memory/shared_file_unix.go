//go:build !windows

package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func ensureSharedDirectory(dir string) (int, int, error) {
	if err := rejectSharedSymlink(dir); err != nil {
		return 0, 0, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return 0, 0, fmt.Errorf("creating owner memory directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("cannot inspect owner memory directory owner")
	}
	uid, gid := int(stat.Uid), int(stat.Gid)
	if os.Geteuid() == 0 && uid == 0 {
		for parent := filepath.Dir(dir); parent != dir; parent = filepath.Dir(parent) {
			parentInfo, err := os.Stat(parent)
			if err != nil {
				return 0, 0, err
			}
			parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
			if ok && parentStat.Uid != 0 {
				uid, gid = int(parentStat.Uid), int(parentStat.Gid)
				if err := os.Chown(dir, uid, gid); err != nil {
					return 0, 0, err
				}
				break
			}
			if parent == filepath.Dir(parent) {
				break
			}
		}
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func setSharedFileOwner(file *os.File, uid, gid int) error {
	if os.Geteuid() == 0 {
		if err := file.Chown(uid, gid); err != nil {
			return err
		}
	}
	return file.Chmod(0600)
}

func lockSharedMemory(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func unlockSharedMemory(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func syncSharedDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
