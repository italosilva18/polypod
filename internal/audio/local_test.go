package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalTranscriberOGG(t *testing.T) {
	cfg, logPath := localTestConfig(t, `printf '%s\n' "$@" > "$ARGS_LOG"; printf '{"text":" Olá, mundo! ","language":"pt"}'`)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transcriber.Transcribe(context.Background(), []byte("OggS\x00\x01fake"), "audio/ogg; codecs=opus")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Olá, mundo!" {
		t.Fatalf("transcrição = %q", got)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(parts) != 6 || parts[0] != cfg.ScriptPath || parts[2] != "--model-path" || parts[3] != cfg.ModelPath || parts[4] != "--language" || parts[5] != "pt" {
		t.Fatalf("argumentos do worker incorretos: %q", parts)
	}
	if _, err := os.Stat(parts[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WAV temporário não removido: %v", err)
	}
}

func TestLocalTranscriberSilence(t *testing.T) {
	cfg, _ := localTestConfig(t, `printf '{"text":"","language":"pt"}'`)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := transcriber.Transcribe(context.Background(), []byte("OggSfake"), "audio/opus")
	if err != nil || got != "" {
		t.Fatalf("silêncio = %q, %v", got, err)
	}
}

func TestLocalTranscriberRejectsInvalidAudio(t *testing.T) {
	cfg, _ := localTestConfig(t, `printf '{"text":"ok"}'`)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		data []byte
		mime string
	}{
		{[]byte("not ogg"), "audio/ogg"},
		{[]byte("OggSfake"), "image/png"},
		{make([]byte, MaxAudioBytes+1), "audio/ogg"},
	} {
		if _, err := transcriber.Transcribe(context.Background(), tc.data, tc.mime); !errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("Transcribe(%q) = %v; esperado formato inválido", tc.mime, err)
		}
	}
}

func TestLocalTranscriberTimeout(t *testing.T) {
	cfg, _ := localTestConfig(t, `exec sleep 2`)
	cfg.Timeout = 50 * time.Millisecond
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transcriber.Transcribe(context.Background(), []byte("OggSfake"), "audio/ogg")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
}

func TestLocalTranscriberBoundsJSONOutput(t *testing.T) {
	cfg, _ := localTestConfig(t, `cat "$LARGE_JSON"`)
	large := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(large, []byte(`{"text":"`+strings.Repeat("a", maxTranscriptBytes)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARGE_JSON", large)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transcriber.Transcribe(context.Background(), []byte("OggSfake"), "audio/ogg")
	if err == nil || !strings.Contains(err.Error(), "excede limite") {
		t.Fatalf("saída excessiva = %v", err)
	}
}

func TestLocalTranscriberRejectsLongAudio(t *testing.T) {
	cfg, _ := localTestConfig(t, `printf '{"text":"ok"}'`)
	cfg.Timeout = 5 * time.Second
	header, err := os.ReadFile(os.Getenv("WAV_FIXTURE"))
	if err != nil {
		t.Fatal(err)
	}
	longWAV := filepath.Join(t.TempDir(), "long.wav")
	if err := os.WriteFile(longWAV, header, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(longWAV, maxPCMBytes+1); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAV_FIXTURE", longWAV)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transcriber.Transcribe(context.Background(), []byte("OggSfake"), "audio/ogg")
	if err == nil || !strings.Contains(err.Error(), "cinco minutos") {
		t.Fatalf("áudio longo = %v", err)
	}
}

func TestLocalTranscriberSerializesWhisper(t *testing.T) {
	cfg, _ := localTestConfig(t, `printf 'start\n' >> "$STARTS"; while [ ! -e "$RELEASE" ]; do sleep 0.01; done; printf '{"text":"ok"}'`)
	cfg.Timeout = 3 * time.Second
	starts := filepath.Join(t.TempDir(), "starts")
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("STARTS", starts)
	t.Setenv("RELEASE", release)
	transcriber, err := NewLocalTranscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	transcribe := func() {
		_, err := transcriber.Transcribe(context.Background(), []byte("OggSfake"), "audio/ogg")
		results <- err
	}
	go transcribe()
	deadline := time.After(time.Second)
	for {
		data, _ := os.ReadFile(starts)
		if strings.Count(string(data), "start") == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("primeira transcrição não iniciou")
		case <-time.After(10 * time.Millisecond):
		}
	}
	go transcribe()
	time.Sleep(100 * time.Millisecond)
	data, err := os.ReadFile(starts)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "start"); got != 1 {
		t.Fatalf("%d processos Whisper simultâneos", got)
	}
	if err := os.WriteFile(release, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func localTestConfig(t *testing.T, workerBody string) (LocalConfig, string) {
	t.Helper()
	dir := t.TempDir()
	wav := filepath.Join(dir, "fixture.wav")
	header := make([]byte, 44)
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], 16000)
	binary.LittleEndian.PutUint32(header[28:32], 32000)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	if err := os.WriteFile(wav, header, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAV_FIXTURE", wav)
	ffmpeg := writeTestExecutable(t, dir, "ffmpeg", `for out do :; done; cp "$WAV_FIXTURE" "$out"`)
	python := writeTestExecutable(t, dir, "python", workerBody)
	script := filepath.Join(dir, "worker.py")
	model := filepath.Join(dir, "model.pt")
	if err := os.WriteFile(script, []byte("# worker fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("model fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "worker-args.log")
	t.Setenv("ARGS_LOG", logPath)
	return LocalConfig{
		PythonBinary: python, ScriptPath: script, ModelPath: model,
		FFmpegBinary: ffmpeg, Language: "pt", Timeout: time.Second,
	}, logPath
}

func writeTestExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
