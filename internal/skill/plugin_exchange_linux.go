//go:build linux

package skill

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// exchangePluginDirectories atomically swaps two populated directories when
// the filesystem and kernel support renameat2(RENAME_EXCHANGE).
func exchangePluginDirectories(destination, staged string) (bool, error) {
	err := unix.Renameat2(unix.AT_FDCWD, destination, unix.AT_FDCWD, staged, unix.RENAME_EXCHANGE)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EXDEV) {
		return false, nil
	}
	return false, fmt.Errorf("trocando diretorios do plugin: %w", err)
}
