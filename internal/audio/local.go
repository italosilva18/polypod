package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultLocalTimeout = 15 * time.Minute
	maxLocalTimeout     = time.Hour
	maxTranscriptBytes  = 128 << 10
	maxDiagnosticBytes  = 4 << 10
	// A five-minute, mono, 16 kHz PCM16 recording is 9,600,000 bytes.
	maxPCMBytes = 5 * 60 * 16000 * 2
)

// LocalConfig runs an installed Python transcription worker with an already
// present Whisper .pt checkpoint or offline CTranslate2 model directory. It
// never downloads a model or sends audio over the network.
type LocalConfig struct {
	PythonBinary string
	ScriptPath   string
	ModelPath    string
	FFmpegBinary string
	Language     string
	Timeout      time.Duration
}

// LocalTranscriber converts received audio to bounded PCM and invokes a local
// Whisper worker. It implements the same Transcribe method as HTTPTranscriber.
type LocalTranscriber struct {
	python   string
	script   string
	model    string
	ffmpeg   string
	language string
	timeout  time.Duration
}

// The WhatsApp gateway and CLI each construct a LocalTranscriber. A shared
// gate prevents both from loading large model copies at the same time.
var localTranscriptionGate = make(chan struct{}, 1)

func NewLocalTranscriber(cfg LocalConfig) (*LocalTranscriber, error) {
	if strings.TrimSpace(cfg.PythonBinary) == "" {
		cfg.PythonBinary = "python3"
	}
	if strings.TrimSpace(cfg.FFmpegBinary) == "" {
		cfg.FFmpegBinary = "ffmpeg"
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = "pt"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultLocalTimeout
	}
	if cfg.Timeout < 0 || cfg.Timeout > maxLocalTimeout {
		return nil, errors.New("timeout de transcrição local inválido")
	}
	python, err := resolveExecutable(cfg.PythonBinary)
	if err != nil {
		return nil, fmt.Errorf("Python de transcrição local: %w", err)
	}
	ffmpeg, err := resolveExecutable(cfg.FFmpegBinary)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg de transcrição local: %w", err)
	}
	script, err := regularFilePath(cfg.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("script de transcrição local: %w", err)
	}
	model, err := ValidateLocalModelPath(cfg.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("modelo de transcrição local: %w", err)
	}
	return &LocalTranscriber{
		python: python, script: script, model: model, ffmpeg: ffmpeg,
		language: cfg.Language, timeout: cfg.Timeout,
	}, nil
}

func (t *LocalTranscriber) Transcribe(ctx context.Context, data []byte, declaredMIME string) (string, error) {
	if len(data) == 0 || len(data) > MaxAudioBytes {
		return "", ErrUnsupportedAudio
	}
	mediaType, _, err := mime.ParseMediaType(declaredMIME)
	if err != nil {
		return "", ErrUnsupportedAudio
	}
	ext, valid := audioExtension(mediaType, data)
	if !valid {
		return "", ErrUnsupportedAudio
	}

	runCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	// The base Whisper model uses hundreds of MiB in a single CPU process.
	// Admit one transcription at a time so concurrent WhatsApp deliveries do
	// not launch multiple model copies and exhaust this machine's memory.
	select {
	case localTranscriptionGate <- struct{}{}:
		defer func() { <-localTranscriptionGate }()
	case <-runCtx.Done():
		return "", runCtx.Err()
	}
	// The service and an independently launched CLI may be separate processes.
	// Coordinate on the same local model inode before loading either copy.
	releaseModel, err := acquireModelLock(runCtx, t.model)
	if err != nil {
		return "", fmt.Errorf("aguardando modelo de transcrição local: %w", err)
	}
	defer releaseModel()
	dir, err := os.MkdirTemp("", "polypod-asr-")
	if err != nil {
		return "", fmt.Errorf("criar diretório temporário de áudio: %w", err)
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "input"+ext)
	wav := filepath.Join(dir, "audio.wav")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return "", fmt.Errorf("gravar áudio temporário: %w", err)
	}

	// 301 seconds lets us detect and reject recordings beyond five minutes
	// instead of returning a silently truncated transcription. The output is
	// always mono PCM16 at 16 kHz, which Whisper can consume consistently.
	ffmpeg := exec.CommandContext(runCtx, t.ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", input, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-t", "301", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le",
		"-f", "wav", wav)
	if err := runLocalCommand(runCtx, ffmpeg, maxDiagnosticBytes, "decoder"); err != nil {
		return "", fmt.Errorf("conversão de áudio local falhou: %w", err)
	}
	if err := validateWAV(wav); err != nil {
		return "", err
	}

	worker := exec.CommandContext(runCtx, t.python, t.script, wav,
		"--model-path", t.model, "--language", t.language)
	stdout, err := runLocalCommandWithOutput(runCtx, worker, maxTranscriptBytes, "worker")
	if err != nil {
		return "", fmt.Errorf("transcrição local falhou: %w", err)
	}
	var result struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil || result.Text == nil {
		return "", errors.New("resposta de transcrição local inválida")
	}
	transcript := strings.TrimSpace(*result.Text)
	if !utf8.ValidString(transcript) {
		return "", errors.New("transcrição local não contém UTF-8 válido")
	}
	// Silence is a valid result. The media interpreter can report it without
	// retrying the same WhatsApp delivery indefinitely.
	return transcript, nil
}

func resolveExecutable(name string) (string, error) {
	name = expandHome(strings.TrimSpace(name))
	if name == "" {
		return "", errors.New("executável não configurado")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

func regularFilePath(name string) (string, error) {
	name = expandHome(strings.TrimSpace(name))
	if name == "" {
		return "", errors.New("caminho não configurado")
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("caminho não aponta para arquivo regular com conteúdo")
	}
	return path, nil
}

// ValidateLocalModelPath accepts a non-empty Whisper .pt checkpoint or an
// offline CTranslate2 directory containing model.bin and config.json. It
// rejects symlinks in the model path and required files so the model cannot be
// redirected to a different location after configuration review.
func ValidateLocalModelPath(name string) (string, error) {
	name = expandHome(strings.TrimSpace(name))
	if name == "" {
		return "", errors.New("modelo não configurado")
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if resolved != path {
		return "", errors.New("caminho do modelo contém link simbólico")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() {
		if filepath.Ext(path) != ".pt" || info.Size() == 0 {
			return "", errors.New("checkpoint Whisper deve ser arquivo .pt não vazio")
		}
		return path, nil
	}
	if !info.IsDir() {
		return "", errors.New("modelo deve ser arquivo .pt ou diretório CTranslate2")
	}
	for _, filename := range []string{"model.bin", "config.json"} {
		file, err := os.Lstat(filepath.Join(path, filename))
		if err != nil {
			return "", err
		}
		if !file.Mode().IsRegular() || file.Size() == 0 {
			return "", fmt.Errorf("diretório CTranslate2 exige %s regular não vazio", filename)
		}
	}
	return path, nil
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func validateWAV(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("ler WAV convertido: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 44 {
		return errors.New("WAV convertido inválido")
	}
	if info.Size() >= maxPCMBytes {
		return errors.New("áudio excede duração máxima de cinco minutos")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("abrir WAV convertido: %w", err)
	}
	defer f.Close()
	header := make([]byte, 12)
	if _, err := f.Read(header); err != nil || !bytes.Equal(header[:4], []byte("RIFF")) || !bytes.Equal(header[8:12], []byte("WAVE")) {
		return errors.New("WAV convertido inválido")
	}
	return nil
}

type boundedOutput struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining < n {
		b.truncated = true
		if remaining > 0 {
			_, _ = b.buf.Write(p[:remaining])
		}
		return n, nil
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func runLocalCommand(ctx context.Context, cmd *exec.Cmd, limit int, stage string) error {
	_, err := runLocalCommandWithOutput(ctx, cmd, limit, stage)
	return err
}

func runLocalCommandWithOutput(ctx context.Context, cmd *exec.Cmd, limit int, stage string) ([]byte, error) {
	stdout := &boundedOutput{limit: limit}
	stderr := &boundedOutput{limit: maxDiagnosticBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureLocalProcess(cmd)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, classifyLocalProcessError(stage, stderr.buf.String(), err)
	}
	if stdout.truncated {
		return nil, errors.New("saída excede limite")
	}
	return stdout.buf.Bytes(), nil
}

// Worker stderr may contain private paths or model output. Map only known
// failure signatures to stable, actionable categories before persisting it.
func classifyLocalProcessError(stage, diagnostic string, cause error) error {
	lower := strings.ToLower(diagnostic)
	switch {
	case strings.Contains(lower, "no space left on device"):
		return fmt.Errorf("sem espaço para áudio temporário: %w", cause)
	case strings.Contains(lower, "out of memory"), strings.Contains(lower, "cannot allocate memory"),
		strings.Contains(lower, "memoryerror"), strings.Contains(lower, "bad_alloc"):
		return fmt.Errorf("memória insuficiente para transcrição local: %w", cause)
	case stage == "decoder" && (strings.Contains(lower, "invalid data found when processing input") ||
		strings.Contains(lower, "moov atom not found") || strings.Contains(lower, "could not find codec parameters") ||
		strings.Contains(lower, "unsupported codec") || strings.Contains(lower, "decoder (codec") ||
		strings.Contains(lower, "stream map '0:a:0' matches no streams")):
		return fmt.Errorf("%w: decodificador rejeitou o conteúdo: %v", ErrUnsupportedAudio, cause)
	case stage == "worker" && (strings.Contains(lower, "modulenotfounderror") ||
		strings.Contains(lower, "importerror") || strings.Contains(lower, "error while loading shared libraries")):
		return fmt.Errorf("dependência Python de transcrição indisponível: %w", cause)
	case stage == "worker" && (strings.Contains(lower, "filenotfounderror") ||
		strings.Contains(lower, "model.bin") && strings.Contains(lower, "no such file")):
		return fmt.Errorf("modelo local de transcrição não pode ser lido: %w", cause)
	case stage == "decoder":
		return fmt.Errorf("ffmpeg não conseguiu converter o áudio: %w", cause)
	default:
		return fmt.Errorf("worker de transcrição local falhou: %w", cause)
	}
}
