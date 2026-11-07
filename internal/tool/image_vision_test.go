package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/types"
)

type localImageVisionStub struct {
	request provider.ChatRequest
}

func (p *localImageVisionStub) Name() string                   { return "test" }
func (p *localImageVisionStub) SupportsVision() bool           { return true }
func (p *localImageVisionStub) SupportsTools() bool            { return false }
func (p *localImageVisionStub) SupportsStructuredOutput() bool { return false }
func (p *localImageVisionStub) Chat(_ context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	p.request = req
	return &provider.ChatResponse{Content: "Um quadrado vermelho."}, nil
}
func (p *localImageVisionStub) ChatStream(ctx context.Context, req provider.ChatRequest, _ provider.StreamCallback) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestLocalImageVisionTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.png")
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := range 10 {
		for x := range 20 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	vision := &localImageVisionStub{}
	reg := NewRegistry()
	RegisterLocalImageVisionTool(reg, vision, "test-vision")
	tool, ok := reg.Get("image_analyze")
	if !ok {
		t.Fatal("image_analyze not registered")
	}
	args := map[string]any{"path": "fixture.png", "question": "Qual a cor?"}
	if !tool.ValidateInput(args).Valid || !tool.IsReadOnly(args) {
		t.Fatal("image tool metadata invalid")
	}
	result, err := tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, args)
	if err != nil || result.IsError || !strings.Contains(result.Content, "quadrado vermelho") {
		t.Fatalf("image analysis result = %+v, %v", result, err)
	}
	if vision.request.Model != "test-vision" || len(vision.request.Messages) != 2 ||
		len(vision.request.Messages[1].Parts) != 2 || vision.request.Messages[1].Parts[1].MimeType != "image/png" {
		t.Fatalf("image vision request = %+v", vision.request)
	}
	actual, err := base64.StdEncoding.DecodeString(vision.request.Messages[1].Parts[1].ImageB64)
	if err != nil || !bytes.Equal(actual, data.Bytes()) {
		t.Fatal("provider did not receive the validated image bytes")
	}
	if strings.Contains(vision.request.Messages[0].Content, path) || strings.Contains(vision.request.Messages[1].Parts[0].Text, path) {
		t.Fatal("private image path sent to provider")
	}
}

func TestLocalImageVisionToolRejectsInvalidImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fake.png"), []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	vision := &localImageVisionStub{}
	reg := NewRegistry()
	RegisterLocalImageVisionTool(reg, vision, "test-vision")
	tool, _ := reg.Get("image_analyze")
	result, err := tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, map[string]any{"path": "fake.png"})
	if err != nil || !result.IsError || len(vision.request.Messages) != 0 {
		t.Fatalf("invalid image reached provider: %+v, %v", result, err)
	}
}

func TestLocalImageVisionToolSendsOnlyFirstGIFFrame(t *testing.T) {
	dir := t.TempDir()
	palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	red := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	green := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
	for i := range green.Pix {
		green.Pix[i] = 1
	}
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{red, green}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "animated.gif"), animated.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	vision := &localImageVisionStub{}
	reg := NewRegistry()
	RegisterLocalImageVisionTool(reg, vision, "test-vision")
	tool, _ := reg.Get("image_analyze")
	if tool.IsConcurrencySafe(map[string]any{}) {
		t.Fatal("large image processing must be serialized")
	}
	result, err := tool.Execute(types.ToolContext{Context: context.Background(), WorkingDir: dir}, map[string]any{"path": "animated.gif"})
	if err != nil || result.IsError {
		t.Fatalf("GIF analysis failed: %+v, %v", result, err)
	}
	part := vision.request.Messages[1].Parts[1]
	if part.MimeType != "image/png" {
		t.Fatalf("GIF sent to provider as %q", part.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(part.ImageB64)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := png.Decode(bytes.NewReader(decoded))
	if err != nil {
		t.Fatal(err)
	}
	r, g, _, _ := frame.At(0, 0).RGBA()
	if r <= g {
		t.Fatalf("provider received a later GIF frame: red=%d green=%d", r, g)
	}
}
