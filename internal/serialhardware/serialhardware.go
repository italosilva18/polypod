// Package serialhardware provides bounded USB discovery, serial I/O and
// firmware backend invocation without shell interpretation.
package serialhardware

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultBaud      = 115200
	DefaultTimeout   = time.Second
	MaxSerialBytes   = 4096
	MaxSerialTimeout = 30 * time.Second
	MaxFirmwareBytes = 64 << 20
)

var (
	ErrUnsupported        = errors.New("operação de hardware indisponível neste sistema")
	ErrBackendUnavailable = errors.New("backend de gravação indisponível")
	portPattern           = regexp.MustCompile(`^/dev/(?:tty(?:USB|ACM|S|AMA)\d+|rfcomm\d+|pts/\d+|serial/by-(?:id|path)/[A-Za-z0-9._-]+)$`)
)

type USBDevice struct {
	Path         string `json:"path"`
	VendorID     string `json:"vendor_id"`
	ProductID    string `json:"product_id"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	Serial       string `json:"serial,omitempty"`
	BusNumber    string `json:"bus_number,omitempty"`
	DeviceNumber string `json:"device_number,omitempty"`
}

type SerialPort struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type SerialConfig struct {
	Port    string
	Baud    int
	Timeout time.Duration
}

type ExchangeResult struct {
	Written  int    `json:"written"`
	Received []byte `json:"-"`
	TimedOut bool   `json:"timed_out"`
}

// Transport performs one bounded session so exchange does not reopen and
// reset a device between write and read.
type Transport interface {
	Exchange(ctx context.Context, cfg SerialConfig, payload []byte, readBytes int) (ExchangeResult, error)
}

type Service struct {
	USBRoot   string
	DevRoot   string
	Transport Transport
	Runner    CommandRunner
}

func NewService() *Service {
	return &Service{
		USBRoot: "/sys/bus/usb/devices", DevRoot: "/dev",
		Transport: platformTransport{}, Runner: OSCommandRunner{},
	}
}

func ValidateSerialConfig(cfg SerialConfig) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupported
	}
	if !portPattern.MatchString(cfg.Port) {
		return fmt.Errorf("porta serial inválida: %q", cfg.Port)
	}
	if _, err := baudConstant(cfg.Baud); err != nil {
		return err
	}
	if cfg.Timeout < time.Millisecond || cfg.Timeout > MaxSerialTimeout {
		return fmt.Errorf("timeout serial deve estar entre 1 e %d ms", MaxSerialTimeout.Milliseconds())
	}
	return nil
}

func (s *Service) Exchange(ctx context.Context, cfg SerialConfig, payload []byte, readBytes int) (ExchangeResult, error) {
	if err := ValidateSerialConfig(cfg); err != nil {
		return ExchangeResult{}, err
	}
	if len(payload) == 0 || len(payload) > MaxSerialBytes || readBytes < 0 || readBytes > MaxSerialBytes {
		return ExchangeResult{}, errors.New("envio exige 1–4096 bytes e leitura admite até 4096 bytes")
	}
	transport := s.Transport
	if transport == nil {
		transport = platformTransport{}
	}
	return transport.Exchange(ctx, cfg, payload, readBytes)
}

func ParsePayload(text, hexData string) ([]byte, error) {
	if (text == "") == (strings.TrimSpace(hexData) == "") {
		return nil, errors.New("informe exatamente um de text ou data_hex")
	}
	if hexData != "" {
		return decodeHexPayload(hexData)
	}
	data := []byte(text)
	if len(data) == 0 || len(data) > MaxSerialBytes {
		return nil, fmt.Errorf("text deve conter de 1 a %d bytes", MaxSerialBytes)
	}
	return data, nil
}

func decodeHexPayload(value string) ([]byte, error) {
	compact := strings.Join(strings.Fields(value), "")
	if len(compact) == 0 || len(compact) > 2*MaxSerialBytes {
		return nil, fmt.Errorf("data_hex deve conter de 1 a %d bytes", MaxSerialBytes)
	}
	data, err := hex.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("data_hex inválido: %w", err)
	}
	return data, nil
}
