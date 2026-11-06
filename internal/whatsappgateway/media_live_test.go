package whatsappgateway

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/costa/polypod/internal/chatgptauth"
	"github.com/costa/polypod/internal/provider"
)

// Opt-in integration test. It uses only a synthetic image and the existing
// ChatGPT OAuth session; it never opens a WhatsApp store or sends a message.
func TestMediaInterpreterLiveVision(t *testing.T) {
	if os.Getenv("POLYPOD_MEDIA_LIVE_TEST") != "1" {
		t.Skip("set POLYPOD_MEDIA_LIVE_TEST=1 to exercise the configured ChatGPT vision path")
	}
	model := os.Getenv("POLYPOD_MEDIA_LIVE_MODEL")
	if model == "" {
		model = "gpt-6.1-sol"
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 320))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(65, 65, 255, 255), &image.Uniform{C: color.RGBA{R: 230, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	dir, path := mediaFixture(t, buf.Bytes())
	credentialPath := chatgptauth.DefaultCredentialPath()
	if !filepath.IsAbs(credentialPath) {
		credentialPath = filepath.Join("..", "..", credentialPath)
	}
	manager := chatgptauth.NewManager(credentialPath)
	interpreter := &MediaInterpreter{
		MediaDir:    dir,
		Vision:      provider.NewChatGPT(manager.AccessToken),
		VisionModel: model,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	text, err := interpreter.Render(ctx, Event{Text: "Descreva a imagem.", MediaPath: path, MIME: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "vermelh") && !strings.Contains(lower, "red") {
		t.Fatalf("vision response did not identify the synthetic red square: %q", text)
	}
	if strings.Contains(text, path) {
		t.Fatal("private media path leaked into rendered text")
	}
	t.Logf("synthetic image description: %s", text)
}
