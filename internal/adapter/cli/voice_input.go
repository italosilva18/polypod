package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/costa/polypod/internal/audio"
	"github.com/costa/polypod/internal/config"
)

// newVoiceCapture reuses the configured speech-to-text backend for CLI
// dictation. Recording and transcription run in a Bubbletea command, leaving
// the chat and composer responsive while the microphone is active.
func newVoiceCapture(cfg config.AudioTranscriptionConfig) func(context.Context, time.Duration) (string, error) {
	var transcribe func(context.Context, []byte, string) (string, error)
	var setupErr error
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "", "http":
		var backend *audio.HTTPTranscriber
		backend, setupErr = audio.NewHTTPTranscriber(cfg.Endpoint, cfg.Model, cfg.APIKey, nil)
		if setupErr == nil {
			transcribe = backend.Transcribe
		}
	case "local":
		var backend *audio.LocalTranscriber
		backend, setupErr = audio.NewLocalTranscriber(audio.LocalConfig{
			PythonBinary: cfg.PythonBinary,
			ScriptPath:   cfg.ScriptPath,
			ModelPath:    cfg.ModelPath,
			FFmpegBinary: cfg.FFmpegBinary,
			Language:     cfg.Language,
			Timeout:      time.Duration(cfg.TimeoutSeconds) * time.Second,
		})
		if setupErr == nil {
			transcribe = backend.Transcribe
		}
	default:
		setupErr = fmt.Errorf("backend de transcrição desconhecido: %s", cfg.Backend)
	}
	return func(ctx context.Context, duration time.Duration) (string, error) {
		if setupErr != nil {
			return "", setupErr
		}
		data, err := recordMicrophone(ctx, duration)
		if err != nil {
			return "", err
		}
		return transcribe(ctx, data, "audio/wav")
	}
}

func recordMicrophone(ctx context.Context, duration time.Duration) ([]byte, error) {
	seconds := int(duration / time.Second)
	if seconds < 1 || seconds > 120 {
		return nil, fmt.Errorf("duração de gravação inválida")
	}
	recorder, err := exec.LookPath("arecord")
	if err != nil {
		return nil, fmt.Errorf("arecord não está instalado: %w", err)
	}
	dir, err := os.MkdirTemp("", "polypod-voice-")
	if err != nil {
		return nil, fmt.Errorf("criar área temporária de áudio: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "recording.wav")
	device := strings.TrimSpace(os.Getenv("POLYPOD_VOICE_INPUT_DEVICE"))
	if device == "" {
		device = "default"
	}
	recordCtx, cancel := context.WithTimeout(ctx, duration+15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(recordCtx, recorder, "-q", "-D", device,
		"-f", "S16_LE", "-r", "16000", "-c", "1", "-d", fmt.Sprint(seconds),
		"-t", "wav", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 300 {
			message = message[:300]
		}
		if message != "" {
			return nil, fmt.Errorf("microfone (%s): %s: %w", device, message, err)
		}
		return nil, fmt.Errorf("microfone (%s): %w", device, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ler gravação: %w", err)
	}
	if info.Size() == 0 || info.Size() > audio.MaxAudioBytes {
		return nil, fmt.Errorf("gravação vazia ou muito grande")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler gravação: %w", err)
	}
	return data, nil
}
