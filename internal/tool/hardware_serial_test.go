package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/serialhardware"
	"github.com/costa/polypod/internal/types"
)

type serialTransportFake struct {
	calls     int
	readBytes int
	payload   []byte
}

func (f *serialTransportFake) Exchange(_ context.Context, _ serialhardware.SerialConfig, payload []byte, readBytes int) (serialhardware.ExchangeResult, error) {
	f.calls++
	f.readBytes = readBytes
	f.payload = append([]byte(nil), payload...)
	result := serialhardware.ExchangeResult{Written: len(payload)}
	if readBytes > 0 {
		result.Received = []byte("pong")
	}
	return result, nil
}

type flashRunnerFake struct {
	calls int
	args  []string
}

func (f *flashRunnerFake) LookPath(name string) (string, error) {
	if name == "esptool" {
		return "/fake/esptool", nil
	}
	return "", os.ErrNotExist
}

func (f *flashRunnerFake) Run(_ context.Context, _ string, args []string) (string, error) {
	f.calls++
	f.args = append([]string(nil), args...)
	return "ok", nil
}

func TestSerialHardwareToolsRegistrationAndBoundedExecution(t *testing.T) {
	dir := t.TempDir()
	transport := &serialTransportFake{}
	runner := &flashRunnerFake{}
	service := &serialhardware.Service{Transport: transport, Runner: runner}
	registry := NewRegistry()
	registerSerialHardwareTools(registry, service)
	names := []string{"list_usb_devices", "list_serial_ports", "serial_send", "serial_exchange", "flash_firmware"}
	if len(registry.FilterByNames(names)) != len(names) {
		t.Fatalf("registered names = %v", registry.List())
	}
	for _, name := range []string{"serial_send", "serial_exchange"} {
		tool, _ := registry.Get(name)
		if !tool.IsDestructive(nil) || tool.IsReadOnly(nil) {
			t.Fatalf("tool %s must be a serialized write", name)
		}
	}
	flashTool, _ := registry.Get("flash_firmware")
	if !flashTool.IsReadOnly(map[string]any{"action": "status"}) || !flashTool.IsDestructive(map[string]any{"action": "flash"}) {
		t.Fatal("flash status/flash classification is incorrect")
	}
	ctx := types.ToolContext{Context: context.Background(), WorkingDir: dir}
	result, err := registry.Execute("serial_send", map[string]any{
		"port": "/dev/ttyUSB0", "data_hex": "aabb", "baud": 115200,
	}, ctx)
	if err != nil || result.IsError || transport.calls != 1 || transport.readBytes != 0 || len(transport.payload) != 2 {
		t.Fatalf("serial send = %+v, %v, transport=%+v", result, err, transport)
	}
	result, err = registry.Execute("serial_exchange", map[string]any{
		"port": "/dev/ttyUSB0", "text": "ping", "read_bytes": 4,
	}, ctx)
	if err != nil || result.IsError || transport.calls != 2 || !strings.Contains(result.Content, "pong") {
		t.Fatalf("serial exchange = %+v, %v, transport=%+v", result, err, transport)
	}
	result, err = registry.Execute("serial_send", map[string]any{
		"port": "/dev/ttyUSB0", "text": strings.Repeat("x", serialhardware.MaxSerialBytes+1),
	}, ctx)
	if err != nil || !result.IsError || transport.calls != 2 {
		t.Fatalf("oversized send = %+v, %v, calls=%d", result, err, transport.calls)
	}
	result, err = registry.Execute("flash_firmware", map[string]any{"action": "status"}, ctx)
	if err != nil || result.IsError || !strings.Contains(result.Content, "esptool") {
		t.Fatalf("flash status = %+v, %v", result, err)
	}
	firmware := filepath.Join(dir, "fw.bin")
	if err := os.WriteFile(firmware, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	flashArgs := map[string]any{"action": "flash", "backend": "esptool", "port": "/dev/ttyUSB0", "firmware_path": "fw.bin"}
	result, err = registry.Execute("flash_firmware", flashArgs, ctx)
	if err != nil || !result.IsError || runner.calls != 0 {
		t.Fatalf("flash without confirm = %+v, %v, calls=%d", result, err, runner.calls)
	}
	flashArgs["confirm"] = true
	result, err = registry.Execute("flash_firmware", flashArgs, ctx)
	if err != nil || result.IsError || runner.calls != 1 {
		t.Fatalf("confirmed flash = %+v, %v, calls=%d", result, err, runner.calls)
	}
	var parsed serialhardware.FlashResult
	if err := json.Unmarshal([]byte(result.Content), &parsed); err != nil || !parsed.Success {
		t.Fatalf("flash result = %+v, %v", parsed, err)
	}
}
