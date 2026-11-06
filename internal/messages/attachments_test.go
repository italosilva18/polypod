package messages

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestResizeImage(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			source.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}

	resized, mime, err := ResizeImage(encoded.Bytes(), 10, 10)
	if err != nil {
		t.Fatalf("ResizeImage: %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("mime = %q; want image/png", mime)
	}
	decoded, _, err := image.Decode(bytes.NewReader(resized))
	if err != nil {
		t.Fatalf("decode resized image: %v", err)
	}
	if got := decoded.Bounds().Size(); got.X != 10 || got.Y != 5 {
		t.Fatalf("size = %v; want 10x5", got)
	}
}
