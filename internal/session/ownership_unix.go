//go:build !windows

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The root service and the desktop CLI share the sessions directory. Keep
// private files accessible to that directory's owner when root writes them.
func sessionOwner(info os.FileInfo) (int, int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("session file owner unavailable")
	}
	return int(stat.Uid), int(stat.Gid), nil
}

func inheritSessionFileOwner(tempPath, targetPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	parent, err := os.Stat(filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	uid, gid, err := sessionOwner(parent)
	if err != nil {
		return err
	}
	if existing, err := os.Stat(targetPath); err == nil {
		oldUID, oldGID, err := sessionOwner(existing)
		if err != nil {
			return err
		}
		if oldUID != 0 {
			uid, gid = oldUID, oldGID
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Chown(tempPath, uid, gid)
}

func inheritSessionDirOwner(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	uid, gid, err := sessionOwner(parent)
	if err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil {
		return err
	}
	currentUID, _, err := sessionOwner(current)
	if err != nil {
		return err
	}
	if currentUID == 0 && uid != 0 {
		return os.Chown(path, uid, gid)
	}
	return nil
}
