// Package imageproc provides image processing utilities: resizing, compression,
// format detection, and validation. Migrated from TypeScript imageResizer.ts.
package imageproc

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os/exec"
	"strings"

	"golang.org/x/image/webp"
)

// API / size constants — mirrors apiLimits.ts constants.
const (
	// ImageMaxWidth is the maximum pixel width accepted by the Anthropic API.
	ImageMaxWidth = 8000
	// ImageMaxHeight is the maximum pixel height accepted by the Anthropic API.
	ImageMaxHeight = 8000
	// ImageTargetRawSize is the maximum raw-byte size of the decoded image data (5 MB).
	ImageTargetRawSize = 5 * 1024 * 1024
	// APIImageMaxBase64Size is the maximum base64-encoded image size (5 MB string length).
	APIImageMaxBase64Size = 5 * 1024 * 1024
)

// qualitySteps are the JPEG quality values tried in progressive compression.
var qualitySteps = []int{90, 80, 70, 60, 50, 40}

// ─── Error categories ───────────────────────────────────────────────────────

// ResizeErrorCategory classifies the kind of resize failure.
type ResizeErrorCategory string

const (
	ErrDecodeFailure         ResizeErrorCategory = "decodeFailure"
	ErrEncodeFailure         ResizeErrorCategory = "encodeFailure"
	ErrDimensionCheckFailure ResizeErrorCategory = "dimensionCheckFailure"
	ErrFileSizeCheckFailure  ResizeErrorCategory = "fileSizeCheckFailure"
	ErrQualityReduction      ResizeErrorCategory = "qualityReductionFailure"
	ErrUnknownMimeType       ResizeErrorCategory = "unknownMimeType"
	ErrProcessTimeout        ResizeErrorCategory = "processTimeout"
	ErrUnknown               ResizeErrorCategory = "unknownError"
)

// ResizeError is returned when image resizing/compression fails.
type ResizeError struct {
	Category ResizeErrorCategory
	Msg      string
}

func (e *ResizeError) Error() string {
	return fmt.Sprintf("image resize error [%s]: %s", e.Category, e.Msg)
}

// ClassifyResizeError infers a ResizeErrorCategory from a generic error.
func ClassifyResizeError(err error) ResizeErrorCategory {
	if err == nil {
		return ErrUnknown
	}
	if re, ok := err.(*ResizeError); ok {
		return re.Category
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return ErrProcessTimeout
	case strings.Contains(msg, "decode") || strings.Contains(msg, "corrupt") ||
		strings.Contains(msg, "unsupported") || strings.Contains(msg, "format"):
		return ErrDecodeFailure
	case strings.Contains(msg, "encode") || strings.Contains(msg, "compress"):
		return ErrEncodeFailure
	case strings.Contains(msg, "dimension") || strings.Contains(msg, "pixel"):
		return ErrDimensionCheckFailure
	case strings.Contains(msg, "size") || strings.Contains(msg, "too large"):
		return ErrFileSizeCheckFailure
	case strings.Contains(msg, "mime") || strings.Contains(msg, "media type"):
		return ErrUnknownMimeType
	default:
		return ErrUnknown
	}
}

// ─── Stats ───────────────────────────────────────────────────────────────────

// ResizeStats carries before/after metrics for a resize operation.
type ResizeStats struct {
	OriginalWidth  int
	OriginalHeight int
	NewWidth       int
	NewHeight      int
	OriginalBytes  int
	NewBytes       int
	Quality        int    // Final JPEG quality used (0 = not applicable)
	ResizeReason   string // "none" | "dimensions" | "size" | "both"
	MediaType      string // resulting media type, e.g. "image/jpeg"
}

// ─── Format detection ────────────────────────────────────────────────────────

// DetectImageFormat returns the MIME type from magic bytes.
// Falls back to "image/png" if the format cannot be determined.
func DetectImageFormat(data []byte) string {
	if len(data) < 4 {
		return "image/png"
	}
	// PNG: 89 50 4E 47
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	// JPEG: FF D8 FF
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF: 47 49 46
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "image/gif"
	}
	// WebP: RIFF....WEBP
	if len(data) >= 12 &&
		data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return "image/webp"
	}
	return "image/png"
}

// DetectImageFormatFromBase64 decodes the first bytes of base64 data to detect format.
func DetectImageFormatFromBase64(b64 string) string {
	// We only need the first ~16 bytes worth of header, so decode a small prefix.
	maxSrc := 20 // ceil(16*4/3) ≈ 22
	if len(b64) < maxSrc {
		maxSrc = len(b64)
	}
	// base64 decode may fail if not on 4-byte boundary; use a full block
	aligned := (maxSrc / 4) * 4
	if aligned == 0 {
		return "image/png"
	}
	header, err := base64.StdEncoding.DecodeString(b64[:aligned])
	if err != nil {
		return "image/png"
	}
	return DetectImageFormat(header)
}

// normalizeMediaType strips the "image/" prefix and normalises "jpg" → "jpeg".
func normalizeMediaType(mt string) string {
	mt = strings.ToLower(mt)
	mt = strings.TrimPrefix(mt, "image/")
	if mt == "jpg" {
		mt = "jpeg"
	}
	return mt
}

// ─── Dimension helpers ───────────────────────────────────────────────────────

// GetTargetDimensions returns the largest (w, h) that fits inside
// (ImageMaxWidth × ImageMaxHeight) while preserving aspect ratio.
// If the image already fits, the original dimensions are returned unchanged.
func GetTargetDimensions(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	if w <= ImageMaxWidth && h <= ImageMaxHeight {
		return w, h
	}
	scaleW := float64(ImageMaxWidth) / float64(w)
	scaleH := float64(ImageMaxHeight) / float64(h)
	scale := math.Min(scaleW, scaleH)
	return int(math.Round(float64(w) * scale)), int(math.Round(float64(h) * scale))
}

// ─── Core resize pipeline ────────────────────────────────────────────────────

// ResizeImage is the primary entry point. It accepts a base64-encoded image and
// its media type, applies dimension- and size-capping logic, and returns the
// (possibly compressed) base64 result together with ResizeStats.
//
// Strategy (mirrors maybeResizeAndDownsampleImageBuffer + compressImageBuffer):
//  1. If image already fits (≤ 5 MB raw AND ≤ 8000×8000), return as-is.
//  2. If only too large (bytes), try progressive JPEG compression at quality steps.
//  3. If dimensions exceed cap (or size still too large after compression), resize
//     to target dimensions then try JPEG quality steps again.
//  4. Last resort: resize to 1000×proportional + JPEG q=20.
func ResizeImage(base64Data string, mediaType string) (string, ResizeStats, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", ResizeStats{}, &ResizeError{ErrDecodeFailure, "base64 decode failed: " + err.Error()}
	}
	if len(raw) == 0 {
		return "", ResizeStats{}, &ResizeError{ErrDecodeFailure, "image data is empty"}
	}

	// Detect actual format from magic bytes (don't trust caller).
	detectedMT := DetectImageFormat(raw)
	if mediaType == "" {
		mediaType = detectedMT
	}
	ext := normalizeMediaType(mediaType)

	originalSize := len(raw)

	// Decode to get dimensions.
	img, _, decErr := decodeImage(raw, ext)
	if decErr != nil {
		// Graceful fallback: if already within limit, pass through unresized.
		b64Size := base64.StdEncoding.EncodedLen(originalSize)
		if b64Size <= APIImageMaxBase64Size {
			stats := ResizeStats{
				OriginalBytes: originalSize,
				NewBytes:      originalSize,
				ResizeReason:  "none",
				MediaType:     detectedMT,
			}
			return base64Data, stats, nil
		}
		return "", ResizeStats{}, &ResizeError{ErrDecodeFailure, "decode image: " + decErr.Error()}
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	needsDimResize := origW > ImageMaxWidth || origH > ImageMaxHeight
	needsSizeCompress := originalSize > ImageTargetRawSize

	if !needsDimResize && !needsSizeCompress {
		// Already fine — return original base64 as-is.
		stats := ResizeStats{
			OriginalWidth:  origW,
			OriginalHeight: origH,
			NewWidth:       origW,
			NewHeight:      origH,
			OriginalBytes:  originalSize,
			NewBytes:       originalSize,
			ResizeReason:   "none",
			MediaType:      "image/" + ext,
		}
		return base64Data, stats, nil
	}

	reason := resizeReason(needsDimResize, needsSizeCompress)

	// Determine target pixel dimensions.
	targetW, targetH := origW, origH
	if needsDimResize {
		targetW, targetH = GetTargetDimensions(origW, origH)
	}

	// If PNG and only size issue, try PNG compression first.
	if ext == "png" && !needsDimResize {
		compressed, encErr := encodePNG(img)
		if encErr == nil && len(compressed) <= ImageTargetRawSize {
			b64 := base64.StdEncoding.EncodeToString(compressed)
			stats := ResizeStats{
				OriginalWidth:  origW,
				OriginalHeight: origH,
				NewWidth:       origW,
				NewHeight:      origH,
				OriginalBytes:  originalSize,
				NewBytes:       len(compressed),
				ResizeReason:   reason,
				MediaType:      "image/png",
			}
			return b64, stats, nil
		}
	}

	// Try JPEG at quality steps (with resize if needed).
	resizedImg := img
	if needsDimResize {
		resizedImg = scaleImage(img, targetW, targetH)
	}

	for _, q := range qualitySteps {
		compressed, encErr := encodeJPEG(resizedImg, q)
		if encErr != nil {
			continue
		}
		if len(compressed) <= ImageTargetRawSize {
			b64 := base64.StdEncoding.EncodeToString(compressed)
			stats := ResizeStats{
				OriginalWidth:  origW,
				OriginalHeight: origH,
				NewWidth:       targetW,
				NewHeight:      targetH,
				OriginalBytes:  originalSize,
				NewBytes:       len(compressed),
				Quality:        q,
				ResizeReason:   reason,
				MediaType:      "image/jpeg",
			}
			return b64, stats, nil
		}
	}

	// Last resort: resize to ≤1000 wide + JPEG q=20.
	smallW := targetW
	smallH := targetH
	if smallW > 1000 {
		smallH = int(math.Round(float64(targetH) * 1000.0 / math.Max(float64(targetW), 1)))
		smallW = 1000
	}
	smallImg := scaleImage(img, smallW, smallH)
	compressed, encErr := encodeJPEG(smallImg, 20)
	if encErr != nil {
		return "", ResizeStats{}, &ResizeError{ErrEncodeFailure, "last-resort encode failed: " + encErr.Error()}
	}

	b64 := base64.StdEncoding.EncodeToString(compressed)
	stats := ResizeStats{
		OriginalWidth:  origW,
		OriginalHeight: origH,
		NewWidth:       smallW,
		NewHeight:      smallH,
		OriginalBytes:  originalSize,
		NewBytes:       len(compressed),
		Quality:        20,
		ResizeReason:   reason,
		MediaType:      "image/jpeg",
	}
	return b64, stats, nil
}

// resizeReason returns a human-readable reason string.
func resizeReason(dim, size bool) string {
	switch {
	case dim && size:
		return "both"
	case dim:
		return "dimensions"
	case size:
		return "size"
	default:
		return "none"
	}
}

// ─── Encoding helpers ─────────────────────────────────────────────────────────

// decodeImage decodes raw image bytes. Tries Go's standard decoders first,
// then falls back to ImageMagick's `convert` if available.
func decodeImage(raw []byte, ext string) (image.Image, string, error) {
	r := bytes.NewReader(raw)

	// Try standard decoders.
	switch ext {
	case "jpeg", "jpg":
		img, err := jpeg.Decode(r)
		if err == nil {
			return img, "jpeg", nil
		}
	case "png":
		img, err := png.Decode(r)
		if err == nil {
			return img, "png", nil
		}
	case "gif":
		// Only the first frame is used by resizing. DecodeAll would allocate
		// every animation frame from an untrusted attachment before limits apply.
		img, err := gif.Decode(r)
		if err == nil {
			return img, "gif", nil
		}
		return nil, "", fmt.Errorf("could not decode GIF: %w", err)
	case "webp":
		img, err := webp.Decode(r)
		if err == nil {
			return img, "webp", nil
		}
	}

	// Generic decode (image.Decode dispatches on magic bytes after registration).
	r.Seek(0, 0)
	img, format, err := image.Decode(r)
	if err == nil {
		return img, format, nil
	}

	// ImageMagick fallback: convert to PNG then decode.
	if converted, magickErr := imageMagickConvertToPNG(raw); magickErr == nil {
		img, pngErr := png.Decode(bytes.NewReader(converted))
		if pngErr == nil {
			return img, "png", nil
		}
	}

	return nil, "", fmt.Errorf("could not decode image (%s): %w", ext, err)
}

// encodeJPEG encodes an image as JPEG at the given quality (1-100).
func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	// JPEG doesn't support alpha; flatten onto white background.
	flat := flattenAlpha(img)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodePNG encodes an image as PNG with best compression.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scaleImage scales img to fit within (maxW × maxH) preserving aspect ratio.
// Uses nearest-neighbour scaling (fast; acceptable for compression purposes).
func scaleImage(img image.Image, newW, newH int) image.Image {
	if newW <= 0 || newH <= 0 {
		return img
	}
	src := img.Bounds()
	srcW := src.Dx()
	srcH := src.Dy()
	if srcW == newW && srcH == newH {
		return img
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	scaleX := float64(srcW) / float64(newW)
	scaleY := float64(srcH) / float64(newH)

	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := int(math.Floor(float64(x) * scaleX))
			srcY := int(math.Floor(float64(y) * scaleY))
			if srcX >= srcW {
				srcX = srcW - 1
			}
			if srcY >= srcH {
				srcY = srcH - 1
			}
			dst.Set(x, y, img.At(src.Min.X+srcX, src.Min.Y+srcY))
		}
	}
	return dst
}

// flattenAlpha composites img onto a white background, producing an opaque image
// suitable for JPEG encoding.
func flattenAlpha(img image.Image) image.Image {
	b := img.Bounds()
	flat := image.NewRGBA(b)
	// Fill with white.
	draw.Draw(flat, b, &image.Uniform{color.White}, image.Point{}, draw.Src)
	// Draw original image over it (alpha blending).
	draw.Draw(flat, b, img, b.Min, draw.Over)
	return flat
}

// imageMagickConvertToPNG calls `convert` (ImageMagick) to convert raw image
// bytes into a PNG. Returns an error if ImageMagick is not installed.
func imageMagickConvertToPNG(raw []byte) ([]byte, error) {
	convertPath, err := exec.LookPath("convert")
	if err != nil {
		return nil, fmt.Errorf("ImageMagick convert not found: %w", err)
	}
	// Pipe stdin → convert → stdout
	cmd := exec.Command(convertPath, "-", "png:-")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ImageMagick convert failed: %w", err)
	}
	return out, nil
}

// ─── Image metadata text ─────────────────────────────────────────────────────

// ImageDimensions holds optional original/display size information.
type ImageDimensions struct {
	OriginalWidth  int
	OriginalHeight int
	DisplayWidth   int
	DisplayHeight  int
}

// CreateImageMetadataText generates a bracket annotation like:
//
//	[Image: source: /path/to/img, original 1920x1080, displayed at 960x540. Multiply coordinates by 2.00 …]
//
// Returns "" when there is no useful info to convey.
func CreateImageMetadataText(dims ImageDimensions, sourcePath string) string {
	origW, origH := dims.OriginalWidth, dims.OriginalHeight
	dispW, dispH := dims.DisplayWidth, dims.DisplayHeight

	if origW <= 0 || origH <= 0 || dispW <= 0 || dispH <= 0 {
		if sourcePath != "" {
			return fmt.Sprintf("[Image source: %s]", sourcePath)
		}
		return ""
	}

	wasResized := origW != dispW || origH != dispH
	if !wasResized && sourcePath == "" {
		return ""
	}

	var parts []string
	if sourcePath != "" {
		parts = append(parts, "source: "+sourcePath)
	}
	if wasResized {
		scale := float64(origW) / float64(dispW)
		parts = append(parts, fmt.Sprintf(
			"original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.",
			origW, origH, dispW, dispH, scale,
		))
	}
	return "[Image: " + strings.Join(parts, ", ") + "]"
}

// ─── Token-limit compression ─────────────────────────────────────────────────

// CompressImageToTokenLimit compresses a raw image buffer to fit within maxTokens.
// Uses the formula: maxBytes = floor(maxTokens / 0.125) * 0.75  (from TS source).
func CompressImageToTokenLimit(raw []byte, maxTokens int, mediaType string) ([]byte, string, error) {
	maxBase64Chars := int(float64(maxTokens) / 0.125)
	maxBytes := int(float64(maxBase64Chars) * 0.75)
	return CompressImageToByteLimit(raw, maxBytes, mediaType)
}

// CompressImageToByteLimit compresses a raw image buffer to fit within maxBytes.
// Returns the compressed bytes, the resulting media type, and any error.
func CompressImageToByteLimit(raw []byte, maxBytes int, mediaType string) ([]byte, string, error) {
	if len(raw) <= maxBytes {
		return raw, mediaType, nil
	}

	ext := normalizeMediaType(mediaType)
	img, _, err := decodeImage(raw, ext)
	if err != nil {
		if len(raw) <= maxBytes {
			return raw, mediaType, nil
		}
		return nil, "", &ResizeError{ErrDecodeFailure, err.Error()}
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Progressive scaling: 100%, 75%, 50%, 25%
	scales := []float64{1.0, 0.75, 0.5, 0.25}
	for _, s := range scales {
		nw := int(math.Round(float64(w) * s))
		nh := int(math.Round(float64(h) * s))
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		scaled := scaleImage(img, nw, nh)

		// Try original format first (PNG preservation).
		if ext == "png" {
			compressed, err := encodePNG(scaled)
			if err == nil && len(compressed) <= maxBytes {
				return compressed, "image/png", nil
			}
		}

		// JPEG at q=80.
		compressed, err := encodeJPEG(scaled, 80)
		if err == nil && len(compressed) <= maxBytes {
			return compressed, "image/jpeg", nil
		}
	}

	// Palette PNG at 800×800.
	small := scaleImage(img, 800, 800)
	if compressed, err := encodePNG(small); err == nil && len(compressed) <= maxBytes {
		return compressed, "image/png", nil
	}

	// JPEG at moderate quality.
	medium := scaleImage(img, 600, 600)
	if compressed, err := encodeJPEG(medium, 50); err == nil && len(compressed) <= maxBytes {
		return compressed, "image/jpeg", nil
	}

	// Ultra-compressed JPEG.
	tiny := scaleImage(img, 400, 400)
	compressed, err := encodeJPEG(tiny, 20)
	if err != nil {
		return nil, "", &ResizeError{ErrEncodeFailure, "ultra-compress failed: " + err.Error()}
	}
	return compressed, "image/jpeg", nil
}
