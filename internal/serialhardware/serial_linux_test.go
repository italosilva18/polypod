//go:build linux

package serialhardware

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSerialExchangeWithPseudoTerminal(t *testing.T) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	defer unix.Close(master)
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprintf("/dev/pts/%d", number)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	responded := make(chan error, 1)
	go func() {
		buf := make([]byte, 4)
		if err := waitFD(ctx, master, unix.POLLIN, time.Now().Add(2*time.Second)); err != nil {
			responded <- err
			return
		}
		n, err := unix.Read(master, buf)
		if err != nil {
			responded <- err
			return
		}
		if string(buf[:n]) != "ping" {
			responded <- fmt.Errorf("received %q, want ping", buf[:n])
			return
		}
		if err := waitFD(ctx, master, unix.POLLOUT, time.Now().Add(2*time.Second)); err != nil {
			responded <- err
			return
		}
		if n, err := unix.Write(master, []byte("pong")); err != nil || n != 4 {
			responded <- fmt.Errorf("write response %d: %w", n, err)
			return
		}
		responded <- nil
	}()
	service := NewService()
	result, err := service.Exchange(ctx, SerialConfig{Port: port, Baud: 115200, Timeout: 2 * time.Second}, []byte("ping"), 4)
	if err != nil || result.Written != 4 || !reflect.DeepEqual(result.Received, []byte("pong")) {
		t.Fatalf("PTY exchange = %+v, %v", result, err)
	}
	select {
	case err := <-responded:
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("PTY responder timed out")
	}
}
