package imageproc

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

// tinyPNG generates a small in-memory PNG for test inputs.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Fill with a non-zero colour so the image isn't just transparent black.
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode tiny PNG: %v", err)
	}
	return buf.Bytes()
}

// tinyJPEG generates a small in-memory JPEG for format-detection tests.
func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 0, G: 128, B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("failed to encode tiny JPEG: %v", err)
	}
	return buf.Bytes()
}

// ─── DetectImageFormat ────────────────────────────────────────────────────────

func TestDetectImageFormat_PNG(t *testing.T) {
	data := tinyPNG(t)
	got := DetectImageFormat(data)
	if got != "image/png" {
		t.Errorf("DetectImageFormat(PNG) = %q, want %q", got, "image/png")
	}
}

func TestDetectImageFormat_JPEG(t *testing.T) {
	data := tinyJPEG(t)
	got := DetectImageFormat(data)
	if got != "image/jpeg" {
		t.Errorf("DetectImageFormat(JPEG) = %q, want %q", got, "image/jpeg")
	}
}

func TestDetectImageFormat_GIF(t *testing.T) {
	// GIF magic bytes: "GIF89a" (0x47 0x49 0x46 ...)
	data := []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}
	got := DetectImageFormat(data)
	if got != "image/gif" {
		t.Errorf("DetectImageFormat(GIF) = %q, want %q", got, "image/gif")
	}
}

func TestDetectImageFormat_WebP(t *testing.T) {
	// RIFF....WEBP
	data := []byte{
		0x52, 0x49, 0x46, 0x46, // RIFF
		0x00, 0x00, 0x00, 0x00, // file size (placeholder)
		0x57, 0x45, 0x42, 0x50, // WEBP
	}
	got := DetectImageFormat(data)
	if got != "image/webp" {
		t.Errorf("DetectImageFormat(WebP) = %q, want %q", got, "image/webp")
	}
}

func TestDetectImageFormat_TooShortDefaultsToPNG(t *testing.T) {
	got := DetectImageFormat([]byte{0x00, 0x01})
	if got != "image/png" {
		t.Errorf("DetectImageFormat(<4 bytes) = %q, want default %q", got, "image/png")
	}
}

func TestDetectImageFormat_UnknownDefaultsToPNG(t *testing.T) {
	// Bytes that don't match any known format.
	got := DetectImageFormat([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05})
	if got != "image/png" {
		t.Errorf("DetectImageFormat(unknown) = %q, want default %q", got, "image/png")
	}
}

// ─── DetectImageFormatFromBase64 ─────────────────────────────────────────────

func TestDetectImageFormatFromBase64_PNG(t *testing.T) {
	data := tinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(data)
	got := DetectImageFormatFromBase64(b64)
	if got != "image/png" {
		t.Errorf("DetectImageFormatFromBase64(PNG) = %q, want %q", got, "image/png")
	}
}

func TestDetectImageFormatFromBase64_JPEG(t *testing.T) {
	data := tinyJPEG(t)
	b64 := base64.StdEncoding.EncodeToString(data)
	got := DetectImageFormatFromBase64(b64)
	if got != "image/jpeg" {
		t.Errorf("DetectImageFormatFromBase64(JPEG) = %q, want %q", got, "image/jpeg")
	}
}

func TestDetectImageFormatFromBase64_InvalidDefaultsToPNG(t *testing.T) {
	got := DetectImageFormatFromBase64("!!!not base64!!!")
	if got != "image/png" {
		t.Errorf("DetectImageFormatFromBase64(invalid) = %q, want default %q", got, "image/png")
	}
}

func TestDetectImageFormatFromBase64_EmptyDefaultsToPNG(t *testing.T) {
	got := DetectImageFormatFromBase64("")
	if got != "image/png" {
		t.Errorf("DetectImageFormatFromBase64(\"\") = %q, want default %q", got, "image/png")
	}
}

// ─── GetTargetDimensions ──────────────────────────────────────────────────────

func TestGetTargetDimensions_AlreadyFits(t *testing.T) {
	w, h := GetTargetDimensions(800, 600)
	if w != 800 || h != 600 {
		t.Errorf("GetTargetDimensions(800,600) = (%d,%d), want (800,600)", w, h)
	}
}

func TestGetTargetDimensions_ExactlyAtLimit(t *testing.T) {
	w, h := GetTargetDimensions(ImageMaxWidth, ImageMaxHeight)
	if w != ImageMaxWidth || h != ImageMaxHeight {
		t.Errorf("GetTargetDimensions(max,max) = (%d,%d), want (%d,%d)",
			w, h, ImageMaxWidth, ImageMaxHeight)
	}
}

func TestGetTargetDimensions_ScalesDownWide(t *testing.T) {
	// 16000×8000 → should scale 50% to 8000×4000.
	w, h := GetTargetDimensions(16000, 8000)
	if w != 8000 || h != 4000 {
		t.Errorf("GetTargetDimensions(16000,8000) = (%d,%d), want (8000,4000)", w, h)
	}
}

func TestGetTargetDimensions_ScalesDownTall(t *testing.T) {
	// 8000×16000 → should scale 50% to 4000×8000.
	w, h := GetTargetDimensions(8000, 16000)
	if w != 4000 || h != 8000 {
		t.Errorf("GetTargetDimensions(8000,16000) = (%d,%d), want (4000,8000)", w, h)
	}
}

func TestGetTargetDimensions_PreservesAspectRatio(t *testing.T) {
	// 20000×10000 → scale by 0.4 → 8000×4000 (2:1 aspect preserved).
	w, h := GetTargetDimensions(20000, 10000)
	if w > ImageMaxWidth || h > ImageMaxHeight {
		t.Errorf("GetTargetDimensions(20000,10000) = (%d,%d) exceeds limits", w, h)
	}
	// Aspect ratio check (should still be ~2:1).
	if w == 0 || h == 0 {
		t.Fatalf("zero dimension returned")
	}
	ratio := float64(w) / float64(h)
	if ratio < 1.99 || ratio > 2.01 {
		t.Errorf("aspect ratio not preserved: got %f, want ~2.0", ratio)
	}
}

func TestGetTargetDimensions_ZeroOrNegative(t *testing.T) {
	w, h := GetTargetDimensions(0, 100)
	if w != 0 || h != 100 {
		t.Errorf("GetTargetDimensions(0,100) = (%d,%d), want (0,100)", w, h)
	}
	w, h = GetTargetDimensions(-5, -10)
	if w != -5 || h != -10 {
		t.Errorf("GetTargetDimensions(-5,-10) = (%d,%d), want (-5,-10)", w, h)
	}
}

// ─── ResizeImage ──────────────────────────────────────────────────────────────

func TestResizeImage_SmallPNGPassthrough(t *testing.T) {
	data := tinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(data)

	result, stats, err := ResizeImage(b64, "image/png")
	if err != nil {
		t.Fatalf("ResizeImage returned error: %v", err)
	}
	if result != b64 {
		t.Errorf("expected passthrough of small PNG, got different data")
	}
	if stats.ResizeReason != "none" {
		t.Errorf("ResizeReason = %q, want %q", stats.ResizeReason, "none")
	}
	if stats.OriginalWidth != 10 || stats.OriginalHeight != 10 {
		t.Errorf("dimensions = (%d,%d), want (10,10)",
			stats.OriginalWidth, stats.OriginalHeight)
	}
	if stats.OriginalBytes != len(data) {
		t.Errorf("OriginalBytes = %d, want %d", stats.OriginalBytes, len(data))
	}
	if stats.NewBytes != len(data) {
		t.Errorf("NewBytes = %d, want %d", stats.NewBytes, len(data))
	}
}

func TestResizeImage_InvalidBase64(t *testing.T) {
	_, _, err := ResizeImage("!!!not valid base64!!!", "image/png")
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
	re, ok := err.(*ResizeError)
	if !ok {
		t.Fatalf("expected *ResizeError, got %T", err)
	}
	if re.Category != ErrDecodeFailure {
		t.Errorf("Category = %q, want %q", re.Category, ErrDecodeFailure)
	}
}

func TestResizeImage_EmptyData(t *testing.T) {
	_, _, err := ResizeImage("", "image/png")
	if err == nil {
		t.Fatal("expected error for empty data, got nil")
	}
	re, ok := err.(*ResizeError)
	if !ok {
		t.Fatalf("expected *ResizeError, got %T", err)
	}
	if re.Category != ErrDecodeFailure {
		t.Errorf("Category = %q, want %q", re.Category, ErrDecodeFailure)
	}
}

func TestResizeImage_EmptyMediaTypeAutoDetect(t *testing.T) {
	data := tinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(data)

	_, stats, err := ResizeImage(b64, "")
	if err != nil {
		t.Fatalf("ResizeImage with empty mediaType: %v", err)
	}
	if !strings.Contains(stats.MediaType, "png") {
		t.Errorf("auto-detect MediaType = %q, want to contain \"png\"", stats.MediaType)
	}
}

// ─── ClassifyResizeError ──────────────────────────────────────────────────────

func TestClassifyResizeError_Nil(t *testing.T) {
	got := ClassifyResizeError(nil)
	if got != ErrUnknown {
		t.Errorf("ClassifyResizeError(nil) = %q, want %q", got, ErrUnknown)
	}
}

func TestClassifyResizeError_ResizeError(t *testing.T) {
	src := &ResizeError{Category: ErrDimensionCheckFailure, Msg: "too big"}
	got := ClassifyResizeError(src)
	if got != ErrDimensionCheckFailure {
		t.Errorf("ClassifyResizeError(*ResizeError) = %q, want %q",
			got, ErrDimensionCheckFailure)
	}
}

func TestClassifyResizeError_FromGenericErrors(t *testing.T) {
	cases := []struct {
		msg  string
		want ResizeErrorCategory
	}{
		{"operation timed out", ErrProcessTimeout},
		{"cannot decode image", ErrDecodeFailure},
		{"unsupported format", ErrDecodeFailure},
		{"encode failed", ErrEncodeFailure},
		{"compress step failed", ErrEncodeFailure},
		{"dimension exceeded", ErrDimensionCheckFailure},
		{"pixel overflow", ErrDimensionCheckFailure},
		{"file size too large", ErrFileSizeCheckFailure},
		{"unknown mime type", ErrUnknownMimeType},
		{"random weird problem", ErrUnknown},
	}
	for _, tc := range cases {
		got := ClassifyResizeError(errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("ClassifyResizeError(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

func TestResizeError_ErrorString(t *testing.T) {
	e := &ResizeError{Category: ErrEncodeFailure, Msg: "jpeg encode blew up"}
	got := e.Error()
	if !strings.Contains(got, "encodeFailure") {
		t.Errorf("Error() = %q, should contain category", got)
	}
	if !strings.Contains(got, "jpeg encode blew up") {
		t.Errorf("Error() = %q, should contain msg", got)
	}
}

// ─── CreateImageMetadataText ─────────────────────────────────────────────────

func TestCreateImageMetadataText_NoInfo(t *testing.T) {
	got := CreateImageMetadataText(ImageDimensions{}, "")
	if got != "" {
		t.Errorf("CreateImageMetadataText(empty) = %q, want \"\"", got)
	}
}

func TestCreateImageMetadataText_SourceOnly(t *testing.T) {
	got := CreateImageMetadataText(ImageDimensions{}, "/tmp/pic.png")
	if !strings.Contains(got, "/tmp/pic.png") {
		t.Errorf("expected source path in metadata, got %q", got)
	}
}

func TestCreateImageMetadataText_Resized(t *testing.T) {
	dims := ImageDimensions{
		OriginalWidth:  1920,
		OriginalHeight: 1080,
		DisplayWidth:   960,
		DisplayHeight:  540,
	}
	got := CreateImageMetadataText(dims, "/foo/bar.png")
	if !strings.Contains(got, "1920x1080") {
		t.Errorf("expected original dimensions in output, got %q", got)
	}
	if !strings.Contains(got, "960x540") {
		t.Errorf("expected display dimensions in output, got %q", got)
	}
	if !strings.Contains(got, "2.00") {
		t.Errorf("expected scale factor 2.00 in output, got %q", got)
	}
	if !strings.Contains(got, "/foo/bar.png") {
		t.Errorf("expected source path in output, got %q", got)
	}
}

func TestCreateImageMetadataText_NoResizeNoSource(t *testing.T) {
	dims := ImageDimensions{
		OriginalWidth:  800,
		OriginalHeight: 600,
		DisplayWidth:   800,
		DisplayHeight:  600,
	}
	got := CreateImageMetadataText(dims, "")
	if got != "" {
		t.Errorf("expected empty string for non-resized no-source case, got %q", got)
	}
}

// ─── CompressImageToByteLimit ─────────────────────────────────────────────────

func TestCompressImageToByteLimit_AlreadyUnderLimit(t *testing.T) {
	data := tinyPNG(t)
	out, mt, err := CompressImageToByteLimit(data, len(data)+1000, "image/png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("expected identical bytes passthrough when already under limit")
	}
	if mt != "image/png" {
		t.Errorf("MediaType = %q, want \"image/png\"", mt)
	}
}

func TestCompressImageToTokenLimit_AlreadyUnderLimit(t *testing.T) {
	data := tinyPNG(t)
	// 10k tokens → maxBytes ≈ 60000, well above our tiny PNG.
	out, mt, err := CompressImageToTokenLimit(data, 10000, "image/png")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("expected identical bytes when already under token limit")
	}
	if mt != "image/png" {
		t.Errorf("MediaType = %q, want \"image/png\"", mt)
	}
}
