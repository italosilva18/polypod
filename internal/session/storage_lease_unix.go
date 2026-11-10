//go:build !windows

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// The stable session directory inode coordinates separate Polypod processes
// without creating a lock file owned by a different user.
func lockSessionDirectory(dir string) (func() error, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("abrindo diretório das sessões para lock: %w", err)
	}
	deadline := time.Now().Add(sessionLeaseTimeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("adquirindo lock das sessões: %w", err), file.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(fmt.Errorf("tempo de espera do lock das sessões excedido"), file.Close())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
