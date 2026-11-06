//go:build linux

package hardware

import (
	"errors"
	"fmt"
	"syscall"
)

func openCharacterDevice(path string) (int, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("abrindo %s: %w", path, err)
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		closeErr := syscall.Close(fd)
		return -1, fmt.Errorf("inspecionando %s: %w", path, errors.Join(err, closeErr))
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		closeErr := syscall.Close(fd)
		return -1, errors.Join(errors.New("o caminho de hardware não é dispositivo de caracteres"), closeErr)
	}
	return fd, nil
}

func closeCharacterDevice(fd int, resultErr *error) {
	if err := syscall.Close(fd); err != nil {
		*resultErr = errors.Join(*resultErr, fmt.Errorf("fechando dispositivo de hardware: %w", err))
	}
}

func deviceIOCTL(fd int, request uintptr, argument uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, argument)
	if errno != 0 {
		return errno
	}
	return nil
}
