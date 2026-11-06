//go:build linux

package hardware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"unsafe"
)

// Request numbers and spiTransfer layout are from linux/spi/spidev.h.
const (
	spiWriteMode  = 0x40016b01
	spiWriteBits  = 0x40016b03
	spiWriteSpeed = 0x40046b04
	spiMessageOne = 0x40206b00
)

var spiDeviceName = regexp.MustCompile(`^spidev([0-9]+)\.([0-9]+)$`)

type linuxSPI struct{}

func NewSPI() SPI { return linuxSPI{} }

func (linuxSPI) Devices(ctx context.Context) ([]string, error) {
	paths, err := filepath.Glob("/dev/spidev*")
	if err != nil {
		return nil, err
	}
	var devices []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		if spiDeviceName.FindStringSubmatch(name) == nil {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeCharDevice != 0 {
			devices = append(devices, name[len("spidev"):])
		}
	}
	sort.Strings(devices)
	return devices, nil
}

type spiTransfer struct {
	TxBuffer    uint64
	RxBuffer    uint64
	Length      uint32
	SpeedHz     uint32
	DelayUsec   uint16
	BitsPerWord uint8
	CSChange    uint8
	TxNBits     uint8
	RxNBits     uint8
	WordDelay   uint8
	Padding     uint8
}

func (linuxSPI) Transfer(ctx context.Context, device string, config SPIConfig, tx []byte) (rx []byte, resultErr error) {
	path, err := spiPath(device)
	if err != nil {
		return nil, err
	}
	if config.SpeedHz < 1 || config.SpeedHz > 125_000_000 {
		return nil, errors.New("velocidade SPI deve estar entre 1 Hz e 125 MHz")
	}
	if config.Mode > 3 || config.Bits < 1 || config.Bits > 32 {
		return nil, errors.New("modo SPI deve ser 0..3 e bits por palavra 1..32")
	}
	if len(tx) < 1 || len(tx) > 4096 {
		return nil, errors.New("transferência SPI deve conter 1..4096 bytes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := openCharacterDevice(path)
	if err != nil {
		return nil, err
	}
	defer closeCharacterDevice(fd, &resultErr)
	mode, bits, speed := config.Mode, config.Bits, config.SpeedHz
	for _, setting := range []struct {
		name string
		req  uintptr
		ptr  uintptr
	}{
		{name: "modo", req: spiWriteMode, ptr: uintptr(unsafe.Pointer(&mode))},
		{name: "bits por palavra", req: spiWriteBits, ptr: uintptr(unsafe.Pointer(&bits))},
		{name: "velocidade", req: spiWriteSpeed, ptr: uintptr(unsafe.Pointer(&speed))},
	} {
		if err := deviceIOCTL(fd, setting.req, setting.ptr); err != nil {
			return nil, fmt.Errorf("configurando %s SPI: %w", setting.name, err)
		}
	}
	rx = make([]byte, len(tx))
	transfer := spiTransfer{TxBuffer: uint64(uintptr(unsafe.Pointer(&tx[0]))),
		RxBuffer: uint64(uintptr(unsafe.Pointer(&rx[0]))), Length: uint32(len(tx)),
		SpeedHz: config.SpeedHz, BitsPerWord: config.Bits}
	n, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), spiMessageOne, uintptr(unsafe.Pointer(&transfer)))
	runtime.KeepAlive(tx)
	runtime.KeepAlive(rx)
	runtime.KeepAlive(transfer)
	runtime.KeepAlive(mode)
	runtime.KeepAlive(bits)
	runtime.KeepAlive(speed)
	if errno != 0 {
		return nil, fmt.Errorf("transferência SPI: %w", errno)
	}
	if n != uintptr(len(tx)) {
		return nil, fmt.Errorf("transferência SPI incompleta: %d de %d bytes", n, len(tx))
	}
	return rx, ctx.Err()
}

func spiPath(device string) (string, error) {
	match := spiDeviceName.FindStringSubmatch("spidev" + device)
	if match == nil {
		return "", errors.New("device SPI deve ter formato barramento.chip, por exemplo 0.0")
	}
	bus, errBus := strconv.ParseUint(match[1], 10, 16)
	chip, errChip := strconv.ParseUint(match[2], 10, 16)
	if errBus != nil || errChip != nil {
		return "", errors.New("número de dispositivo SPI fora do intervalo 0..65535")
	}
	return fmt.Sprintf("/dev/spidev%d.%d", bus, chip), nil
}
