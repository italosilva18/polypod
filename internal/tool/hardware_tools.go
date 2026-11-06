package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/costa/polypod/internal/hardware"
	"github.com/costa/polypod/internal/types"
)

var (
	i2cBusPattern    = regexp.MustCompile(`^[0-9]{1,5}$`)
	spiDevicePattern = regexp.MustCompile(`^([0-9]{1,5})\.([0-9]{1,5})$`)
)

// RegisterHardwareTools exposes Linux I2C and SPI operations through the
// ordinary tool/permission pipeline. No device is opened during registration.
func RegisterHardwareTools(reg *Registry) {
	registerHardwareTools(reg, hardware.NewI2C(), hardware.NewSPI())
}

func registerHardwareTools(reg *Registry, i2c hardware.I2C, spi hardware.SPI) {
	registerHardwareDef(reg, "i2c_detect", "Lista barramentos I²C disponíveis sem acessar periféricos.",
		map[string]any{}, []string{}, true,
		func(map[string]any) error { return nil },
		func(tc types.ToolContext, _ map[string]any) (any, error) {
			buses, err := i2c.Buses(tc.Context)
			if buses == nil {
				buses = []int{}
			}
			return map[string]any{"buses": buses}, err
		})
	registerHardwareDef(reg, "i2c_scan", "Sonda endereços 7-bit no barramento I²C. A sondagem pode afetar periféricos; confirme o barramento antes de chamar.",
		map[string]any{"bus": busSchema()}, []string{"bus"}, false,
		func(args map[string]any) error { _, err := parseI2CBus(args); return err },
		func(tc types.ToolContext, args map[string]any) (any, error) {
			bus, err := parseI2CBus(args)
			if err != nil {
				return nil, err
			}
			devices, err := i2c.Scan(tc.Context, bus)
			if devices == nil {
				devices = []hardware.I2CDevice{}
			}
			return map[string]any{"bus": bus, "devices": devices}, err
		})
	registerHardwareDef(reg, "i2c_read", "Lê 1 a 256 bytes de endereço I²C 7-bit; register opcional usa transação combinada com repeated start.",
		i2cProperties(true), []string{"bus", "address"}, false,
		func(args map[string]any) error { _, err := parseI2CRead(args); return err },
		func(tc types.ToolContext, args map[string]any) (any, error) {
			p, err := parseI2CRead(args)
			if err != nil {
				return nil, err
			}
			data, err := i2c.Read(tc.Context, p.bus, p.address, p.register, p.length)
			return map[string]any{"bus": p.bus, "address": p.address, "bytes": byteValues(data), "hex": fmt.Sprintf("% x", data)}, err
		})
	registerHardwareDef(reg, "i2c_write", "Escreve até 256 bytes em endereço I²C 7-bit. register opcional precede os dados na mesma transação; requer confirm=true.",
		i2cProperties(false), []string{"bus", "address", "data", "confirm"}, false,
		func(args map[string]any) error { _, err := parseI2CWrite(args); return err },
		func(tc types.ToolContext, args map[string]any) (any, error) {
			p, err := parseI2CWrite(args)
			if err != nil {
				return nil, err
			}
			err = i2c.Write(tc.Context, p.bus, p.address, p.register, p.data)
			return map[string]any{"bus": p.bus, "address": p.address, "written": len(p.data)}, err
		})
	registerHardwareDef(reg, "spi_list", "Lista dispositivos /dev/spidev* disponíveis sem transferir bytes.",
		map[string]any{}, []string{}, true,
		func(map[string]any) error { return nil },
		func(tc types.ToolContext, _ map[string]any) (any, error) {
			devices, err := spi.Devices(tc.Context)
			if devices == nil {
				devices = []string{}
			}
			return map[string]any{"devices": devices}, err
		})
	registerHardwareDef(reg, "spi_read", "Recebe 1 a 4096 bytes por SPI enviando bytes zero; a transferência pode afetar o periférico.",
		spiProperties(true), []string{"device", "length"}, false,
		func(args map[string]any) error { _, err := parseSPIRead(args); return err },
		func(tc types.ToolContext, args map[string]any) (any, error) {
			p, err := parseSPIRead(args)
			if err != nil {
				return nil, err
			}
			data, err := spi.Transfer(tc.Context, p.device, p.config, make([]byte, p.length))
			return map[string]any{"device": p.device, "bytes": byteValues(data), "hex": fmt.Sprintf("% x", data)}, err
		})
	registerHardwareDef(reg, "spi_transfer", "Transferência SPI full-duplex de 1 a 4096 bytes; requer confirm=true.",
		spiProperties(false), []string{"device", "data", "confirm"}, false,
		func(args map[string]any) error { _, err := parseSPITransfer(args); return err },
		func(tc types.ToolContext, args map[string]any) (any, error) {
			p, err := parseSPITransfer(args)
			if err != nil {
				return nil, err
			}
			data, err := spi.Transfer(tc.Context, p.device, p.config, p.data)
			return map[string]any{"device": p.device, "sent": len(p.data), "received": byteValues(data), "hex": fmt.Sprintf("% x", data)}, err
		})
}

func registerHardwareDef(reg *Registry, name, description string, properties map[string]any, required []string,
	readOnly bool, validate func(map[string]any) error,
	execute func(types.ToolContext, map[string]any) (any, error)) {
	reg.RegisterDef(types.ToolDef{
		NameFn:        func() string { return name },
		DescriptionFn: func() string { return description },
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": properties, "required": required}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return readOnly },
		IsDestructiveFn: func(map[string]any) bool { return !readOnly },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validate(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validate(args); err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			value, err := execute(tc, args)
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			data, err := json.Marshal(value)
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("serializando resultado: %v", err)}, nil
			}
			return types.ToolResult{Content: string(data)}, nil
		},
	})
}

func busSchema() map[string]any { return map[string]any{"type": "string", "pattern": "^[0-9]{1,5}$"} }
func byteArraySchema(max int) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 0, "maximum": 255}, "minItems": 1, "maxItems": max}
}
func i2cProperties(read bool) map[string]any {
	p := map[string]any{"bus": busSchema(), "address": map[string]any{"type": "integer", "minimum": 8, "maximum": 119},
		"register": map[string]any{"type": "integer", "minimum": 0, "maximum": 255}}
	if read {
		p["length"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 256, "default": 1}
	} else {
		p["data"] = byteArraySchema(256)
		p["confirm"] = map[string]any{"type": "boolean"}
	}
	return p
}
func spiProperties(read bool) map[string]any {
	p := map[string]any{"device": map[string]any{"type": "string", "pattern": "^[0-9]{1,5}\\.[0-9]{1,5}$"},
		"speed": map[string]any{"type": "integer", "minimum": 1, "maximum": 125000000, "default": 1000000},
		"mode":  map[string]any{"type": "integer", "minimum": 0, "maximum": 3, "default": 0},
		"bits":  map[string]any{"type": "integer", "minimum": 1, "maximum": 32, "default": 8}}
	if read {
		p["length"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 4096}
	} else {
		p["data"] = byteArraySchema(4096)
		p["confirm"] = map[string]any{"type": "boolean"}
	}
	return p
}

type i2cArgs struct {
	bus, address, length int
	register             *byte
	data                 []byte
}

func parseI2CBus(args map[string]any) (int, error) {
	raw, ok := args["bus"].(string)
	if !ok || !i2cBusPattern.MatchString(raw) {
		return 0, errors.New("bus I²C deve ser número decimal 0..65535")
	}
	bus, err := strconv.Atoi(raw)
	if err != nil || bus < 0 || bus > 65535 {
		return 0, errors.New("bus I²C fora do intervalo 0..65535")
	}
	return bus, nil
}

func parseI2CBase(args map[string]any) (i2cArgs, error) {
	bus, err := parseI2CBus(args)
	if err != nil {
		return i2cArgs{}, err
	}
	address, err := requiredInt(args, "address", 0x08, 0x77)
	if err != nil {
		return i2cArgs{}, err
	}
	reg, err := optionalInt(args, "register", 0, 255, -1)
	if err != nil {
		return i2cArgs{}, err
	}
	p := i2cArgs{bus: bus, address: address}
	if reg >= 0 {
		value := byte(reg)
		p.register = &value
	}
	return p, nil
}

func parseI2CRead(args map[string]any) (i2cArgs, error) {
	p, err := parseI2CBase(args)
	if err != nil {
		return p, err
	}
	p.length, err = optionalInt(args, "length", 1, 256, 1)
	return p, err
}

func parseI2CWrite(args map[string]any) (i2cArgs, error) {
	p, err := parseI2CBase(args)
	if err != nil {
		return p, err
	}
	if args["confirm"] != true {
		return p, errors.New("i2c_write requer confirm=true")
	}
	p.data, err = parseBytes(args["data"], 256)
	if err != nil {
		return p, err
	}
	if p.register != nil && len(p.data) == 256 {
		return p, errors.New("register mais data excede 256 bytes")
	}
	return p, nil
}

type spiArgs struct {
	device string
	config hardware.SPIConfig
	length int
	data   []byte
}

func parseSPIBase(args map[string]any) (spiArgs, error) {
	device, ok := args["device"].(string)
	match := spiDevicePattern.FindStringSubmatch(device)
	if !ok || match == nil {
		return spiArgs{}, errors.New("device SPI deve ter formato barramento.chip, por exemplo 0.0")
	}
	bus, errBus := strconv.ParseUint(match[1], 10, 16)
	chip, errChip := strconv.ParseUint(match[2], 10, 16)
	if errBus != nil || errChip != nil {
		return spiArgs{}, errors.New("número de dispositivo SPI fora do intervalo 0..65535")
	}
	speed, err := optionalInt(args, "speed", 1, 125000000, 1000000)
	if err != nil {
		return spiArgs{}, err
	}
	mode, err := optionalInt(args, "mode", 0, 3, 0)
	if err != nil {
		return spiArgs{}, err
	}
	bits, err := optionalInt(args, "bits", 1, 32, 8)
	if err != nil {
		return spiArgs{}, err
	}
	return spiArgs{device: fmt.Sprintf("%d.%d", bus, chip),
		config: hardware.SPIConfig{SpeedHz: uint32(speed), Mode: uint8(mode), Bits: uint8(bits)}}, nil
}

func parseSPIRead(args map[string]any) (spiArgs, error) {
	p, err := parseSPIBase(args)
	if err != nil {
		return p, err
	}
	p.length, err = requiredInt(args, "length", 1, 4096)
	return p, err
}

func parseSPITransfer(args map[string]any) (spiArgs, error) {
	p, err := parseSPIBase(args)
	if err != nil {
		return p, err
	}
	if args["confirm"] != true {
		return p, errors.New("spi_transfer requer confirm=true")
	}
	p.data, err = parseBytes(args["data"], 4096)
	return p, err
}

func parseBytes(value any, max int) ([]byte, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) < 1 || len(raw) > max {
		return nil, fmt.Errorf("data deve conter 1..%d bytes", max)
	}
	data := make([]byte, len(raw))
	for i, v := range raw {
		n, err := exactInt(v)
		if err != nil || n < 0 || n > 255 {
			return nil, fmt.Errorf("data[%d] deve ser byte 0..255", i)
		}
		data[i] = byte(n)
	}
	return data, nil
}

func byteValues(data []byte) []int {
	values := make([]int, len(data))
	for i, b := range data {
		values[i] = int(b)
	}
	return values
}

func requiredInt(args map[string]any, key string, min, max int) (int, error) {
	value, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("%s obrigatório", key)
	}
	return boundedInt(key, value, min, max)
}
func optionalInt(args map[string]any, key string, min, max, fallback int) (int, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	return boundedInt(key, value, min, max)
}
func boundedInt(key string, value any, min, max int) (int, error) {
	n, err := exactInt(value)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s deve ser inteiro entre %d e %d", key, min, max)
	}
	return n, nil
}
func exactInt(value any) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case int64:
		if n > math.MaxInt || n < math.MinInt {
			return 0, errors.New("inteiro fora do limite")
		}
		return int(n), nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n > float64(math.MaxInt) || n < float64(math.MinInt) {
			return 0, errors.New("valor não é inteiro")
		}
		return int(n), nil
	case json.Number:
		v, err := n.Int64()
		if err != nil {
			return 0, err
		}
		return exactInt(v)
	default:
		return 0, errors.New("valor não é inteiro")
	}
}
