package messages

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// AttachmentType identifies the kind of attachment.
type AttachmentType string

const (
	AttachFile  AttachmentType = "file"
	AttachImage AttachmentType = "image"
	AttachPDF   AttachmentType = "pdf"
)

// Attachment represents a file, image, or PDF attachment in a message.
type Attachment struct {
	Type     AttachmentType `json:"type"`
	Path     string         `json:"path"`
	Content  string         `json:"content,omitempty"` // Base64 for images, text for files.
	MIMEType string         `json:"mime_type,omitempty"`
	Name     string         `json:"name,omitempty"`
	Size     int64          `json:"size,omitempty"`
}

// SupportedImageFormats lists the image media types supported by the API.
var SupportedImageFormats = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// MaxImageBase64Size is the maximum allowed base64-encoded image size (5 MB).
const MaxImageBase64Size = 5 * 1024 * 1024

// MaxImageWidth and MaxImageHeight are the maximum pixel dimensions.
const (
	MaxImageWidth  = 7632
	MaxImageHeight = 7632
)

// ProcessAttachment processes a file path and returns the appropriate attachment.
// It detects the file type and handles images, PDFs, and text files.
func ProcessAttachment(filePath string) (*Attachment, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot access file: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("path is a directory, not a file")
	}

	ext := strings.ToLower(filepath.Ext(absPath))

	// PDF handling.
	if ext == ".pdf" {
		text, err := PDFToText(absPath)
		if err != nil {
			return nil, fmt.Errorf("PDF extraction failed: %w", err)
		}
		return &Attachment{
			Type:     AttachPDF,
			Path:     absPath,
			Content:  text,
			MIMEType: "application/pdf",
			Name:     filepath.Base(absPath),
			Size:     info.Size(),
		}, nil
	}

	// Image handling.
	mimeType := mimeFromExt(ext)
	if SupportedImageFormats[mimeType] {
		content, err := ImageToBase64(absPath)
		if err != nil {
			return nil, err
		}
		return &Attachment{
			Type:     AttachImage,
			Path:     absPath,
			Content:  content,
			MIMEType: mimeType,
			Name:     filepath.Base(absPath),
			Size:     info.Size(),
		}, nil
	}

	// Text file handling.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %w", err)
	}

	return &Attachment{
		Type:     AttachFile,
		Path:     absPath,
		Content:  string(data),
		MIMEType: mimeType,
		Name:     filepath.Base(absPath),
		Size:     info.Size(),
	}, nil
}

// ImageToBase64 reads an image file and returns its base64-encoded content.
func ImageToBase64(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot read image: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	if len(encoded) > MaxImageBase64Size {
		return "", fmt.Errorf("image base64 size (%d bytes) exceeds limit (%d bytes)",
			len(encoded), MaxImageBase64Size)
	}

	return encoded, nil
}

// ValidateImage checks format, size, and basic validity of an image file.
func ValidateImage(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("cannot stat image: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	mimeType := mimeFromExt(ext)
	if !SupportedImageFormats[mimeType] {
		return fmt.Errorf("unsupported image format: %s (supported: png, jpeg, gif, webp)", ext)
	}

	// Check base64 size (base64 is ~4/3 of raw size).
	base64Size := (info.Size() * 4) / 3
	if base64Size > MaxImageBase64Size {
		return fmt.Errorf("image too large: base64 would be %d bytes (limit: %d bytes)",
			base64Size, MaxImageBase64Size)
	}

	// Validate magic bytes.
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 12)
	n, _ := f.Read(header)
	if n < 4 {
		return errors.New("file too small to be a valid image")
	}

	if !isValidImageHeader(header[:n]) {
		return errors.New("invalid image file header")
	}

	return nil
}

// DetectImageFormat returns the MIME type from magic bytes.
func DetectImageFormat(data []byte) string {
	if len(data) < 4 {
		return "image/png" // default
	}

	// PNG.
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}
	// JPEG.
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF.
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "image/gif"
	}
	// WebP.
	if data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 {
		if len(data) >= 12 && data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
			return "image/webp"
		}
	}

	return "application/octet-stream"
}

// ResizeImage decodes and proportionally resizes an image to fit inside the
// requested bounds. WebP input is emitted as PNG because x/image has no WebP
// encoder; the returned MIME type always describes the output bytes.
func ResizeImage(data []byte, maxWidth, maxHeight int) ([]byte, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("empty image data")
	}

	if maxWidth <= 0 || maxHeight <= 0 {
		return nil, "", errors.New("resize bounds must be positive")
	}
	source, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode image: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxWidth && height <= maxHeight && (len(data)*4)/3 <= MaxImageBase64Size {
		return data, DetectImageFormat(data), nil
	}
	scale := min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height))
	targetWidth := max(1, int(float64(width)*scale))
	targetHeight := max(1, int(float64(height)*scale))
	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Over, nil)

	var output bytes.Buffer
	outputMIME := "image/" + decodedFormat
	switch decodedFormat {
	case "jpeg":
		err = jpeg.Encode(&output, target, &jpeg.Options{Quality: 85})
		outputMIME = "image/jpeg"
	case "gif":
		err = gif.Encode(&output, target, nil)
		outputMIME = "image/gif"
	default:
		err = png.Encode(&output, target)
		outputMIME = "image/png"
	}
	if err != nil {
		return nil, "", fmt.Errorf("encode resized image: %w", err)
	}
	if (output.Len()*4)/3 > MaxImageBase64Size {
		return nil, "", fmt.Errorf("resized image still exceeds encoded limit: %d bytes", (output.Len()*4)/3)
	}
	return output.Bytes(), outputMIME, nil
}

// PDFToText extracts text from a PDF using the pdftotext command-line tool.
// Returns an error if pdftotext is not installed.
func PDFToText(filePath string) (string, error) {
	// Check if pdftotext is available.
	pdftotext, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("pdftotext not found: install poppler-utils (apt-get install poppler-utils or brew install poppler)")
	}

	// Run pdftotext with "-" for stdout output.
	cmd := exec.Command(pdftotext, "-layout", filePath, "-")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("pdftotext failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("pdftotext failed: %w", err)
	}

	return string(out), nil
}

// IsPDFFile checks if a file has a PDF extension.
func IsPDFFile(filePath string) bool {
	return strings.ToLower(filepath.Ext(filePath)) == ".pdf"
}

// --- helpers ---

func mimeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".ts":
		return "application/typescript"
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".rb":
		return "text/x-ruby"
	case ".rs":
		return "text/x-rust"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	default:
		return http.DetectContentType([]byte{})
	}
}

func isValidImageHeader(header []byte) bool {
	if len(header) < 4 {
		return false
	}
	// PNG
	if header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 {
		return true
	}
	// JPEG
	if header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
		return true
	}
	// GIF
	if header[0] == 0x47 && header[1] == 0x49 && header[2] == 0x46 {
		return true
	}
	// WebP
	if header[0] == 0x52 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x46 {
		if len(header) >= 12 && header[8] == 0x57 && header[9] == 0x45 && header[10] == 0x42 && header[11] == 0x50 {
			return true
		}
	}
	return false
}
