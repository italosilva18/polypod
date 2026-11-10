package cli

import (
	"bytes"
	"context"
	"encoding/binary"
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
func newVoiceCapture(cfg config.AudioTranscriptionConfig) func(context.Context, context.Context, time.Duration, func(string)) (string, error) {
	var transcribe func(context.Context, []byte, string) (string, error)
	var setupErr error
	partialSupported := false
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
			partialSupported = true
		}
	default:
		setupErr = fmt.Errorf("backend de transcrição desconhecido: %s", cfg.Backend)
	}
	return func(recordCtx, transcribeCtx context.Context, duration time.Duration, onPartial func(string)) (string, error) {
		if setupErr != nil {
			return "", setupErr
		}
		var snapshot func([]byte)
		stopPartials := func() {}
		if partialSupported && onPartial != nil {
			partialCtx, cancel := context.WithCancel(transcribeCtx)
			snapshots := make(chan []byte, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for wav := range snapshots {
					if partialCtx.Err() != nil {
						return
					}
					text, err := transcribe(partialCtx, wav, "audio/wav")
					if err == nil && partialCtx.Err() == nil && strings.TrimSpace(text) != "" {
						onPartial(text)
					}
				}
			}()
			snapshot = func(wav []byte) {
				// Keep only the newest preview while the local model is busy.
				select {
				case snapshots <- wav:
				default:
					select {
					case <-snapshots:
					default:
					}
					select {
					case snapshots <- wav:
					default:
					}
				}
			}
			stopPartials = func() {
				cancel()
				close(snapshots)
				<-done
			}
		}
		data, err := recordMicrophoneProgress(recordCtx, duration, snapshot)
		stopPartials()
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
	return recordMicrophoneProgress(ctx, duration, nil)
}

// recordMicrophoneProgress emits bounded, self-contained WAV snapshots while
// arecord writes the full recording. A snapshot is only a preview: the final
// transcriber always reads the recorder's completed WAV file.
func recordMicrophoneProgress(ctx context.Context, duration time.Duration, onSnapshot func([]byte)) ([]byte, error) {
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
	var ticker *time.Ticker
	var ticks <-chan time.Time
	if onSnapshot != nil {
		ticker = time.NewTicker(time.Second)
		defer ticker.Stop()
		ticks = ticker.C
	}
	previewCount := 0
	nextPreview := time.Now().Add(4 * time.Second)
	recording := true
	for recording {
		select {
		case runErr = <-finished:
			recording = false
		case <-ticks:
			if previewCount >= 4 || time.Now().Before(nextPreview) {
				continue
			}
			if wav, err := readRecordingPreview(path); err == nil {
				onSnapshot(wav)
				previewCount++
				nextPreview = time.Now().Add(12 * time.Second)
			}
		case <-recordCtx.Done():
			stopped = true
			// Give arecord a chance to finalize the WAV header before reading it.
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				_ = cmd.Process.Kill()
			}
			timer := time.NewTimer(3 * time.Second)
			select {
			case runErr = <-finished:
				timer.Stop()
			case <-timer.C:
				_ = cmd.Process.Kill()
				runErr = <-finished
			}
			recording = false
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

// readRecordingPreview replaces arecord's in-progress RIFF lengths with the
// number of PCM bytes currently on disk. The recorder is fixed at 16 kHz,
// mono PCM16, and the preview has no trailing chunks.
func readRecordingPreview(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 || !bytes.Equal(data[:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WAVE")) {
		return nil, fmt.Errorf("cabeçalho WAV incompleto")
	}
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if bytes.Equal(data[offset:offset+4], []byte("data")) {
			start := offset + 8
			pcmBytes := (len(data) - start) &^ 1
			if pcmBytes < 16000*2*3 || start+pcmBytes > audio.MaxAudioBytes {
				return nil, fmt.Errorf("prévia de áudio insuficiente")
			}
			wav := append([]byte(nil), data[:start+pcmBytes]...)
			binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
			binary.LittleEndian.PutUint32(wav[offset+4:offset+8], uint32(pcmBytes))
			return wav, nil
		}
		if size < 0 || size > len(data)-offset-8 {
			break
		}
		offset += 8 + size + size%2
	}
	return nil, fmt.Errorf("amostras WAV indisponíveis")
}
