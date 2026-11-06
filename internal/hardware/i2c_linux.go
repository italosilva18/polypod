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

// Request numbers and layouts are from linux/i2c-dev.h and linux/i2c.h.
const (
	i2cSlave        = 0x0703
	i2cFuncs        = 0x0705
	i2cRDWR         = 0x0707
	i2cSMBus        = 0x0720
	i2cFlagRead     = 0x0001
	i2cSMBusQuick   = 0
	i2cSMBusByte    = 1
	i2cSMBusWrite   = 0
	i2cSMBusRead    = 1
	i2cFuncQuick    = 0x00010000
	i2cFuncReadByte = 0x00020000
)

var i2cDeviceName = regexp.MustCompile(`^i2c-([0-9]+)$`)

type linuxI2C struct{}

func NewI2C() I2C { return linuxI2C{} }

func (linuxI2C) Buses(ctx context.Context) ([]int, error) {
	paths, err := filepath.Glob("/dev/i2c-*")
	if err != nil {
		return nil, err
	}
	var buses []int
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		match := i2cDeviceName.FindStringSubmatch(filepath.Base(path))
		if match == nil {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			continue
		}
		bus, err := strconv.Atoi(match[1])
		if err == nil && bus >= 0 && bus <= 65535 {
			buses = append(buses, bus)
		}
	}
	sort.Ints(buses)
	return buses, nil
}

type i2cMessage struct {
	Address uint16
	Flags   uint16
	Length  uint16
	Buffer  uintptr
}

type i2cMessageSet struct {
	Messages uintptr
	Count    uint32
}

type i2cSMBusData [34]byte

type i2cSMBusRequest struct {
	ReadWrite uint8
	Command   uint8
	_         [2]byte
	Size      uint32
	Data      uintptr
}

func (linuxI2C) Read(ctx context.Context, bus, address int, register *byte, length int) (data []byte, resultErr error) {
	if err := validateI2COperation(bus, address, length); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := openCharacterDevice(fmt.Sprintf("/dev/i2c-%d", bus))
	if err != nil {
		return nil, err
	}
	defer closeCharacterDevice(fd, &resultErr)
	if err := deviceIOCTL(fd, i2cSlave, uintptr(address)); err != nil {
		return nil, fmt.Errorf("selecionando endereço I²C 0x%02x: %w", address, err)
	}
	read := make([]byte, length)
	messages := make([]i2cMessage, 0, 2)
	var reg [1]byte
	if register != nil {
		reg[0] = *register
		messages = append(messages, i2cMessage{Address: uint16(address), Length: 1, Buffer: uintptr(unsafe.Pointer(&reg[0]))})
	}
	messages = append(messages, i2cMessage{Address: uint16(address), Flags: i2cFlagRead,
		Length: uint16(length), Buffer: uintptr(unsafe.Pointer(&read[0]))})
	request := i2cMessageSet{Messages: uintptr(unsafe.Pointer(&messages[0])), Count: uint32(len(messages))}
	err = deviceIOCTL(fd, i2cRDWR, uintptr(unsafe.Pointer(&request)))
	runtime.KeepAlive(reg)
	runtime.KeepAlive(read)
	runtime.KeepAlive(messages)
	if err != nil {
		return nil, fmt.Errorf("lendo I²C 0x%02x: %w", address, err)
	}
	return read, ctx.Err()
}

func (linuxI2C) Write(ctx context.Context, bus, address int, register *byte, data []byte) (resultErr error) {
	if err := validateI2COperation(bus, address, len(data)); err != nil {
		return err
	}
	if register != nil && len(data) == 256 {
		return errors.New("registro mais dados excede 256 bytes")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := openCharacterDevice(fmt.Sprintf("/dev/i2c-%d", bus))
	if err != nil {
		return err
	}
	defer closeCharacterDevice(fd, &resultErr)
	if err := deviceIOCTL(fd, i2cSlave, uintptr(address)); err != nil {
		return fmt.Errorf("selecionando endereço I²C 0x%02x: %w", address, err)
	}
	buffer := make([]byte, 0, len(data)+1)
	if register != nil {
		buffer = append(buffer, *register)
	}
	buffer = append(buffer, data...)
	message := i2cMessage{Address: uint16(address), Length: uint16(len(buffer)), Buffer: uintptr(unsafe.Pointer(&buffer[0]))}
	request := i2cMessageSet{Messages: uintptr(unsafe.Pointer(&message)), Count: 1}
	err = deviceIOCTL(fd, i2cRDWR, uintptr(unsafe.Pointer(&request)))
	runtime.KeepAlive(buffer)
	runtime.KeepAlive(message)
	if err != nil {
		return fmt.Errorf("escrevendo I²C 0x%02x: %w", address, err)
	}
	return ctx.Err()
}

func (linuxI2C) Scan(ctx context.Context, bus int) (devices []I2CDevice, resultErr error) {
	if bus < 0 || bus > 65535 {
		return nil, errors.New("barramento I²C fora do intervalo 0..65535")
	}
	fd, err := openCharacterDevice(fmt.Sprintf("/dev/i2c-%d", bus))
	if err != nil {
		return nil, err
	}
	defer closeCharacterDevice(fd, &resultErr)
	var funcs uintptr
	if err := deviceIOCTL(fd, i2cFuncs, uintptr(unsafe.Pointer(&funcs))); err != nil {
		return nil, fmt.Errorf("consultando capacidades I²C: %w", err)
	}
	quick, readByte := funcs&i2cFuncQuick != 0, funcs&i2cFuncReadByte != 0
	if !quick && !readByte {
		return nil, errors.New("adaptador não oferece sondagem SMBus Quick ou Read Byte")
	}
	for address := 0x08; address <= 0x77; address++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := deviceIOCTL(fd, i2cSlave, uintptr(address)); err != nil {
			if errors.Is(err, syscall.EBUSY) {
				devices = append(devices, I2CDevice{Address: address, Busy: true})
			}
			continue
		}
		if probeI2C(fd, address, quick, readByte) {
			devices = append(devices, I2CDevice{Address: address})
		}
	}
	return devices, nil
}

func probeI2C(fd, address int, quick, readByte bool) bool {
	// Some EEPROMs at these addresses can be altered by SMBus Quick Write.
	useRead := address >= 0x30 && address <= 0x37 || address >= 0x50 && address <= 0x5f
	if useRead && !readByte {
		return false
	}
	request := i2cSMBusRequest{ReadWrite: i2cSMBusWrite, Size: i2cSMBusQuick}
	var data i2cSMBusData
	if useRead || !quick {
		if !readByte {
			return false
		}
		request.ReadWrite, request.Size = i2cSMBusRead, i2cSMBusByte
		request.Data = uintptr(unsafe.Pointer(&data))
	}
	err := deviceIOCTL(fd, i2cSMBus, uintptr(unsafe.Pointer(&request)))
	runtime.KeepAlive(data)
	return err == nil
}

func validateI2COperation(bus, address, length int) error {
	if bus < 0 || bus > 65535 {
		return errors.New("barramento I²C fora do intervalo 0..65535")
	}
	if address < 0x08 || address > 0x77 {
		return errors.New("endereço I²C deve estar entre 0x08 e 0x77")
	}
	if length < 1 || length > 256 {
		return errors.New("transferência I²C deve conter 1..256 bytes")
	}
	return nil
}
