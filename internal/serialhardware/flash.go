package serialhardware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+-]{0,39}$`)

type FlashRequest struct {
	Backend      string
	Port         string
	FirmwarePath string
	Baud         int
	Chip         string // esptool, optional auto-detect
	Offset       uint64 // esptool flash offset
	Part         string // avrdude target MCU
	Programmer   string // avrdude programmer ID
	Timeout      time.Duration
}

type FlashResult struct {
	Backend        string `json:"backend"`
	Port           string `json:"port"`
	FirmwarePath   string `json:"firmware_path"`
	FirmwareSHA256 string `json:"firmware_sha256"`
	Output         string `json:"output"`
	Success        bool   `json:"success"`
}

type BackendStatus struct {
	Backend   string `json:"backend"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
}

type CommandRunner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, executable string, args []string) (string, error)
}

type OSCommandRunner struct{}

func (OSCommandRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (OSCommandRunner) Run(ctx context.Context, executable string, args []string) (string, error) {
	command := exec.CommandContext(ctx, executable, args...)
	output := &boundedOutput{max: 32 << 10}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.String(), err
}

type boundedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	space := b.max - b.buf.Len()
	if space > 0 {
		if _, err := b.buf.Write(p[:min(space, len(p))]); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (b *boundedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (s *Service) FlashBackends() []BackendStatus {
	runner := s.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	statuses := make([]BackendStatus, 0, 2)
	for _, name := range []string{"esptool", "avrdude"} {
		path, err := runner.LookPath(name)
		statuses = append(statuses, BackendStatus{Backend: name, Available: err == nil, Path: path})
	}
	return statuses
}

// Flash validates every operand before invoking a fixed executable with a
// fixed argv layout. No shell or caller-supplied extra arguments are used.
func (s *Service) Flash(ctx context.Context, request FlashRequest) (result FlashResult, returnErr error) {
	request, err := validateFlashRequest(request)
	if err != nil {
		return FlashResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FlashResult{}, err
	}
	snapshotDir, snapshotPath, hash, err := snapshotFirmware(request.FirmwarePath)
	if err != nil {
		return FlashResult{}, fmt.Errorf("preparando cópia do firmware: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(snapshotDir); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("removendo cópia do firmware: %w", err))
		}
	}()
	result = FlashResult{Backend: request.Backend, Port: request.Port,
		FirmwarePath: request.FirmwarePath, FirmwareSHA256: hash}
	request.FirmwarePath = snapshotPath
	runner := s.Runner
	if runner == nil {
		runner = OSCommandRunner{}
	}
	executable, err := runner.LookPath(request.Backend)
	if err != nil {
		return result, fmt.Errorf("%w: %s", ErrBackendUnavailable, request.Backend)
	}
	args := flashArgs(request)
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	result.Output, err = runner.Run(runCtx, executable, args)
	if err != nil {
		return result, fmt.Errorf("gravação com %s falhou: %w", request.Backend, err)
	}
	result.Success = true
	return result, nil
}

func validateFlashRequest(req FlashRequest) (FlashRequest, error) {
	if req.Backend != "esptool" && req.Backend != "avrdude" {
		return req, errors.New("backend deve ser esptool ou avrdude")
	}
	if !portPattern.MatchString(req.Port) {
		return req, errors.New("porta de gravação inválida")
	}
	if req.Baud == 0 {
		req.Baud = DefaultBaud
	}
	if _, err := baudConstant(req.Baud); err != nil {
		return req, err
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	if req.Timeout < time.Second || req.Timeout > 10*time.Minute {
		return req, errors.New("timeout de gravação deve estar entre 1 e 600 segundos")
	}
	if strings.ContainsAny(req.FirmwarePath, "\x00\n\r") || strings.TrimSpace(req.FirmwarePath) == "" {
		return req, errors.New("caminho do firmware inválido")
	}
	absPath, err := filepath.Abs(req.FirmwarePath)
	if err != nil {
		return req, err
	}
	req.FirmwarePath = absPath
	info, err := os.Lstat(absPath)
	if err != nil {
		return req, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > MaxFirmwareBytes {
		return req, errors.New("firmware deve ser arquivo regular de 1 a 64 MiB")
	}
	if req.Backend == "esptool" {
		if req.Chip == "" {
			req.Chip = "auto"
		}
		switch req.Chip {
		case "auto", "esp8266", "esp32", "esp32s2", "esp32s3", "esp32c3", "esp32c6", "esp32h2":
		default:
			return req, errors.New("chip esptool não suportado")
		}
		if req.Offset > 64<<20 || req.Offset%4096 != 0 {
			return req, errors.New("offset deve ser múltiplo de 4096 e no máximo 64 MiB")
		}
		if strings.ToLower(filepath.Ext(absPath)) != ".bin" {
			return req, errors.New("esptool exige firmware .bin")
		}
	} else {
		if !identifierPattern.MatchString(req.Part) || !identifierPattern.MatchString(req.Programmer) {
			return req, errors.New("avrdude exige part e programmer válidos")
		}
		if strings.Contains(absPath, ":") {
			return req, errors.New("caminho do firmware não pode conter ':' no avrdude")
		}
		ext := strings.ToLower(filepath.Ext(absPath))
		if ext != ".hex" && ext != ".bin" {
			return req, errors.New("avrdude exige firmware .hex ou .bin")
		}
	}
	return req, nil
}

// snapshotFirmware gives the backend a private read-only copy. The digest is
// computed from that copy, so replacing the source after validation cannot
// change the bytes the backend reads without changing the reported digest.
func snapshotFirmware(sourcePath string) (dir, snapshotPath, digest string, returnErr error) {
	dir, err := os.MkdirTemp("", "polypod-firmware-*")
	if err != nil {
		return "", "", "", err
	}
	createdDir := dir
	defer func() {
		if returnErr != nil {
			if err := os.RemoveAll(createdDir); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", "", "", err
	}
	defer func() {
		if err := source.Close(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	info, err := source.Stat()
	if err != nil {
		return "", "", "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > MaxFirmwareBytes {
		return "", "", "", errors.New("firmware deve ser arquivo regular de 1 a 64 MiB")
	}
	snapshot, err := os.CreateTemp(dir, "image-*"+filepath.Ext(sourcePath))
	if err != nil {
		return "", "", "", err
	}
	snapshotPath = snapshot.Name()
	hasher := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(snapshot, hasher), io.LimitReader(source, MaxFirmwareBytes+1))
	syncErr := snapshot.Sync()
	closeErr := snapshot.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", "", "", err
	}
	if count == 0 || count > MaxFirmwareBytes {
		return "", "", "", errors.New("firmware deve ser arquivo regular de 1 a 64 MiB")
	}
	if err := os.Chmod(snapshotPath, 0o400); err != nil {
		return "", "", "", err
	}
	return dir, snapshotPath, hex.EncodeToString(hasher.Sum(nil)), nil
}

func flashArgs(req FlashRequest) []string {
	if req.Backend == "esptool" {
		args := make([]string, 0, 9)
		if req.Chip != "auto" {
			args = append(args, "--chip", req.Chip)
		}
		return append(args, "--port", req.Port, "--baud", strconv.Itoa(req.Baud),
			"write-flash", fmt.Sprintf("0x%x", req.Offset), req.FirmwarePath)
	}
	format := "i"
	if strings.EqualFold(filepath.Ext(req.FirmwarePath), ".bin") {
		format = "r"
	}
	return []string{"-p", req.Part, "-c", req.Programmer, "-P", req.Port,
		"-b", strconv.Itoa(req.Baud), "-U", "flash:w:" + req.FirmwarePath + ":" + format}
}
