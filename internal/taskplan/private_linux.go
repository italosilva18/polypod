//go:build linux

package taskplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// prepareDatabaseFile uses directory-relative, no-follow operations while the
// process may be privileged. A root service owns this private directory so an
// unprivileged user cannot replace its database or WAL paths with symlinks.
func prepareDatabaseFile(dataDir string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("criando diretório de dados do plano: %w", err)
	}
	parentFD, err := unix.Open(dataDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("abrindo diretório de dados do plano: %w", err)
	}
	defer unix.Close(parentFD)
	if err := unix.Mkdirat(parentFD, "taskplan", 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return "", fmt.Errorf("criando diretório privado do plano: %w", err)
	}
	dirFD, err := unix.Openat(parentFD, "taskplan", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("abrindo diretório privado do plano: %w", err)
	}
	defer unix.Close(dirFD)
	if os.Geteuid() == 0 {
		if err := unix.Fchown(dirFD, 0, 0); err != nil {
			return "", fmt.Errorf("protegendo dono do plano: %w", err)
		}
	}
	if err := unix.Fchmod(dirFD, 0o700); err != nil {
		return "", fmt.Errorf("protegendo diretório do plano: %w", err)
	}
	if err := securePlanFile(dirFD, "plans.db", true); err != nil {
		return "", err
	}
	for _, name := range []string{"plans.db-wal", "plans.db-shm", "plans.db-journal"} {
		if err := securePlanFile(dirFD, name, false); err != nil {
			return "", err
		}
	}
	return filepath.Join(dataDir, "taskplan", "plans.db"), nil
}

func securePlanFile(dirFD int, name string, create bool) error {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if create {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_NOFOLLOW | unix.O_CLOEXEC
	}
	fd, err := unix.Openat(dirFD, name, flags, 0o600)
	if errors.Is(err, unix.ENOENT) && !create {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abrindo arquivo privado %s: %w", name, err)
	}
	defer unix.Close(fd)
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return fmt.Errorf("inspecionando arquivo privado %s: %w", name, err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
		return fmt.Errorf("arquivo privado %s não é regular ou possui links extras", name)
	}
	if os.Geteuid() == 0 {
		if err := unix.Fchown(fd, 0, 0); err != nil {
			return fmt.Errorf("protegendo dono de %s: %w", name, err)
		}
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return fmt.Errorf("protegendo arquivo %s: %w", name, err)
	}
	return nil
}

func secureAuxiliaryFiles(dataDir string) error {
	dirFD, err := unix.Open(filepath.Join(dataDir, "taskplan"),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("abrindo diretório privado do plano: %w", err)
	}
	defer unix.Close(dirFD)
	for _, name := range []string{"plans.db-wal", "plans.db-shm", "plans.db-journal"} {
		if err := securePlanFile(dirFD, name, false); err != nil {
			return err
		}
	}
	return nil
}
