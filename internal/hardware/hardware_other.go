//go:build !linux

package hardware

import "context"

type unsupportedHardware struct{}

func NewI2C() I2C { return unsupportedHardware{} }
func NewSPI() SPI { return unsupportedHardware{} }

func (unsupportedHardware) Buses(context.Context) ([]int, error) { return nil, ErrUnsupported }
func (unsupportedHardware) Scan(context.Context, int) ([]I2CDevice, error) {
	return nil, ErrUnsupported
}
func (unsupportedHardware) Read(context.Context, int, int, *byte, int) ([]byte, error) {
	return nil, ErrUnsupported
}
func (unsupportedHardware) Write(context.Context, int, int, *byte, []byte) error {
	return ErrUnsupported
}
func (unsupportedHardware) Devices(context.Context) ([]string, error) { return nil, ErrUnsupported }
func (unsupportedHardware) Transfer(context.Context, string, SPIConfig, []byte) ([]byte, error) {
	return nil, ErrUnsupported
}
