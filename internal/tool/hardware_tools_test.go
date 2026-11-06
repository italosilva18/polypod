package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/hardware"
	"github.com/costa/polypod/internal/types"
)

type fakeI2C struct {
	calls    int
	bus      int
	address  int
	register *byte
	length   int
	data     []byte
}

func (f *fakeI2C) Buses(context.Context) ([]int, error) { f.calls++; return []int{1, 2}, nil }
func (f *fakeI2C) Scan(_ context.Context, bus int) ([]hardware.I2CDevice, error) {
	f.calls++
	f.bus = bus
	return []hardware.I2CDevice{{Address: 0x38}, {Address: 0x50, Busy: true}}, nil
}
func (f *fakeI2C) Read(_ context.Context, bus, address int, register *byte, length int) ([]byte, error) {
	f.calls++
	f.bus, f.address, f.register, f.length = bus, address, register, length
	return []byte{0x12, 0x34}, nil
}
func (f *fakeI2C) Write(_ context.Context, bus, address int, register *byte, data []byte) error {
	f.calls++
	f.bus, f.address, f.register, f.data = bus, address, register, append([]byte(nil), data...)
	return nil
}

type fakeSPI struct {
	calls  int
	device string
	config hardware.SPIConfig
	tx     []byte
}

func (f *fakeSPI) Devices(context.Context) ([]string, error) { f.calls++; return []string{"0.0"}, nil }
func (f *fakeSPI) Transfer(_ context.Context, device string, config hardware.SPIConfig, tx []byte) ([]byte, error) {
	f.calls++
	f.device, f.config, f.tx = device, config, append([]byte(nil), tx...)
	return []byte{0xab, 0xcd}, nil
}

func hardwareFixture(t *testing.T) (*Registry, *fakeI2C, *fakeSPI) {
	t.Helper()
	i2c, spi := &fakeI2C{}, &fakeSPI{}
	reg := NewRegistry()
	registerHardwareTools(reg, i2c, spi)
	if reg.Count() != 7 {
		t.Fatalf("registered %d hardware tools", reg.Count())
	}
	return reg, i2c, spi
}

func TestHardwareToolsValidateBeforeIO(t *testing.T) {
	reg, i2c, spi := hardwareFixture(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"i2c_scan", map[string]any{"bus": "../1"}},
		{"i2c_read", map[string]any{"bus": "1", "address": 7}},
		{"i2c_read", map[string]any{"bus": "1", "address": 0x38, "register": 256}},
		{"i2c_read", map[string]any{"bus": "1", "address": 0x38, "length": 1.5}},
		{"i2c_write", map[string]any{"bus": "1", "address": 0x38, "data": []any{1}}},
		{"i2c_write", map[string]any{"bus": "1", "address": 0x38, "data": []any{256}, "confirm": true}},
		{"spi_read", map[string]any{"device": "../0", "length": 2}},
		{"spi_read", map[string]any{"device": "0.0", "length": 0}},
		{"spi_read", map[string]any{"device": "0.0", "length": 2, "speed": 125000001}},
		{"spi_transfer", map[string]any{"device": "0.0", "data": []any{1}}},
		{"spi_transfer", map[string]any{"device": "0.0", "data": []any{-1}, "confirm": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, _ := reg.Get(tc.name)
			if tool.ValidateInput(tc.args).Valid {
				t.Fatalf("accepted invalid %s args", tc.name)
			}
			result, err := reg.ExecuteRaw(tc.name, tc.args, types.ToolContext{Context: context.Background()})
			if err != nil || !result.IsError {
				t.Fatalf("raw execution bypassed validation: %+v %v", result, err)
			}
		})
	}
	if i2c.calls != 0 || spi.calls != 0 {
		t.Fatalf("hardware touched for invalid args: I²C=%d SPI=%d", i2c.calls, spi.calls)
	}
}

func TestHardwareToolsPassExactParametersAndReturnBytes(t *testing.T) {
	reg, i2c, spi := hardwareFixture(t)
	ctx := types.ToolContext{Context: context.Background()}
	read, err := reg.ExecuteRaw("i2c_read", map[string]any{"bus": "001", "address": float64(0x38), "register": float64(0x10), "length": float64(2)}, ctx)
	if err != nil || read.IsError || !strings.Contains(read.Content, `"bytes":[18,52]`) {
		t.Fatalf("I²C read: %+v %v", read, err)
	}
	if i2c.bus != 1 || i2c.address != 0x38 || i2c.register == nil || *i2c.register != 0x10 || i2c.length != 2 {
		t.Fatalf("wrong I²C read args: %+v", i2c)
	}
	write, err := reg.ExecuteRaw("i2c_write", map[string]any{"bus": "1", "address": 0x38, "data": []any{float64(0), float64(255)}, "confirm": true}, ctx)
	if err != nil || write.IsError || len(i2c.data) != 2 || i2c.data[1] != 255 {
		t.Fatalf("I²C write: %+v %v data=%v", write, err, i2c.data)
	}
	spiRead, err := reg.ExecuteRaw("spi_read", map[string]any{"device": "00.0", "length": 2}, ctx)
	if err != nil || spiRead.IsError || spi.device != "0.0" || len(spi.tx) != 2 || spi.tx[0] != 0 || spi.tx[1] != 0 {
		t.Fatalf("SPI read: %+v %v tx=%v", spiRead, err, spi.tx)
	}
	spiWrite, err := reg.ExecuteRaw("spi_transfer", map[string]any{"device": "0.0", "speed": 500000, "mode": 3, "bits": 8,
		"data": []any{0xa1, 0xb2}, "confirm": true}, ctx)
	if err != nil || spiWrite.IsError || spi.config.SpeedHz != 500000 || spi.config.Mode != 3 || len(spi.tx) != 2 || spi.tx[0] != 0xa1 {
		t.Fatalf("SPI transfer: %+v %v config=%+v tx=%v", spiWrite, err, spi.config, spi.tx)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(spiWrite.Content), &output); err != nil || output["sent"] != float64(2) {
		t.Fatalf("SPI output invalid: %s %v", spiWrite.Content, err)
	}
}

func TestHardwareToolPermissionClassification(t *testing.T) {
	reg, _, _ := hardwareFixture(t)
	for _, name := range []string{"i2c_detect", "spi_list"} {
		tool, _ := reg.Get(name)
		if !tool.IsReadOnly(nil) || tool.IsDestructive(nil) {
			t.Fatalf("%s should be read-only", name)
		}
	}
	for _, name := range []string{"i2c_scan", "i2c_read", "i2c_write", "spi_read", "spi_transfer"} {
		tool, _ := reg.Get(name)
		if tool.IsReadOnly(nil) || !tool.IsDestructive(nil) {
			t.Fatalf("%s can affect a peripheral", name)
		}
	}
}
