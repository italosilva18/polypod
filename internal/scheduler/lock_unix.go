//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLease uses a stable sidecar inode. Locking the JSON file itself would
// not work because each save replaces that inode atomically.
type fileLease struct{ file *os.File }

func (l *fileLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func acquireFileLease(path string, try bool) (*fileLease, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("criando diretório de lock do agendador: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("abrindo lock do agendador: %w", err)
	}
	flags := syscall.LOCK_EX
	if try {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), flags); err != nil {
		f.Close()
		if try && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("adquirindo lock do agendador: %w", err)
	}
	return &fileLease{file: f}, true, nil
}

func (s *Scheduler) stateLease() (*fileLease, error) {
	lease, _, err := acquireFileLease(s.dataFile+".state.lock", false)
	return lease, err
}

func (s *Scheduler) taskLease(id string) (*fileLease, bool, error) {
	hash := sha256.Sum256([]byte(id))
	name := s.dataFile + ".run." + hex.EncodeToString(hash[:12]) + ".lock"
	return acquireFileLease(name, true)
}
