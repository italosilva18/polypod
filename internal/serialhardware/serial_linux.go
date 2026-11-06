//go:build linux

package serialhardware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type platformTransport struct{}

func baudConstant(baud int) (uint32, error) {
	switch baud {
	case 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	case 230400:
		return unix.B230400, nil
	case 460800:
		return unix.B460800, nil
	case 921600:
		return unix.B921600, nil
	default:
		return 0, fmt.Errorf("baud não suportado: %d", baud)
	}
}

func (platformTransport) Exchange(ctx context.Context, cfg SerialConfig, payload []byte, readBytes int) (result ExchangeResult, returnErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	info, err := os.Stat(cfg.Port)
	if err != nil {
		return result, err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return result, fmt.Errorf("porta não é dispositivo de caracteres: %s", cfg.Port)
	}
	fd, err := unix.Open(cfg.Port, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := unix.Close(fd); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	if err := configurePort(fd, cfg.Baud); err != nil {
		return result, err
	}
	deadline := time.Now().Add(cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for result.Written < len(payload) {
		if err := waitFD(ctx, fd, unix.POLLOUT, deadline); err != nil {
			return result, fmt.Errorf("envio serial: %w", err)
		}
		n, err := unix.Write(fd, payload[result.Written:])
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return result, err
		}
		result.Written += n
	}
	if readBytes == 0 {
		return result, nil
	}
	result.Received = make([]byte, 0, readBytes)
	buf := make([]byte, readBytes)
	for len(result.Received) < readBytes {
		err := waitFD(ctx, fd, unix.POLLIN, deadline)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			result.TimedOut = true
			return result, nil
		}
		if err != nil {
			return result, fmt.Errorf("leitura serial: %w", err)
		}
		n, err := unix.Read(fd, buf[:readBytes-len(result.Received)])
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return result, err
		}
		if n == 0 {
			continue
		}
		result.Received = append(result.Received, buf[:n]...)
	}
	return result, nil
}

func configurePort(fd, baud int) error {
	speed, err := baudConstant(baud)
	if err != nil {
		return err
	}
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	termios.Iflag = 0
	termios.Oflag = 0
	termios.Lflag = 0
	termios.Cflag = unix.CREAD | unix.CLOCAL | unix.CS8 | speed
	termios.Ispeed = speed
	termios.Ospeed = speed
	termios.Cc[unix.VMIN] = 0
	termios.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(fd, unix.TCSETS, termios)
}

func waitFD(ctx context.Context, fd int, event int16, deadline time.Time) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		pollMS := int(remaining.Milliseconds())
		if pollMS < 1 {
			pollMS = 1
		}
		if pollMS > 100 {
			pollMS = 100
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: event}}
		_, err := unix.Poll(fds, pollMS)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if fds[0].Revents&event != 0 {
			return nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return io.ErrUnexpectedEOF
		}
	}
}
