//go:build !windows

package workdir

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func withProcessLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := inheritSharedOwner(path, path); err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// inheritSharedOwner keeps a private state file readable by the account that
// owns the shared data directory, even when the service writes it as root.
func inheritSharedOwner(outputPath, destinationPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	parent, err := os.Stat(filepath.Dir(destinationPath))
	if err != nil {
		return err
	}
	parentStat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("não foi possível ler o dono do diretório")
	}
	uid, gid := int(parentStat.Uid), int(parentStat.Gid)
	if existing, err := os.Stat(destinationPath); err == nil {
		if stat, ok := existing.Sys().(*syscall.Stat_t); ok && stat.Uid != 0 {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Chown(outputPath, uid, gid)
}
