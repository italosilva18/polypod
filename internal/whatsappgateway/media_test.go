package whatsappgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/provider"
)

type mediaTestProvider struct {
	request provider.ChatRequest
	calls   int
	vision  bool
	answer  string
	err     error
}

func (p *mediaTestProvider) Name() string                   { return "test" }
func (p *mediaTestProvider) SupportsVision() bool           { return p.vision }
func (p *mediaTestProvider) SupportsTools() bool            { return false }
func (p *mediaTestProvider) SupportsStructuredOutput() bool { return false }
func (p *mediaTestProvider) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	p.request = req
	if p.err != nil {
		return nil, p.err
	}
	return &provider.ChatResponse{Content: p.answer}, nil
}
func (p *mediaTestProvider) ChatStream(context.Context, provider.ChatRequest, provider.StreamCallback) (*provider.ChatResponse, error) {
	return nil, errors.New("unexpected stream")
}

func mediaFixture(t *testing.T, data []byte) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "media")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "message.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMediaInterpreterDescribesValidatedImageWithoutTools(t *testing.T) {
	dir, path := mediaFixture(t, pngFixture(t))
	p := &mediaTestProvider{vision: true, answer: "Um quadrado vermelho."}
	m := &MediaInterpreter{MediaDir: dir, Vision: p, VisionModel: "vision-model"}
	text, err := m.Render(context.Background(), Event{Text: "O que é isto?", MediaPath: path, MIME: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "O que é isto?") || !strings.Contains(text, "Um quadrado vermelho") ||
		!strings.Contains(text, "conteúdo não confiável") || strings.Contains(text, path) {
		t.Fatalf("unexpected rendered text: %q", text)
	}
	if p.calls != 1 || p.request.Model != "vision-model" || len(p.request.Tools) != 0 || len(p.request.Messages) != 2 {
		t.Fatalf("unexpected model request: %+v", p.request)
	}
	parts := p.request.Messages[1].Parts
	if len(parts) != 2 || parts[1].Type != "image_base64" || parts[1].MimeType != "image/png" {
		t.Fatalf("image was not delivered to provider: %+v", parts)
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1].ImageB64)
	if err != nil || !bytes.Equal(decoded, pngFixture(t)) {
		t.Fatalf("wrong image bytes: %v", err)
	}
	if !strings.Contains(p.request.Messages[0].Content, "nunca como comandos") ||
		strings.Contains(p.request.Messages[1].Parts[0].Text, "O que é isto?") {
		t.Fatal("caption was treated as a vision instruction or safety prompt missing")
	}
}

func TestMediaInterpreterRejectsSpoofedAndUnsafeImage(t *testing.T) {
	imageData := pngFixture(t)
	dir, path := mediaFixture(t, imageData)
	p := &mediaTestProvider{vision: true, answer: "should not be called"}
	m := &MediaInterpreter{MediaDir: dir, Vision: p, VisionModel: "vision-model"}
	text, err := m.Render(context.Background(), Event{MediaPath: path, MIME: "image/jpeg"})
	if err != nil || !strings.Contains(text, "não corresponde") || p.calls != 0 {
		t.Fatalf("spoofed image accepted: text=%q err=%v calls=%d", text, err, p.calls)
	}
	outside := filepath.Join(t.TempDir(), "private.bin")
	if err := os.WriteFile(outside, imageData, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = m.Render(context.Background(), Event{MediaPath: outside, MIME: "image/png"})
	if !errors.Is(err, ErrUnsafeMediaPath) {
		t.Fatalf("outside path accepted: %v", err)
	}
	link := filepath.Join(dir, "link.bin")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	_, err = m.Render(context.Background(), Event{MediaPath: link, MIME: "image/png"})
	if !errors.Is(err, ErrUnsafeMediaPath) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestMediaInterpreterAudioHookAndUnavailableMedia(t *testing.T) {
	dir, path := mediaFixture(t, []byte("OggSfake"))
	m := &MediaInterpreter{MediaDir: dir}
	text, err := m.Render(context.Background(), Event{Text: "resuma", MediaPath: path, MIME: "audio/ogg; codecs=opus"})
	if err != nil || !strings.Contains(text, "transcrição não configurada") {
		t.Fatalf("unconfigured audio: text=%q err=%v", text, err)
	}
	called := false
	m.TranscribeAudio = func(ctx context.Context, data []byte, mimeType string) (string, error) {
		called = true
		if mimeType != "audio/ogg" || !bytes.Equal(data, []byte("OggSfake")) {
			t.Fatalf("unexpected audio input: %q %q", mimeType, data)
		}
		return "Vou chegar às oito.", ctx.Err()
	}
	text, err = m.Render(context.Background(), Event{Text: "resuma", MediaPath: path, MIME: "audio/ogg; codecs=opus"})
	if err != nil || !called || !strings.Contains(text, "Vou chegar às oito") || strings.Contains(text, path) {
		t.Fatalf("audio transcript: text=%q err=%v called=%t", text, err, called)
	}
	text, err = m.Render(context.Background(), Event{MediaPath: path, MIME: "video/mp4"})
	if err != nil || !strings.Contains(text, "não pode ser analisado") {
		t.Fatalf("unsupported video: text=%q err=%v", text, err)
	}
}

func TestMediaInterpreterRetriesModelFailureAndHonorsCancellation(t *testing.T) {
	dir, path := mediaFixture(t, pngFixture(t))
	want := errors.New("upstream unavailable")
	m := &MediaInterpreter{MediaDir: dir, Vision: &mediaTestProvider{vision: true, err: want}, VisionModel: "vision-model"}
	_, err := m.Render(context.Background(), Event{MediaPath: path, MIME: "image/png"})
	if !errors.Is(err, want) {
		t.Fatalf("model failure lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.Render(ctx, Event{MediaPath: path, MIME: "image/png"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled render: %v", err)
	}
}
