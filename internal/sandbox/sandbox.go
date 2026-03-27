package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Config holds sandbox settings.
type Config struct {
	Enabled     bool   `yaml:"enabled"`
	Image       string `yaml:"image"`
	MemoryLimit string `yaml:"memory_limit"`
	CPULimit    string `yaml:"cpu_limit"`
	Timeout     int    `yaml:"timeout"`
	Network     bool   `yaml:"network"`
}

// Sandbox executes commands in Docker containers.
type Sandbox struct {
	config Config
	logger *slog.Logger
}

// New creates a sandbox.
func New(cfg Config, logger *slog.Logger) *Sandbox {
	if cfg.Image == "" {
		cfg.Image = "ubuntu:22.04"
	}
	if cfg.MemoryLimit == "" {
		cfg.MemoryLimit = "256m"
	}
	if cfg.CPULimit == "" {
		cfg.CPULimit = "1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30
	}
	return &Sandbox{config: cfg, logger: logger}
}

// IsAvailable checks if Docker is running.
func (s *Sandbox) IsAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

// Execute runs a command inside a Docker container.
func (s *Sandbox) Execute(ctx context.Context, command, image string) (string, error) {
	if !s.IsAvailable() {
		return "", fmt.Errorf("docker nao disponivel")
	}
	if image == "" {
		image = s.config.Image
	}

	args := []string{
		"run", "--rm",
		"--memory=" + s.config.MemoryLimit,
		"--cpus=" + s.config.CPULimit,
	}
	if !s.config.Network {
		args = append(args, "--network=none")
	}
	args = append(args, image, "bash", "-c", command)

	timeout := time.Duration(s.config.Timeout) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "docker", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", nil
	}
	if err != nil {
		return result + "\n[exit: " + err.Error() + "]", nil
	}
	return result, nil
}

// ExecuteScript runs a script in the appropriate Docker image.
func (s *Sandbox) ExecuteScript(ctx context.Context, script, language string) (string, error) {
	if !s.IsAvailable() {
		return "", fmt.Errorf("docker nao disponivel")
	}

	var image, interpreter, ext string
	switch language {
	case "python":
		image, interpreter, ext = "python:3.12-slim", "python3", ".py"
	case "node", "javascript":
		image, interpreter, ext = "node:20-slim", "node", ".js"
	case "go":
		image, interpreter, ext = "golang:1.24", "go run", ".go"
	case "ruby":
		image, interpreter, ext = "ruby:3.3-slim", "ruby", ".rb"
	case "bash", "shell":
		image, interpreter, ext = "ubuntu:22.04", "bash", ".sh"
	default:
		image, interpreter, ext = "ubuntu:22.04", "bash", ".sh"
	}

	tmpFile, err := os.CreateTemp("", "polypod-sandbox-*"+ext)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(script)
	tmpFile.Close()

	timeout := time.Duration(s.config.Timeout) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"run", "--rm",
		"--memory=" + s.config.MemoryLimit,
		"--cpus=" + s.config.CPULimit,
		"--network=none",
		"-v", tmpFile.Name() + ":/script" + ext + ":ro",
		image,
	}
	args = append(args, strings.Fields(interpreter)...)
	args = append(args, "/script"+ext)

	cmd := exec.CommandContext(execCtx, "docker", args...)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))

	if execCtx.Err() == context.DeadlineExceeded {
		return result + "\n[timeout: sandbox]", nil
	}
	if err != nil {
		return result + "\n[exit: " + err.Error() + "]", nil
	}
	return result, nil
}

// RegisterSkills registers sandbox skills.
func RegisterSkills(reg *skill.Registry, sb *Sandbox) {
	reg.Register(&skill.Skill{
		Name:        "sandbox_run",
		Description: "Executar comando em container Docker isolado (sandbox)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"command": {Type: jsonschema.String, Description: "Comando a executar"},
				"image":   {Type: jsonschema.String, Description: "Imagem Docker (default: ubuntu:22.04)"},
			},
			Required: []string{"command"},
		},
		Execute: func(args map[string]string) (string, error) {
			return sb.Execute(context.Background(), args["command"], args["image"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "sandbox_script",
		Description: "Executar script em sandbox Docker (python, node, go, bash, ruby)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"script":   {Type: jsonschema.String, Description: "Conteudo do script"},
				"language": {Type: jsonschema.String, Description: "Linguagem: python, node, go, bash, ruby"},
			},
			Required: []string{"script", "language"},
		},
		Execute: func(args map[string]string) (string, error) {
			return sb.ExecuteScript(context.Background(), args["script"], args["language"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "sandbox_available",
		Description: "Verificar se o sandbox Docker esta disponivel",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			if sb.IsAvailable() {
				return fmt.Sprintf("Docker sandbox disponivel.\nImagem padrao: %s\nMemoria: %s\nCPU: %s\nTimeout: %ds\nRede: %v",
					sb.config.Image, sb.config.MemoryLimit, sb.config.CPULimit, sb.config.Timeout, sb.config.Network), nil
			}
			return "Docker sandbox NAO disponivel. Instale o Docker para usar.", nil
		},
	})
}
