//go:build !linux

package taskplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// prepareDatabaseFile keeps the same private layout on other platforms.
func prepareDatabaseFile(dataDir string) (string, error) {
	privateDir := filepath.Join(dataDir, "taskplan")
	if err := os.MkdirAll(privateDir, 0o700); err != nil {
		return "", fmt.Errorf("criando diretório privado do plano: %w", err)
	}
	info, err := os.Lstat(privateDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("diretório do plano precisa ser um diretório real")
	}
	if err := os.Chmod(privateDir, 0o700); err != nil {
		return "", fmt.Errorf("protegendo diretório do plano: %w", err)
	}
	path := filepath.Join(privateDir, "plans.db")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("banco do plano precisa ser um arquivo regular")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("abrindo banco do plano: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return "", fmt.Errorf("protegendo banco do plano: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func secureAuxiliaryFiles(dataDir string) error {
	privateDir := filepath.Join(dataDir, "taskplan")
	for _, name := range []string{"plans.db-wal", "plans.db-shm", "plans.db-journal"} {
		path := filepath.Join(privateDir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("arquivo auxiliar %s precisa ser regular", name)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protegendo arquivo auxiliar %s: %w", name, err)
		}
	}
	return nil
}
