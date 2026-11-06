//go:build linux

package hardware

import (
	"context"
	"strconv"
	"testing"
	"unsafe"
)

func TestLinuxI2CAndSPIABISizes(t *testing.T) {
	if unsafe.Sizeof(spiTransfer{}) != 32 {
		t.Fatalf("spi_ioc_transfer size=%d", unsafe.Sizeof(spiTransfer{}))
	}
	if unsafe.Sizeof(i2cMessage{}) != uintptr(8+strconv.IntSize/8) {
		t.Fatalf("i2c_msg size=%d", unsafe.Sizeof(i2cMessage{}))
	}
	if unsafe.Offsetof(i2cMessage{}.Buffer) != 8 {
		t.Fatalf("i2c_msg buffer offset=%d", unsafe.Offsetof(i2cMessage{}.Buffer))
	}
	if unsafe.Sizeof(i2cSMBusRequest{}) != uintptr(8+strconv.IntSize/8) {
		t.Fatalf("i2c_smbus_ioctl_data size=%d", unsafe.Sizeof(i2cSMBusRequest{}))
	}
}

func TestHardwarePathValidationDoesNotOpenDevices(t *testing.T) {
	for _, raw := range []string{"../0", "0/1", "65536.0", "0.65536", "0.0/../../tmp"} {
		if _, err := spiPath(raw); err == nil {
			t.Errorf("accepted SPI path %q", raw)
		}
	}
	ctx := context.Background()
	if _, err := (linuxI2C{}).Read(ctx, 1, 0x02, nil, 1); err == nil {
		t.Fatal("reserved I²C address accepted")
	}
	if err := (linuxI2C{}).Write(ctx, 1, 0x38, nil, make([]byte, 257)); err == nil {
		t.Fatal("oversized I²C write accepted")
	}
	if _, err := (linuxSPI{}).Transfer(ctx, "0.0", SPIConfig{SpeedHz: 125000001, Bits: 8}, []byte{1}); err == nil {
		t.Fatal("oversized SPI clock accepted")
	}
}
