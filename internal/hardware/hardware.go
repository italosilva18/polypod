// Package hardware provides bounded userspace I2C and SPI operations. It does
// not initialize devices or perform I/O until a tool explicitly calls it.
package hardware

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("I²C/SPI requer Linux e dispositivos em /dev")

type I2CDevice struct {
	Address int  `json:"address"`
	Busy    bool `json:"busy,omitempty"`
}

type I2C interface {
	Buses(context.Context) ([]int, error)
	Scan(context.Context, int) ([]I2CDevice, error)
	Read(context.Context, int, int, *byte, int) ([]byte, error)
	Write(context.Context, int, int, *byte, []byte) error
}

type SPIConfig struct {
	SpeedHz uint32
	Mode    uint8
	Bits    uint8
}

type SPI interface {
	Devices(context.Context) ([]string, error)
	Transfer(context.Context, string, SPIConfig, []byte) ([]byte, error)
}
