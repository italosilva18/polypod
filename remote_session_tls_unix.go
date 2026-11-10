//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const remoteTLSPEMLimit = 1 << 20

// readHostTLSFile reads a host certificate or private key without following
// the final path component. private selects the key rules: mode 0600 or
// stricter, owner equal to the effective uid, and a directory walk that
// rejects group or other write permission.
func readHostTLSFile(path string, private bool) ([]byte, error) {
	envName := "POLYPOD_REMOTE_SESSION_TLS_CERT"
	if private {
		envName = "POLYPOD_REMOTE_SESSION_TLS_KEY"
	}
	clean, parts, err := absoluteTLSPath(path, envName)
	if err != nil {
		return nil, err
	}
	data, st, err := readWalkedFile(clean, parts, envName, private)
	if err != nil {
		return nil, err
	}
	if private {
		if st.Mode&0o077 != 0 {
			return nil, fmt.Errorf("%s precisa ter permissão 0600 ou mais restrita", envName)
		}
		if st.Uid != uint32(os.Geteuid()) {
			return nil, fmt.Errorf("%s precisa pertencer ao usuário efetivo do processo", envName)
		}
	}
	return data, nil
}

// readRemoteSessionCAFile reads the client trust bundle. The file must be a
// regular non-symlink and must not be writable by group or other.
func readRemoteSessionCAFile(path string) ([]byte, error) {
	const envName = "POLYPOD_REMOTE_SESSION_TLS_CA"
	clean, parts, err := absoluteTLSPath(path, envName)
	if err != nil {
		return nil, err
	}
	data, st, err := readWalkedFile(clean, parts, envName, true)
	if err != nil {
		return nil, err
	}
	if st.Mode&0o022 != 0 {
		return nil, fmt.Errorf("%s não pode ser gravável por grupo ou por outros", envName)
	}
	return data, nil
}

func absoluteTLSPath(path, envName string) (string, []string, error) {
	if !filepath.IsAbs(path) {
		return "", nil, fmt.Errorf("%s precisa ser um caminho absoluto", envName)
	}
	clean := filepath.Clean(path)
	if clean == string(os.PathSeparator) {
		return "", nil, fmt.Errorf("%s precisa ser um arquivo", envName)
	}
	parts := strings.Split(strings.TrimPrefix(clean, string(os.PathSeparator)), string(os.PathSeparator))
	if len(parts) == 0 || parts[0] == "" {
		return "", nil, fmt.Errorf("%s precisa ser um arquivo", envName)
	}
	return clean, parts, nil
}

// readWalkedFile opens each directory with O_NOFOLLOW and then the file from
// that directory descriptor. The directory that contains the file cannot be
// group- or other-writable. An ancestor may be group- or other-writable only
// when it has the sticky bit.
func readWalkedFile(clean string, parts []string, envName string, checkDirs bool) ([]byte, syscall.Stat_t, error) {
	dirfd, err := syscall.Open(string(os.PathSeparator), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscall.Stat_t{}, tlsOpenError(envName, err)
	}
	current := string(os.PathSeparator)
	if err := checkTLSDirectory(dirfd, current, len(parts) == 1, checkDirs, envName); err != nil {
		return nil, syscall.Stat_t{}, errors.Join(err, syscall.Close(dirfd))
	}
	for i, name := range parts[:len(parts)-1] {
		next, openErr := syscall.Openat(dirfd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		closeErr := syscall.Close(dirfd)
		if openErr != nil {
			return nil, syscall.Stat_t{}, errors.Join(tlsOpenError(envName, openErr), closeErr)
		}
		if closeErr != nil {
			return nil, syscall.Stat_t{}, errors.Join(fmt.Errorf("fechando diretório de %s: %w", envName, closeErr), syscall.Close(next))
		}
		dirfd = next
		current = filepath.Join(current, name)
		if err := checkTLSDirectory(dirfd, current, i == len(parts)-2, checkDirs, envName); err != nil {
			return nil, syscall.Stat_t{}, errors.Join(err, syscall.Close(dirfd))
		}
	}
	filefd, err := syscall.Openat(dirfd, parts[len(parts)-1], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	closeErr := syscall.Close(dirfd)
	if err != nil {
		return nil, syscall.Stat_t{}, errors.Join(tlsOpenError(envName, err), closeErr)
	}
	if closeErr != nil {
		return nil, syscall.Stat_t{}, errors.Join(fmt.Errorf("fechando diretório de %s: %w", envName, closeErr), syscall.Close(filefd))
	}
	return readLimitedFD(filefd, clean, envName)
}

func checkTLSDirectory(fd int, path string, parent, enforce bool, envName string) error {
	if !enforce {
		return nil
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("lendo diretório das sessões remotas: %w", err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return fmt.Errorf("diretório %s de certificado TLS das sessões remotas inválido", path)
	}
	writable := st.Mode&0o022 != 0
	sticky := st.Mode&syscall.S_ISVTX != 0
	if writable && (parent || !sticky) {
		if parent {
			return fmt.Errorf("diretório %s de %s não pode ser gravável por grupo ou por outros", path, envName)
		}
		return fmt.Errorf("diretório ancestral %s de %s não pode ser gravável por grupo ou por outros", path, envName)
	}
	return nil
}

func readLimitedFD(fd int, path, envName string) ([]byte, syscall.Stat_t, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, syscall.Stat_t{}, errors.Join(fmt.Errorf("lendo %s: %w", envName, err), syscall.Close(fd))
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return nil, syscall.Stat_t{}, errors.Join(
			fmt.Errorf("%s precisa ser um arquivo regular e não um link simbólico", envName),
			syscall.Close(fd),
		)
	}
	if st.Size <= 0 || st.Size > remoteTLSPEMLimit {
		return nil, syscall.Stat_t{}, errors.Join(
			fmt.Errorf("%s precisa ter entre 1 byte e 1 MiB", envName),
			syscall.Close(fd),
		)
	}
	file := os.NewFile(uintptr(fd), path)
	buf := make([]byte, st.Size)
	_, readErr := io.ReadFull(file, buf)
	closeErr := file.Close()
	if readErr != nil {
		return nil, syscall.Stat_t{}, errors.Join(fmt.Errorf("lendo %s: %w", envName, readErr), closeErr)
	}
	if closeErr != nil {
		return nil, syscall.Stat_t{}, fmt.Errorf("fechando %s: %w", envName, closeErr)
	}
	return buf, st, nil
}

func tlsOpenError(envName string, err error) error {
	if errors.Is(err, syscall.ELOOP) {
		return fmt.Errorf("%s não pode ser um link simbólico", envName)
	}
	return fmt.Errorf("lendo %s: %w", envName, err)
}
