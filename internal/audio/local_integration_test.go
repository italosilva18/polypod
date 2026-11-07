package audio

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// This optional test exercises the real Python library, model, ffmpeg and Go
// adapter together with a real OGG/Opus fixture, the WhatsApp voice format.
// Set the three paths to run it; ordinary unit tests stay independent of
// installed model files and external downloads.
func TestLocalWhisperIntegration(t *testing.T) {
	python := os.Getenv("POLYPOD_ASR_TEST_PYTHON")
	model := os.Getenv("POLYPOD_ASR_TEST_MODEL")
	fixture := os.Getenv("POLYPOD_ASR_TEST_FIXTURE")
	if python == "" || model == "" || fixture == "" {
		t.Skip("real Whisper integration paths not configured")
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	transcriber, err := NewLocalTranscriber(LocalConfig{
		PythonBinary: python,
		ScriptPath:   "../../scripts/transcribe-local.py",
		ModelPath:    model,
		FFmpegBinary: "ffmpeg",
		Language:     "en",
		Timeout:      2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := transcriber.Transcribe(context.Background(), data, "audio/ogg; codecs=opus")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(got), "my fellow americans") ||
		!strings.Contains(strings.ToLower(got), "your country") {
		t.Fatalf("fixture transcript did not contain expected speech: %q", got)
	}
}
