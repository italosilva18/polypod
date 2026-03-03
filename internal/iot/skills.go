package iot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// RegisterSkills registers IoT/hardware skills in the skill registry.
func RegisterSkills(reg *skill.Registry) {
	registerListUSBDevices(reg)
	registerListSerialPorts(reg)
	registerSerialSend(reg)
	registerSerialExchange(reg)
	registerFlashFirmware(reg)
}

func registerListUSBDevices(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "list_usb_devices",
		Description: "Listar dispositivos USB conectados ao sistema",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			// Try lsusb first
			if path, err := exec.LookPath("lsusb"); err == nil {
				cmd := exec.Command(path)
				out, err := cmd.CombinedOutput()
				if err == nil && len(out) > 0 {
					return strings.TrimSpace(string(out)), nil
				}
			}

			// Fallback: read from /sys/bus/usb/devices/
			var sb strings.Builder
			sb.WriteString("lsusb nao disponivel, lendo de /sys:\n\n")
			entries, err := filepath.Glob("/sys/bus/usb/devices/*/product")
			if err != nil {
				return "", fmt.Errorf("falha ao listar dispositivos USB: %w", err)
			}
			if len(entries) == 0 {
				return "Nenhum dispositivo USB encontrado.", nil
			}
			for _, entry := range entries {
				product, err := os.ReadFile(entry)
				if err != nil {
					continue
				}
				devDir := filepath.Dir(entry)
				name := strings.TrimSpace(string(product))

				vendor := readSysFile(filepath.Join(devDir, "idVendor"))
				prodID := readSysFile(filepath.Join(devDir, "idProduct"))
				sb.WriteString(fmt.Sprintf("%s:%s %s\n", vendor, prodID, name))
			}
			return strings.TrimSpace(sb.String()), nil
		},
	})
}

func registerListSerialPorts(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "list_serial_ports",
		Description: "Listar portas seriais disponiveis no sistema (/dev/ttyUSB*, /dev/ttyACM*, /dev/ttyS*)",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Execute: func(args map[string]string) (string, error) {
			patterns := []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyS*"}
			var ports []string
			for _, pat := range patterns {
				matches, _ := filepath.Glob(pat)
				ports = append(ports, matches...)
			}
			if len(ports) == 0 {
				return "Nenhuma porta serial encontrada.", nil
			}

			var sb strings.Builder
			for _, port := range ports {
				info, err := os.Stat(port)
				if err != nil {
					sb.WriteString(fmt.Sprintf("%s  [erro: %v]\n", port, err))
					continue
				}
				perm := info.Mode().Perm()

				// Check if we can open it
				accessible := "sim"
				f, err := os.OpenFile(port, os.O_RDWR, 0)
				if err != nil {
					accessible = "nao (" + err.Error() + ")"
				} else {
					f.Close()
				}

				// Try to get product name from sysfs
				product := ""
				devName := filepath.Base(port)
				productPath := fmt.Sprintf("/sys/class/tty/%s/device/../../product", devName)
				if data, err := os.ReadFile(productPath); err == nil {
					product = " [" + strings.TrimSpace(string(data)) + "]"
				}

				sb.WriteString(fmt.Sprintf("%s  perm=%o  acessivel=%s%s\n", port, perm, accessible, product))
			}
			return strings.TrimSpace(sb.String()), nil
		},
	})
}

func registerSerialSend(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "serial_send",
		Description: "Enviar dados para uma porta serial usando stty + echo",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"port":     {Type: jsonschema.String, Description: "Porta serial (ex: /dev/ttyUSB0)"},
				"data":     {Type: jsonschema.String, Description: "Dados a enviar"},
				"baudrate": {Type: jsonschema.String, Description: "Baud rate (default: 9600)"},
			},
			Required: []string{"port", "data"},
		},
		Execute: func(args map[string]string) (string, error) {
			port := args["port"]
			data := args["data"]
			baudrate := args["baudrate"]
			if baudrate == "" {
				baudrate = "9600"
			}

			if err := validatePort(port); err != nil {
				return "", err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			script := fmt.Sprintf(`stty -F %s %s raw -echo && printf '%%s\r\n' %s > %s`,
				shellQuote(port), shellQuote(baudrate), shellQuote(data), shellQuote(port))

			cmd := exec.CommandContext(ctx, "bash", "-c", script)
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return string(out) + "\n[timeout: 5s]", nil
			}
			if err != nil {
				return string(out) + "\n[exit: " + err.Error() + "]", nil
			}
			return fmt.Sprintf("Enviado %d bytes para %s @ %s baud", len(data), port, baudrate), nil
		},
	})
}

func registerSerialExchange(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "serial_exchange",
		Description: "Enviar comando para porta serial e ler a resposta (ideal para comandos AT e similares)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"port":     {Type: jsonschema.String, Description: "Porta serial (ex: /dev/ttyUSB0)"},
				"command":  {Type: jsonschema.String, Description: "Comando a enviar (ex: AT, AT+GMR)"},
				"baudrate": {Type: jsonschema.String, Description: "Baud rate (default: 9600)"},
				"timeout":  {Type: jsonschema.String, Description: "Timeout em segundos (default: 5)"},
			},
			Required: []string{"port", "command"},
		},
		Execute: func(args map[string]string) (string, error) {
			port := args["port"]
			command := args["command"]
			baudrate := args["baudrate"]
			timeout := args["timeout"]
			if baudrate == "" {
				baudrate = "9600"
			}
			if timeout == "" {
				timeout = "5"
			}

			if err := validatePort(port); err != nil {
				return "", err
			}

			execTimeout := 10 * time.Second // extra margin over the serial timeout
			ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
			defer cancel()

			// Try Python with pyserial first (more reliable for read)
			if _, err := exec.LookPath("python3"); err == nil {
				pyScript := fmt.Sprintf(`
import serial, sys
try:
    ser = serial.Serial(%s, %s, timeout=int(%s))
    ser.write((%s + '\r\n').encode())
    resp = b''
    while True:
        chunk = ser.read(1024)
        if not chunk:
            break
        resp += chunk
    ser.close()
    print(resp.decode(errors='replace').strip())
except ImportError:
    sys.exit(99)
except Exception as e:
    print(f'Erro: {e}', file=sys.stderr)
    sys.exit(1)
`, pyQuote(port), baudrate, timeout, pyQuote(command))

				cmd := exec.CommandContext(ctx, "python3", "-c", pyScript)
				out, err := cmd.CombinedOutput()
				// exit code 99 = no pyserial, fall through to bash
				if err == nil {
					return strings.TrimSpace(string(out)), nil
				}
				if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 99 {
					return strings.TrimSpace(string(out)), nil
				}
			}

			// Fallback: bash with stty + timeout + read
			script := fmt.Sprintf(`stty -F %s %s raw -echo && printf '%%s\r\n' %s > %s && timeout %s cat %s`,
				shellQuote(port), shellQuote(baudrate),
				shellQuote(command), shellQuote(port),
				shellQuote(timeout), shellQuote(port))

			cmd := exec.CommandContext(ctx, "bash", "-c", script)
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return strings.TrimSpace(string(out)) + "\n[timeout]", nil
			}
			if err != nil {
				// timeout command exits 124 when it times out — that's expected
				result := strings.TrimSpace(string(out))
				if result != "" {
					return result, nil
				}
				return result + "\n[exit: " + err.Error() + "]", nil
			}
			return strings.TrimSpace(string(out)), nil
		},
	})
}

func registerFlashFirmware(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "flash_firmware",
		Description: "Gravar firmware em um microcontrolador (suporta Arduino, ESP32/ESP8266, AVR)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"platform":      {Type: jsonschema.String, Description: "Plataforma: arduino, esp32, esp8266, avr"},
				"port":          {Type: jsonschema.String, Description: "Porta serial (ex: /dev/ttyUSB0)"},
				"firmware_path": {Type: jsonschema.String, Description: "Caminho do arquivo de firmware (.hex, .bin)"},
				"extra_args":    {Type: jsonschema.String, Description: "Argumentos extras para o comando de flash (ex: para avr, o chip: -p atmega328p)"},
			},
			Required: []string{"platform", "port", "firmware_path"},
		},
		Execute: func(args map[string]string) (string, error) {
			platform := strings.ToLower(args["platform"])
			port := args["port"]
			firmware := args["firmware_path"]
			extraArgs := args["extra_args"]

			if err := validatePort(port); err != nil {
				return "", err
			}

			if _, err := os.Stat(firmware); os.IsNotExist(err) {
				return "", fmt.Errorf("arquivo de firmware nao encontrado: %s", firmware)
			}

			var cmdStr string
			var toolName string

			switch platform {
			case "arduino":
				toolName = "arduino-cli"
				cmdStr = fmt.Sprintf("arduino-cli upload -p %s -i %s",
					shellQuote(port), shellQuote(firmware))
				if extraArgs != "" {
					cmdStr += " " + extraArgs
				}

			case "esp32", "esp8266":
				toolName = "esptool.py"
				// Also check for esptool (without .py)
				if _, err := exec.LookPath("esptool.py"); err != nil {
					toolName = "esptool"
				}
				cmdStr = fmt.Sprintf("%s --port %s write_flash 0x0 %s",
					toolName, shellQuote(port), shellQuote(firmware))
				if extraArgs != "" {
					cmdStr += " " + extraArgs
				}

			case "avr":
				toolName = "avrdude"
				if extraArgs == "" {
					return "", fmt.Errorf("para AVR, extra_args deve conter pelo menos -p <chip> (ex: -p atmega328p)")
				}
				cmdStr = fmt.Sprintf("avrdude %s -c arduino -P %s -U flash:w:%s:i",
					extraArgs, shellQuote(port), shellQuote(firmware))

			default:
				return "", fmt.Errorf("plataforma nao suportada: %s (use: arduino, esp32, esp8266, avr)", platform)
			}

			// Verify tool is installed
			if _, err := exec.LookPath(toolName); err != nil {
				return "", fmt.Errorf("%s nao encontrado no PATH. Instale antes de usar", toolName)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
			out, err := cmd.CombinedOutput()
			if ctx.Err() == context.DeadlineExceeded {
				return string(out) + "\n[timeout: flash excedeu 120s]", nil
			}
			if err != nil {
				return string(out) + "\n[exit: " + err.Error() + "]", nil
			}
			return string(out), nil
		},
	})
}

// validatePort checks that the port path looks like a valid serial device.
func validatePort(port string) error {
	if port == "" {
		return fmt.Errorf("porta serial e obrigatoria")
	}
	if !strings.HasPrefix(port, "/dev/") {
		return fmt.Errorf("porta deve comecar com /dev/ (recebido: %s)", port)
	}
	return nil
}

// shellQuote wraps a string in single quotes for safe shell interpolation.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// pyQuote returns a Python-safe quoted string.
func pyQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return "'" + s + "'"
}

// readSysFile reads a sysfs file and returns its trimmed content or "????" on error.
func readSysFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "????"
	}
	return strings.TrimSpace(string(data))
}
