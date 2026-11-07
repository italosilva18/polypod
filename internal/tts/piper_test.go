package tts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPiperRejectsInvalidText(t *testing.T) {
	p := &Piper{}
	for _, input := range []string{"", " \n ", strings.Repeat("a", MaxTextRunes+1), string([]byte{0xff})} {
		if _, err := p.Synthesize(context.Background(), input); err == nil {
			t.Fatalf("accepted invalid text of %d bytes", len(input))
		}
	}
}

func TestNewPiperExpandsHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"python", "ffmpeg", "voice.onnx", "voice.onnx.json"} {
		mode := os.FileMode(0600)
		if name == "python" || name == "ffmpeg" {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(home, name), []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
	}
	p, err := NewPiper(PiperConfig{PythonBinary: "~/python", FFmpegBinary: "~/ffmpeg", ModelPath: "~/voice.onnx"})
	if err != nil {
		t.Fatal(err)
	}
	if p.model != filepath.Join(home, "voice.onnx") {
		t.Fatalf("model path = %q", p.model)
	}
}

// Run with a private Piper environment and voice model to prove the actual
// Python model -> WAV -> ffmpeg -> OGG/Opus pipeline on this machine.
func TestPiperIntegration(t *testing.T) {
	python := os.Getenv("POLYPOD_TTS_TEST_PYTHON")
	model := os.Getenv("POLYPOD_TTS_TEST_MODEL")
	if python == "" || model == "" {
		t.Skip("real Piper integration paths not configured")
	}
	p, err := NewPiper(PiperConfig{PythonBinary: python, ModelPath: model, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	data, err := p.Synthesize(context.Background(), "Este é um teste da voz local do Polypod.")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("OggS")) {
		t.Fatal("voice note is not OGG")
	}
	path := t.TempDir() + "/voice.ogg"
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "opus" {
		t.Fatalf("voice codec = %q, want opus", strings.TrimSpace(string(out)))
	}
}
