package cli

import (
	"bytes"
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
func newVoiceCapture(cfg config.AudioTranscriptionConfig) func(context.Context, context.Context, time.Duration) (string, error) {
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
	return func(recordCtx, transcribeCtx context.Context, duration time.Duration) (string, error) {
		if setupErr != nil {
			return "", setupErr
		}
		data, err := recordMicrophone(recordCtx, duration)
		if err != nil {
			return "", err
		}
		if err := transcribeCtx.Err(); err != nil {
			return "", err
		}
		return transcribe(transcribeCtx, data, "audio/wav")
	}
}

func recordMicrophone(ctx context.Context, duration time.Duration) ([]byte, error) {
	seconds := int(duration / time.Second)
	if duration < 0 || (duration > 0 && (seconds < 1 || seconds > 120)) {
		return nil, fmt.Errorf("duração de gravação inválida")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("gravação interrompida antes de começar: %w", err)
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
	recordCtx := ctx
	if duration > 0 {
		var cancel context.CancelFunc
		recordCtx, cancel = context.WithTimeout(ctx, duration+15*time.Second)
		defer cancel()
	}
	args := []string{"-q", "-D", device, "-f", "S16_LE", "-r", "16000", "-c", "1"}
	if duration > 0 {
		args = append(args, "-d", fmt.Sprint(seconds))
	}
	args = append(args, "-t", "wav", path)
	cmd := exec.Command(recorder, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("iniciar microfone (%s): %w", device, err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	var runErr error
	stopped := false
	select {
	case runErr = <-finished:
	case <-recordCtx.Done():
		stopped = true
		// Give arecord a chance to finalize the WAV header before reading it.
		_ = cmd.Process.Signal(os.Interrupt)
		timer := time.NewTimer(3 * time.Second)
		select {
		case runErr = <-finished:
			timer.Stop()
		case <-timer.C:
			_ = cmd.Process.Kill()
			runErr = <-finished
		}
	}
	if recordCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("microfone (%s) não encerrou após o prazo da gravação", device)
	}
	if runErr != nil && !stopped {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 300 {
			message = message[:300]
		}
		if message != "" {
			return nil, fmt.Errorf("microfone (%s): %s: %w", device, message, runErr)
		}
		return nil, fmt.Errorf("microfone (%s): %w", device, runErr)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("microfone (%s) não gerou arquivo de áudio: %w", device, err)
	}
	if info.Size() <= 44 {
		return nil, fmt.Errorf("gravação vazia; fale antes de parar")
	}
	if info.Size() > audio.MaxAudioBytes {
		return nil, fmt.Errorf("gravação excede %d MiB; pare e envie partes menores", audio.MaxAudioBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler gravação: %w", err)
	}
	return data, nil
}
