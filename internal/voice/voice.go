// Package voice provides voice mode toggle and basic audio capture via
// external CLI tools (arecord/sox for recording, whisper/vosk for STT,
// espeak/piper for TTS). Ported from the TypeScript voice services.
package voice

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// VoiceConfig holds the settings for voice mode.
type VoiceConfig struct {
	Enabled    bool
	STTCommand string // speech-to-text binary (default "whisper")
	TTSCommand string // text-to-speech binary (default "espeak")
	Language   string // BCP-47 language tag   (default "pt-BR")
	Logger     *slog.Logger
}

// defaults fills zero-value fields with sensible defaults.
func (c *VoiceConfig) defaults() {
	if c.STTCommand == "" {
		c.STTCommand = "whisper"
	}
	if c.TTSCommand == "" {
		c.TTSCommand = "espeak"
	}
	if c.Language == "" {
		c.Language = "pt-BR"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// VoiceMode manages the runtime voice state (toggle, capture, STT, TTS).
type VoiceMode struct {
	mu      sync.RWMutex
	enabled bool
	cfg     VoiceConfig
}

// NewVoiceMode creates a VoiceMode from the given config.
func NewVoiceMode(cfg VoiceConfig) *VoiceMode {
	cfg.defaults()
	return &VoiceMode{
		enabled: cfg.Enabled,
		cfg:     cfg,
	}
}

// IsEnabled reports whether voice mode is currently active.
func (v *VoiceMode) IsEnabled() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.enabled
}

// Toggle flips voice mode on/off and returns the new state.
func (v *VoiceMode) Toggle() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.enabled = !v.enabled
	v.cfg.Logger.Info("voice mode toggled", "enabled", v.enabled)
	return v.enabled
}

// CaptureAudio records raw audio for the given duration using arecord (ALSA)
// or sox (rec). The captured WAV data is returned as a byte slice. The caller
// may write it to disk or pass the bytes directly to Transcribe.
func (v *VoiceMode) CaptureAudio(ctx context.Context, duration time.Duration) ([]byte, error) {
	v.mu.RLock()
	lang := v.cfg.Language
	logger := v.cfg.Logger
	v.mu.RUnlock()

	_ = lang // reserved for future per-language sample-rate tuning

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod-voice-%d.wav", time.Now().UnixNano()))

	// Try arecord first (Linux/ALSA), then fall back to sox rec.
	cmd, args := captureCommand(tmpFile, duration)
	if cmd == "" {
		return nil, fmt.Errorf("nenhuma ferramenta de gravacao encontrada (instale arecord ou sox)")
	}

	logger.Debug("capturing audio", "cmd", cmd, "args", args, "duration", duration)

	c := exec.CommandContext(ctx, cmd, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		// Context cancellation is normal (user stopped recording early).
		if ctx.Err() != nil {
			// Partial file may still be usable — try to read it.
			if data, readErr := os.ReadFile(tmpFile); readErr == nil && len(data) > 0 {
				defer os.Remove(tmpFile)
				return data, nil
			}
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("falha na gravacao (%s): %w — %s", cmd, err, stderr.String())
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler arquivo de audio: %w", err)
	}
	defer os.Remove(tmpFile)

	return data, nil
}

// captureCommand returns the binary and arguments for audio capture.
// It prefers arecord on Linux, then falls back to sox rec on any platform.
func captureCommand(outPath string, dur time.Duration) (string, []string) {
	secs := fmt.Sprintf("%d", int(dur.Seconds()))

	// Linux: prefer arecord (ALSA utils).
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("arecord"); err == nil {
			return "arecord", []string{
				"-f", "S16_LE",
				"-r", "16000",
				"-c", "1",
				"-t", "wav",
				"-d", secs,
				"-q",
				outPath,
			}
		}
	}

	// Cross-platform: sox rec.
	if _, err := exec.LookPath("rec"); err == nil {
		return "rec", []string{
			"-q",
			"-r", "16000",
			"-e", "signed",
			"-b", "16",
			"-c", "1",
			outPath,
			"trim", "0", secs,
		}
	}

	return "", nil
}

// Transcribe runs the configured STT command on the audio file at audioPath
// and returns the transcribed text.
func (v *VoiceMode) Transcribe(ctx context.Context, audioPath string) (string, error) {
	v.mu.RLock()
	sttCmd := v.cfg.STTCommand
	lang := v.cfg.Language
	logger := v.cfg.Logger
	v.mu.RUnlock()

	if _, err := exec.LookPath(sttCmd); err != nil {
		return "", fmt.Errorf("comando STT %q nao encontrado no PATH", sttCmd)
	}

	args := sttArgs(sttCmd, audioPath, lang)
	logger.Debug("transcribing audio", "cmd", sttCmd, "args", args)

	c := exec.CommandContext(ctx, sttCmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		return "", fmt.Errorf("falha na transcricao (%s): %w — %s", sttCmd, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// sttArgs builds the argument list for known STT backends.
func sttArgs(cmd, audioPath, lang string) []string {
	base := filepath.Base(cmd)
	switch {
	case strings.Contains(base, "whisper"):
		// OpenAI Whisper CLI: whisper <file> --language pt --model small
		return []string{audioPath, "--language", langPrefix(lang), "--model", "small", "--output_format", "txt"}
	case strings.Contains(base, "vosk"):
		return []string{"-l", langPrefix(lang), "-i", audioPath}
	case strings.Contains(base, "deepspeech"):
		return []string{"--audio", audioPath}
	default:
		// Generic: pass the audio file as the sole argument.
		return []string{audioPath}
	}
}

// Speak runs the configured TTS command to synthesize the given text.
func (v *VoiceMode) Speak(ctx context.Context, text string) error {
	v.mu.RLock()
	ttsCmd := v.cfg.TTSCommand
	lang := v.cfg.Language
	logger := v.cfg.Logger
	v.mu.RUnlock()

	if _, err := exec.LookPath(ttsCmd); err != nil {
		return fmt.Errorf("comando TTS %q nao encontrado no PATH", ttsCmd)
	}

	args := ttsArgs(ttsCmd, text, lang)
	logger.Debug("speaking text", "cmd", ttsCmd, "len", len(text))

	c := exec.CommandContext(ctx, ttsCmd, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		return fmt.Errorf("falha na sintese de voz (%s): %w — %s", ttsCmd, err, stderr.String())
	}
	return nil
}

// ttsArgs builds the argument list for known TTS backends.
func ttsArgs(cmd, text, lang string) []string {
	base := filepath.Base(cmd)
	switch {
	case strings.Contains(base, "espeak"):
		return []string{"-v", langPrefix(lang), text}
	case base == "say":
		// macOS built-in say command.
		return []string{text}
	case strings.Contains(base, "piper"):
		return []string{"--output-raw", "--text", text}
	default:
		return []string{text}
	}
}

// Available reports whether the configured STT and TTS binaries are
// reachable in PATH.
func (v *VoiceMode) Available() bool {
	v.mu.RLock()
	stt := v.cfg.STTCommand
	tts := v.cfg.TTSCommand
	v.mu.RUnlock()

	_, sttErr := exec.LookPath(stt)
	_, ttsErr := exec.LookPath(tts)
	return sttErr == nil && ttsErr == nil
}

// ---------------------------------------------------------------------------
// Auto-detection helpers
// ---------------------------------------------------------------------------

// DetectSTT probes PATH for known STT binaries and returns the first one
// found. Returns an empty string when none are available.
func DetectSTT() string {
	for _, name := range []string{"whisper", "vosk", "deepspeech"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// DetectTTS probes PATH for known TTS binaries and returns the first one
// found. Returns an empty string when none are available.
func DetectTTS() string {
	candidates := []string{"espeak", "piper"}
	if runtime.GOOS == "darwin" {
		// On macOS, the built-in "say" command is always available.
		candidates = append([]string{"say"}, candidates...)
	}
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// langPrefix extracts the primary language subtag from a BCP-47 tag
// (e.g. "pt-BR" → "pt"). Several CLI tools only accept the short form.
func langPrefix(bcp47 string) string {
	if i := strings.IndexByte(bcp47, '-'); i > 0 {
		return bcp47[:i]
	}
	return bcp47
}
