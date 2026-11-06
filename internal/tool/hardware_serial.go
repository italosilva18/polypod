package tool

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/costa/polypod/internal/serialhardware"
	"github.com/costa/polypod/internal/types"
)

// RegisterSerialHardwareTools supplies the five USB/serial/flash capabilities
// named in the default agent. Device access still flows through the central
// permission pipeline.
func RegisterSerialHardwareTools(reg *Registry) {
	registerSerialHardwareTools(reg, serialhardware.NewService())
}

func registerSerialHardwareTools(reg *Registry, service *serialhardware.Service) {
	reg.RegisterDef(types.ToolDef{
		NameFn:          func() string { return "list_usb_devices" },
		DescriptionFn:   func() string { return "Listar dispositivos USB pelo sysfs, sem acessar o dispositivo." },
		InputSchemaFn:   emptyHardwareSchema,
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, _ map[string]any) (types.ToolResult, error) {
			devices, err := service.ListUSBDevices(hardwareContext(tc))
			return hardwareJSON(map[string]any{"devices": devices, "count": len(devices)}, err)
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn:          func() string { return "list_serial_ports" },
		DescriptionFn:   func() string { return "Listar portas seriais do sistema sem abri-las." },
		InputSchemaFn:   emptyHardwareSchema,
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, _ map[string]any) (types.ToolResult, error) {
			ports, err := service.ListSerialPorts(hardwareContext(tc))
			return hardwareJSON(map[string]any{"ports": ports, "count": len(ports)}, err)
		},
	})
	for _, exchange := range []bool{false, true} {
		name := "serial_send"
		if exchange {
			name = "serial_exchange"
		}
		reg.RegisterDef(types.ToolDef{
			NameFn: func() string { return name },
			DescriptionFn: func() string {
				if exchange {
					return "Enviar bytes a uma porta serial 8N1 e receber resposta com limite de tamanho e tempo."
				}
				return "Enviar texto UTF-8 ou bytes hexadecimais a uma porta serial 8N1."
			},
			InputSchemaFn: func() map[string]any { return serialHardwareSchema(exchange) },
			ValidateFn: func(args map[string]any) types.ValidationResult {
				if _, _, _, err := parseSerialToolArgs(args, exchange); err != nil {
					return types.ValidationResult{Message: err.Error()}
				}
				return types.ValidationResult{Valid: true}
			},
			IsConcurrencyFn: func(map[string]any) bool { return false },
			IsDestructiveFn: func(map[string]any) bool { return true },
			ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
				cfg, payload, readBytes, err := parseSerialToolArgs(args, exchange)
				if err != nil {
					return hardwareJSON(nil, err)
				}
				result, err := service.Exchange(hardwareContext(tc), cfg, payload, readBytes)
				if err != nil {
					return hardwareJSON(nil, err)
				}
				response := map[string]any{"port": cfg.Port, "baud": cfg.Baud, "written": result.Written}
				if exchange {
					response["received_hex"] = hex.EncodeToString(result.Received)
					response["received_bytes"] = len(result.Received)
					response["timed_out"] = result.TimedOut
					if utf8.Valid(result.Received) {
						response["received_text"] = string(result.Received)
					}
				}
				return hardwareJSON(response, nil)
			},
		})
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "flash_firmware" },
		DescriptionFn: func() string {
			return "Verificar disponibilidade de esptool/avrdude ou gravar firmware com argumentos validados, sem shell. " +
				"Gravação exige confirm=true e arquivo regular; revise chip, offset e porta antes de executar."
		},
		InputSchemaFn: flashHardwareSchema,
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateFlashToolArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsConcurrencyFn: func(map[string]any) bool { return false },
		IsReadOnlyFn:    func(args map[string]any) bool { return args["action"] == "status" },
		IsDestructiveFn: func(args map[string]any) bool { return args["action"] == "flash" },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validateFlashToolArgs(args); err != nil {
				return hardwareJSON(nil, err)
			}
			if args["action"] == "status" {
				return hardwareJSON(map[string]any{"backends": service.FlashBackends()}, nil)
			}
			req, err := parseFlashToolArgs(tc, args)
			if err != nil {
				return hardwareJSON(nil, err)
			}
			result, err := service.Flash(hardwareContext(tc), req)
			if err != nil {
				return hardwareJSON(map[string]any{"result": result}, err)
			}
			return hardwareJSON(result, nil)
		},
	})
}

func emptyHardwareSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}}
}

func serialHardwareSchema(exchange bool) map[string]any {
	properties := map[string]any{
		"port":       map[string]any{"type": "string", "description": "Porta, por exemplo /dev/ttyUSB0"},
		"baud":       map[string]any{"type": "integer", "description": "9600 a 921600; padrão 115200"},
		"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 30000},
		"text":       map[string]any{"type": "string", "description": "Texto UTF-8 a enviar; escolha text ou data_hex"},
		"data_hex":   map[string]any{"type": "string", "description": "Bytes hexadecimais, no máximo 4096 bytes"},
	}
	if exchange {
		properties["read_bytes"] = map[string]any{"type": "integer", "minimum": 1, "maximum": serialhardware.MaxSerialBytes}
	}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"port"}}
}

func parseSerialToolArgs(args map[string]any, exchange bool) (serialhardware.SerialConfig, []byte, int, error) {
	port, ok := args["port"].(string)
	if !ok || strings.TrimSpace(port) == "" {
		return serialhardware.SerialConfig{}, nil, 0, errors.New("port é obrigatório")
	}
	baud, err := integerArg(args, "baud", serialhardware.DefaultBaud, 921600)
	if err != nil {
		return serialhardware.SerialConfig{}, nil, 0, err
	}
	timeoutMS, err := integerArg(args, "timeout_ms", 1000, 30000)
	if err != nil {
		return serialhardware.SerialConfig{}, nil, 0, err
	}
	cfg := serialhardware.SerialConfig{Port: port, Baud: baud, Timeout: time.Duration(timeoutMS) * time.Millisecond}
	if err := serialhardware.ValidateSerialConfig(cfg); err != nil {
		return cfg, nil, 0, err
	}
	text, _ := args["text"].(string)
	hexData, _ := args["data_hex"].(string)
	if raw, ok := args["text"]; ok {
		if _, ok := raw.(string); !ok {
			return cfg, nil, 0, errors.New("text deve ser texto")
		}
	}
	if raw, ok := args["data_hex"]; ok {
		if _, ok := raw.(string); !ok {
			return cfg, nil, 0, errors.New("data_hex deve ser texto")
		}
	}
	payload, err := serialhardware.ParsePayload(text, hexData)
	if err != nil {
		return cfg, nil, 0, err
	}
	readBytes := 0
	if exchange {
		readBytes, err = integerArg(args, "read_bytes", 256, serialhardware.MaxSerialBytes)
		if err != nil {
			return cfg, nil, 0, err
		}
		if readBytes < 1 {
			return cfg, nil, 0, errors.New("read_bytes deve ser de 1 a 4096")
		}
	}
	return cfg, payload, readBytes, nil
}

func flashHardwareSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"action":          map[string]any{"type": "string", "enum": []string{"status", "flash"}},
		"backend":         map[string]any{"type": "string", "enum": []string{"esptool", "avrdude"}},
		"port":            map[string]any{"type": "string"},
		"firmware_path":   map[string]any{"type": "string"},
		"baud":            map[string]any{"type": "integer"},
		"chip":            map[string]any{"type": "string", "description": "Chip Espressif; padrão auto"},
		"offset":          map[string]any{"type": "integer", "description": "Offset do esptool em bytes; múltiplo de 4096"},
		"part":            map[string]any{"type": "string", "description": "Part number do avrdude"},
		"programmer":      map[string]any{"type": "string", "description": "ID do programador avrdude"},
		"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600},
		"confirm":         map[string]any{"type": "boolean"},
	}, "required": []string{"action"}}
}

func validateFlashToolArgs(args map[string]any) error {
	action, ok := args["action"].(string)
	if !ok || (action != "status" && action != "flash") {
		return errors.New("action deve ser status ou flash")
	}
	if action == "status" {
		return nil
	}
	for _, key := range []string{"backend", "port", "firmware_path"} {
		if err := requiredStringArg(args, key); err != nil {
			return err
		}
	}
	if confirmed, ok := args["confirm"].(bool); !ok || !confirmed {
		return errors.New("flash exige confirm=true")
	}
	for _, field := range []string{"baud", "offset", "timeout_seconds"} {
		limit := 921600
		if field == "offset" {
			limit = 64 << 20
		} else if field == "timeout_seconds" {
			limit = 600
		}
		if _, err := integerArg(args, field, 0, limit); err != nil {
			return err
		}
	}
	for _, field := range []string{"chip", "part", "programmer"} {
		if raw, present := args[field]; present {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("%s deve ser texto", field)
			}
		}
	}
	return nil
}

func parseFlashToolArgs(tc types.ToolContext, args map[string]any) (serialhardware.FlashRequest, error) {
	baud, err := integerArg(args, "baud", serialhardware.DefaultBaud, 921600)
	if err != nil {
		return serialhardware.FlashRequest{}, err
	}
	offset, err := integerArg(args, "offset", 0, 64<<20)
	if err != nil {
		return serialhardware.FlashRequest{}, err
	}
	timeoutSeconds, err := integerArg(args, "timeout_seconds", 300, 600)
	if err != nil {
		return serialhardware.FlashRequest{}, err
	}
	if timeoutSeconds == 0 {
		return serialhardware.FlashRequest{}, errors.New("timeout_seconds deve ser positivo")
	}
	firmwarePath := args["firmware_path"].(string)
	if !filepath.IsAbs(firmwarePath) {
		firmwarePath = filepath.Join(tc.WorkingDir, firmwarePath)
	}
	chip, _ := args["chip"].(string)
	part, _ := args["part"].(string)
	programmer, _ := args["programmer"].(string)
	return serialhardware.FlashRequest{
		Backend: args["backend"].(string), Port: args["port"].(string), FirmwarePath: firmwarePath,
		Baud: baud, Chip: chip, Offset: uint64(offset), Part: part, Programmer: programmer,
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	}, nil
}

func hardwareContext(tc types.ToolContext) context.Context {
	if tc.Context != nil {
		return tc.Context
	}
	return context.Background()
}

func hardwareJSON(value any, err error) (types.ToolResult, error) {
	if err != nil {
		if value == nil {
			return types.ToolResult{Content: "Hardware: " + err.Error(), IsError: true}, nil
		}
		value = map[string]any{"error": err.Error(), "details": value}
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return types.ToolResult{Content: "Hardware: " + marshalErr.Error(), IsError: true}, nil
	}
	return types.ToolResult{Content: string(data), IsError: err != nil}, nil
}
