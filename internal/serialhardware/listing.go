package serialhardware

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var usbIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)

func (s *Service) ListUSBDevices(ctx context.Context) ([]USBDevice, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	root := s.USBRoot
	if root == "" {
		root = "/sys/bus/usb/devices"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrUnsupported
		}
		return nil, err
	}
	devices := make([]USBDevice, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(root, entry.Name())
		vendor := readSysfsField(filepath.Join(path, "idVendor"))
		product := readSysfsField(filepath.Join(path, "idProduct"))
		if !usbIDPattern.MatchString(vendor) || !usbIDPattern.MatchString(product) {
			continue
		}
		devices = append(devices, USBDevice{
			Path: entry.Name(), VendorID: strings.ToLower(vendor), ProductID: strings.ToLower(product),
			Manufacturer: readSysfsField(filepath.Join(path, "manufacturer")),
			Product:      readSysfsField(filepath.Join(path, "product")),
			Serial:       readSysfsField(filepath.Join(path, "serial")),
			BusNumber:    readSysfsField(filepath.Join(path, "busnum")),
			DeviceNumber: readSysfsField(filepath.Join(path, "devnum")),
		})
		if len(devices) >= 256 {
			break
		}
	}
	return devices, nil
}

func readSysfsField(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	if len(value) > 128 {
		value = value[:128]
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, value)
}

func (s *Service) ListSerialPorts(ctx context.Context) ([]SerialPort, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	root := s.DevRoot
	if root == "" {
		root = "/dev"
	}
	patterns := []string{"ttyUSB*", "ttyACM*", "ttyS*", "ttyAMA*", "rfcomm*", "serial/by-id/*", "serial/by-path/*"}
	seen := make(map[string]bool)
	var ports []SerialPort
	for _, pattern := range patterns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			if seen[path] {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode()&os.ModeCharDevice == 0 {
				continue
			}
			seen[path] = true
			ports = append(ports, SerialPort{Name: filepath.Base(path), Path: path})
			if len(ports) >= 256 {
				break
			}
		}
		if len(ports) >= 256 {
			break
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Path < ports[j].Path })
	return ports, nil
}
