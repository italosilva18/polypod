//go:build linux || darwin || freebsd || netbsd || openbsd

package skill

import (
	"fmt"
	"os"
	"syscall"
)

func setPluginAccess(path string, mode os.FileMode, gid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("plugin contem caminho especial: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("nao foi possivel verificar grupo do plugin: %s", path)
	}
	changedGroup := int(stat.Gid) != gid
	if changedGroup {
		if err := os.Chown(path, -1, gid); err != nil {
			return fmt.Errorf("definindo grupo do plugin: %w", err)
		}
	}
	if changedGroup || info.Mode()&(os.ModePerm|os.ModeSetgid) != mode {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("definindo permissoes do plugin: %w", err)
		}
	}
	return nil
}

func setPluginFileAccess(file *os.File, mode os.FileMode, gid int) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("arquivo de plugin invalido: %s", file.Name())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("nao foi possivel verificar grupo do plugin: %s", file.Name())
	}
	changedGroup := int(stat.Gid) != gid
	if changedGroup {
		if err := file.Chown(-1, gid); err != nil {
			return fmt.Errorf("definindo grupo do arquivo do plugin: %w", err)
		}
	}
	if changedGroup || info.Mode().Perm() != mode.Perm() {
		if err := file.Chmod(mode); err != nil {
			return fmt.Errorf("definindo permissoes do arquivo do plugin: %w", err)
		}
	}
	return nil
}
