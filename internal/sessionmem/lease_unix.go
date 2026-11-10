//go:build !windows

package sessionmem

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Locking the stable directory inode coordinates separate FileStore instances
// without creating a file that another process may be unable to open.
func lockStoreDirectory(dir string) (func() error, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("sessionmem: abrindo diretorio para lock: %w", err)
	}
	deadline := time.Now().Add(storeLeaseTimeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("sessionmem: adquirindo lock: %w", err), file.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(errors.New("sessionmem: tempo de espera do lock excedido"), file.Close())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
