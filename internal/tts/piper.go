// Package tts synthesizes short speech locally for outbound voice messages.
package tts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxTextRunes   = 1200
	MaxVoiceBytes  = 16 << 20
	defaultTimeout = 2 * time.Minute
	maxTimeout     = 10 * time.Minute
)

type PiperConfig struct {
	PythonBinary string
	ModelPath    string
	FFmpegBinary string
	Timeout      time.Duration
}

type Piper struct {
	python  string
	model   string
	ffmpeg  string
	timeout time.Duration
	gate    chan struct{}
}

func NewPiper(cfg PiperConfig) (*Piper, error) {
	if cfg.PythonBinary == "" {
		cfg.PythonBinary = "python3"
	}
	if cfg.FFmpegBinary == "" {
		cfg.FFmpegBinary = "ffmpeg"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Timeout < 0 || cfg.Timeout > maxTimeout {
		return nil, errors.New("timeout de voz local inválido")
	}
	python, err := exec.LookPath(expandHome(strings.TrimSpace(cfg.PythonBinary)))
	if err != nil {
		return nil, fmt.Errorf("Python de voz local: %w", err)
	}
	ffmpeg, err := exec.LookPath(expandHome(strings.TrimSpace(cfg.FFmpegBinary)))
	if err != nil {
		return nil, fmt.Errorf("ffmpeg de voz local: %w", err)
	}
	modelPath := expandHome(strings.TrimSpace(cfg.ModelPath))
	if modelPath == "" {
		return nil, errors.New("modelo de voz local não configurado")
	}
	model, err := filepath.Abs(modelPath)
	if err != nil {
		return nil, errors.New("caminho do modelo de voz local inválido")
	}
	for _, path := range []string{model, model + ".json"} {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return nil, errors.New("modelo de voz local ou configuração ausente")
		}
	}
	return &Piper{python: python, model: model, ffmpeg: ffmpeg, timeout: cfg.Timeout, gate: make(chan struct{}, 1)}, nil
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

// Synthesize returns an OGG/Opus voice note. Text and intermediate WAV files
// stay in a private temporary directory and are removed after conversion.
func (p *Piper) Synthesize(ctx context.Context, text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxTextRunes {
		return nil, fmt.Errorf("texto de voz deve ter entre 1 e %d caracteres UTF-8", MaxTextRunes)
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-runCtx.Done():
		return nil, runCtx.Err()
	}
	dir, err := os.MkdirTemp("", "polypod-tts-")
	if err != nil {
		return nil, fmt.Errorf("preparar voz: %w", err)
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "text.txt")
	wav := filepath.Join(dir, "voice.wav")
	ogg := filepath.Join(dir, "voice.ogg")
	if err := os.WriteFile(input, []byte(text), 0600); err != nil {
		return nil, fmt.Errorf("preparar texto de voz: %w", err)
	}
	worker := exec.CommandContext(runCtx, p.python, "-m", "piper", "-m", p.model, "-i", input, "-f", wav)
	if err := runVoiceCommand(runCtx, worker); err != nil {
		return nil, fmt.Errorf("síntese Piper: %w", err)
	}
	encoder := exec.CommandContext(runCtx, p.ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", wav, "-vn", "-c:a", "libopus", "-application", "voip",
		"-b:a", "32k", "-vbr", "on", "-ac", "1", "-ar", "48000", "-f", "ogg", ogg)
	if err := runVoiceCommand(runCtx, encoder); err != nil {
		return nil, fmt.Errorf("codificar voz OGG/Opus: %w", err)
	}
	info, err := os.Stat(ogg)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 64 || info.Size() > MaxVoiceBytes {
		return nil, errors.New("áudio OGG/Opus gerado inválido ou grande demais")
	}
	data, err := os.ReadFile(ogg)
	if err != nil {
		return nil, fmt.Errorf("ler voz gerada: %w", err)
	}
	if !bytes.HasPrefix(data, []byte("OggS")) {
		return nil, errors.New("voz gerada não é OGG")
	}
	return data, nil
}

func runVoiceCommand(ctx context.Context, cmd *exec.Cmd) error {
	// Never log the text, model output or private paths from the worker.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
