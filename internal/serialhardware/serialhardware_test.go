package serialhardware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeTransport struct {
	cfg       SerialConfig
	payload   []byte
	readBytes int
	result    ExchangeResult
}

func (f *fakeTransport) Exchange(_ context.Context, cfg SerialConfig, payload []byte, readBytes int) (ExchangeResult, error) {
	f.cfg, f.payload, f.readBytes = cfg, append([]byte(nil), payload...), readBytes
	return f.result, nil
}

type fakeRunner struct {
	available  map[string]bool
	command    string
	args       []string
	output     string
	err        error
	onLookPath func() error
	inputPath  string
	inputBytes []byte
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.onLookPath != nil {
		if err := f.onLookPath(); err != nil {
			return "", err
		}
	}
	if !f.available[name] {
		return "", os.ErrNotExist
	}
	return "/fake/bin/" + name, nil
}

func (f *fakeRunner) Run(_ context.Context, command string, args []string) (string, error) {
	f.command, f.args = command, append([]string(nil), args...)
	if strings.HasSuffix(command, "esptool") {
		f.inputPath = args[len(args)-1]
	} else if strings.HasSuffix(command, "avrdude") {
		operand := strings.TrimPrefix(args[len(args)-1], "flash:w:")
		f.inputPath = operand[:strings.LastIndex(operand, ":")]
	}
	if f.inputPath != "" {
		data, err := os.ReadFile(f.inputPath)
		if err != nil {
			return "", err
		}
		f.inputBytes = data
	}
	return f.output, f.err
}

func TestUSBAndSerialListingsFromFixtures(t *testing.T) {
	dir := t.TempDir()
	usbRoot := filepath.Join(dir, "usb")
	deviceDir := filepath.Join(usbRoot, "1-2")
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"idVendor": "10C4\n", "idProduct": "EA60\n", "manufacturer": "Test Vendor\n", "product": "Virtual Serial\n",
	} {
		if err := os.WriteFile(filepath.Join(deviceDir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(usbRoot, "unrelated"), 0o755); err != nil {
		t.Fatal(err)
	}
	devRoot := filepath.Join(dir, "dev")
	if err := os.MkdirAll(devRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(devRoot, "ttyUSB0")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "ttyUSB1"), []byte("not a device"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := &Service{USBRoot: usbRoot, DevRoot: devRoot}
	usb, err := service.ListUSBDevices(context.Background())
	if err != nil || len(usb) != 1 || usb[0].VendorID != "10c4" || usb[0].ProductID != "ea60" {
		t.Fatalf("USB listing = %+v, %v", usb, err)
	}
	ports, err := service.ListSerialPorts(context.Background())
	if err != nil || len(ports) != 1 || ports[0].Name != "ttyUSB0" {
		t.Fatalf("serial listing = %+v, %v", ports, err)
	}
}

func TestSerialValidationAndFakeTransport(t *testing.T) {
	fake := &fakeTransport{result: ExchangeResult{Written: 2, Received: []byte{0x01, 0x02}}}
	service := &Service{Transport: fake}
	payload, err := ParsePayload("", "01 02")
	if err != nil || !reflect.DeepEqual(payload, []byte{1, 2}) {
		t.Fatalf("hex payload = %v, %v", payload, err)
	}
	if _, err := ParsePayload("a", "61"); err == nil {
		t.Fatal("accepted text and hex together")
	}
	cfg := SerialConfig{Port: "/dev/ttyUSB0", Baud: 115200, Timeout: time.Second}
	result, err := service.Exchange(context.Background(), cfg, payload, 2)
	if err != nil || result.Written != 2 || fake.readBytes != 2 || !reflect.DeepEqual(fake.payload, payload) {
		t.Fatalf("fake exchange = %+v, %v; got config %+v", result, err, fake.cfg)
	}
	if _, err := service.Exchange(context.Background(), cfg, make([]byte, MaxSerialBytes+1), 0); err == nil {
		t.Fatal("accepted oversized payload")
	}
	if _, err := service.Exchange(context.Background(), SerialConfig{Port: "/tmp/file", Baud: 115200, Timeout: time.Second}, payload, 0); err == nil {
		t.Fatal("accepted non-device path")
	}
}

func TestFlashBackendsValidateAndNeverShellExpand(t *testing.T) {
	dir := t.TempDir()
	firmware := filepath.Join(dir, "image;$(touch-nope).bin")
	if err := os.WriteFile(firmware, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{available: map[string]bool{"esptool": true, "avrdude": true}, output: "done"}
	service := &Service{Runner: runner}
	request := FlashRequest{Backend: "esptool", Port: "/dev/ttyUSB0", FirmwarePath: firmware,
		Baud: 115200, Chip: "esp32", Offset: 0x1000, Timeout: time.Minute}
	result, err := service.Flash(context.Background(), request)
	if err != nil || !result.Success || result.FirmwareSHA256 == "" {
		t.Fatalf("esptool flash = %+v, %v", result, err)
	}
	want := []string{"--chip", "esp32", "--port", "/dev/ttyUSB0", "--baud", "115200", "write-flash", "0x1000", runner.inputPath}
	if runner.command != "/fake/bin/esptool" || !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("esptool argv = %q %q", runner.command, runner.args)
	}
	if runner.inputPath == firmware || !bytes.Equal(runner.inputBytes, []byte{1, 2, 3}) {
		t.Fatalf("backend did not receive a private firmware snapshot: %q %v", runner.inputPath, runner.inputBytes)
	}
	if _, err := os.Stat(runner.inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("firmware snapshot was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "touch-nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell interpolation occurred: %v", err)
	}
	request.Chip = "esp32;rm"
	if _, err := service.Flash(context.Background(), request); err == nil {
		t.Fatal("accepted injected chip")
	}
	request.Chip = "esp32"
	request.Backend = "missing"
	if _, err := service.Flash(context.Background(), request); err == nil {
		t.Fatal("accepted unknown backend")
	}
	runner.available["esptool"] = false
	request.Backend = "esptool"
	if _, err := service.Flash(context.Background(), request); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("missing backend error = %v", err)
	}

	hexFile := filepath.Join(dir, "app.hex")
	if err := os.WriteFile(hexFile, []byte(":00000001FF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request = FlashRequest{Backend: "avrdude", Port: "/dev/ttyACM0", FirmwarePath: hexFile,
		Part: "m328p", Programmer: "arduino", Baud: 57600, Timeout: time.Minute}
	result, err = service.Flash(context.Background(), request)
	if err != nil || !result.Success || !strings.Contains(strings.Join(runner.args, " "), "flash:w:"+runner.inputPath+":i") {
		t.Fatalf("avrdude flash = %+v, %v, argv=%v", result, err, runner.args)
	}
}

func TestFlashSnapshotSurvivesSourceReplacementDuringBackendLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.bin")
	original := []byte{0x01, 0x02, 0x03, 0x04}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{available: map[string]bool{"esptool": true}}
	runner.onLookPath = func() error { return os.WriteFile(path, []byte{0xaa, 0xbb}, 0o600) }
	service := &Service{Runner: runner}
	result, err := service.Flash(context.Background(), FlashRequest{
		Backend: "esptool", Port: "/dev/ttyUSB0", FirmwarePath: path, Baud: 115200, Timeout: time.Minute,
	})
	if err != nil || !result.Success {
		t.Fatalf("snapshot flash = %+v, %v", result, err)
	}
	expectedHash := sha256.Sum256(original)
	if result.FirmwarePath != path || result.FirmwareSHA256 != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("reported source/digest = %+v", result)
	}
	if runner.inputPath == path || !bytes.Equal(runner.inputBytes, original) {
		t.Fatalf("backend received modified source instead of snapshot: path=%q bytes=%x", runner.inputPath, runner.inputBytes)
	}
	if _, err := os.Stat(runner.inputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot remains after backend: %v", err)
	}
}
