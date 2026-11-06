// Package attachments handles file attachments for AI conversations —
// images, PDFs, code files. It manages reading, validating, encoding,
// and lifecycle of attachments. Ported from TypeScript attachments.ts.
package attachments

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─── Supported MIME types ───────────────────────────────────────────────────

// supportedExtensions maps file extensions (without dot) to MIME types.
var supportedExtensions = map[string]string{
	// Images
	"png":  "image/png",
	"jpg":  "image/jpeg",
	"jpeg": "image/jpeg",
	"gif":  "image/gif",
	"webp": "image/webp",
	// Documents
	"pdf": "application/pdf",
	// Text / code
	"txt":  "text/plain",
	"md":   "text/markdown",
	"go":   "text/x-go",
	"py":   "text/x-python",
	"js":   "text/javascript",
	"ts":   "text/typescript",
	"tsx":  "text/typescript",
	"jsx":  "text/javascript",
	"rs":   "text/x-rust",
	"c":    "text/x-c",
	"cpp":  "text/x-c++",
	"h":    "text/x-c",
	"hpp":  "text/x-c++",
	"java": "text/x-java",
	"rb":   "text/x-ruby",
	"sh":   "text/x-shellscript",
	"bash": "text/x-shellscript",
	"zsh":  "text/x-shellscript",
	"yaml": "text/yaml",
	"yml":  "text/yaml",
	"json": "application/json",
	"toml": "text/toml",
	"xml":  "text/xml",
	"html": "text/html",
	"css":  "text/css",
	"sql":  "text/x-sql",
	"lua":  "text/x-lua",
	"r":    "text/x-r",
	"csv":  "text/csv",
	"log":  "text/plain",
	"ini":  "text/plain",
	"cfg":  "text/plain",
	"conf": "text/plain",
	"env":  "text/plain",
}

// imageTypes lists MIME types treated as images.
var imageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// ─── Attachment ─────────────────────────────────────────────────────────────

// Attachment represents a single file attachment.
type Attachment struct {
	ID       string // unique identifier
	Path     string // absolute file path (empty for clipboard-sourced)
	Name     string // display name (basename or "clipboard-image")
	MIMEType string // detected MIME type
	Size     int64  // raw byte length
	Content  []byte // raw file content
	Base64   string // base64-encoded content (lazily populated)
	IsImage  bool   // true if MIMEType starts with "image/"
	IsPDF    bool   // true if MIMEType is "application/pdf"
}

// ─── AttachmentManager ──────────────────────────────────────────────────────

// AttachmentManager manages a collection of file attachments with
// thread-safe operations, size validation, and MIME detection.
type AttachmentManager struct {
	mu      sync.Mutex
	items   map[string]*Attachment // keyed by ID
	order   []string               // insertion order of IDs
	maxSize int64                  // maximum allowed file size in bytes
	logger  *slog.Logger
}

// NewAttachmentManager creates an AttachmentManager.
// maxSize is the maximum file size in bytes (0 = no limit).
func NewAttachmentManager(maxSize int64, logger *slog.Logger) *AttachmentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AttachmentManager{
		items:   make(map[string]*Attachment),
		maxSize: maxSize,
		logger:  logger,
	}
}

// Add reads a file from disk, detects its MIME type, validates size,
// and adds it to the managed collection. Returns the new Attachment or an error.
func (m *AttachmentManager) Add(path string) (*Attachment, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", path, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", absPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not a file", absPath)
	}
	if m.maxSize > 0 && info.Size() > m.maxSize {
		return nil, fmt.Errorf(
			"file %q is too large (%d bytes, max %d)",
			absPath, info.Size(), m.maxSize,
		)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", absPath, err)
	}

	mime := DetectMIME(absPath)
	// Refine with magic bytes for binary files.
	if strings.HasPrefix(mime, "image/") || mime == "application/octet-stream" {
		if detected := detectFromMagicBytes(data); detected != "" {
			mime = detected
		}
	}

	att := &Attachment{
		ID:       uuid.New().String(),
		Path:     absPath,
		Name:     filepath.Base(absPath),
		MIMEType: mime,
		Size:     info.Size(),
		Content:  data,
		IsImage:  imageTypes[mime],
		IsPDF:    mime == "application/pdf",
	}

	m.mu.Lock()
	m.items[att.ID] = att
	m.order = append(m.order, att.ID)
	m.mu.Unlock()

	m.logger.Info("attachment added",
		"id", att.ID,
		"name", att.Name,
		"mime", att.MIMEType,
		"size", att.Size,
	)
	return att, nil
}

// AddFromClipboard reads an image from the system clipboard and adds it
// as an attachment. On Linux it uses xclip; on macOS it uses osascript/pngpaste.
// Returns an error if no image is found or clipboard tools are unavailable.
func (m *AttachmentManager) AddFromClipboard() (*Attachment, error) {
	data, err := readImageFromClipboard()
	if err != nil {
		return nil, fmt.Errorf("clipboard image: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("clipboard contains no image data")
	}

	mime := "image/png"
	if detected := detectFromMagicBytes(data); detected != "" {
		mime = detected
	}

	att := &Attachment{
		ID:       uuid.New().String(),
		Path:     "",
		Name:     "clipboard-image",
		MIMEType: mime,
		Size:     int64(len(data)),
		Content:  data,
		IsImage:  true,
		IsPDF:    false,
	}

	m.mu.Lock()
	m.items[att.ID] = att
	m.order = append(m.order, att.ID)
	m.mu.Unlock()

	m.logger.Info("attachment added from clipboard",
		"id", att.ID,
		"mime", att.MIMEType,
		"size", att.Size,
	)
	return att, nil
}

// Remove deletes an attachment by ID.
func (m *AttachmentManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.items[id]; !ok {
		return
	}
	delete(m.items, id)

	// Remove from order slice.
	for i, oid := range m.order {
		if oid == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

// List returns all attachments in insertion order.
func (m *AttachmentManager) List() []*Attachment {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*Attachment, 0, len(m.order))
	for _, id := range m.order {
		if att, ok := m.items[id]; ok {
			result = append(result, att)
		}
	}
	return result
}

// Clear removes all attachments.
func (m *AttachmentManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items = make(map[string]*Attachment)
	m.order = nil
}

// ToBase64 returns the base64-encoded content for an attachment.
// The result is cached on the Attachment struct for subsequent calls.
func (m *AttachmentManager) ToBase64(att *Attachment) string {
	if att == nil {
		return ""
	}
	if att.Base64 != "" {
		return att.Base64
	}
	att.Base64 = base64.StdEncoding.EncodeToString(att.Content)
	return att.Base64
}

// ─── MIME detection ─────────────────────────────────────────────────────────

// DetectMIME determines the MIME type for a file path. It first checks the
// file extension against the known map, then falls back to magic-byte
// sniffing via http.DetectContentType if the extension is unrecognised.
func DetectMIME(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if mime, ok := supportedExtensions[ext]; ok {
		return mime
	}

	// Try reading a small header for sniffing.
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(buf[:n])
}

// detectFromMagicBytes detects the MIME type from magic bytes of raw data.
// Returns "" if the format cannot be identified as a known type.
func detectFromMagicBytes(data []byte) string {
	if len(data) < 4 {
		return ""
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
	// PDF: %PDF
	if data[0] == 0x25 && data[1] == 0x50 && data[2] == 0x44 && data[3] == 0x46 {
		return "application/pdf"
	}
	return ""
}

// SupportedTypes returns the list of supported file extensions.
func SupportedTypes() []string {
	seen := make(map[string]bool)
	var types []string
	for ext := range supportedExtensions {
		if !seen[ext] {
			seen[ext] = true
			types = append(types, ext)
		}
	}
	// Sort for deterministic output.
	sortStrings(types)
	return types
}

// sortStrings is a simple insertion sort for small slices (avoids importing sort).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}

// ─── Clipboard image reading ────────────────────────────────────────────────

// readImageFromClipboard reads image data from the system clipboard.
func readImageFromClipboard() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardMacOS()
	case "linux":
		return readClipboardLinux()
	case "windows":
		return readClipboardWindows()
	default:
		return nil, fmt.Errorf("clipboard image not supported on %s", runtime.GOOS)
	}
}

// readClipboardLinux tries xclip, then wl-paste to read an image from clipboard.
func readClipboardLinux() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try xclip first.
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}

	// Try wl-paste (Wayland).
	if _, err := exec.LookPath("wl-paste"); err == nil {
		cmd := exec.CommandContext(ctx, "wl-paste", "--type", "image/png")
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			return out, nil
		}
	}

	return nil, fmt.Errorf("no clipboard image found (install xclip or wl-paste)")
}

// readClipboardMacOS uses pngpaste or osascript to read an image from clipboard.
func readClipboardMacOS() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try pngpaste (brew install pngpaste).
	if _, err := exec.LookPath("pngpaste"); err == nil {
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod-clip-%s.png", uuid.New().String()))
		defer os.Remove(tmpFile)

		cmd := exec.CommandContext(ctx, "pngpaste", tmpFile)
		if err := cmd.Run(); err == nil {
			data, err := os.ReadFile(tmpFile)
			if err == nil && len(data) > 0 {
				return data, nil
			}
		}
	}

	// Fallback: osascript to write clipboard TIFF → convert via sips.
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod-clip-%s.png", uuid.New().String()))
	defer os.Remove(tmpFile)

	tiffFile := tmpFile + ".tiff"
	defer os.Remove(tiffFile)

	script := fmt.Sprintf(
		`set tiffData to the clipboard as «class TIFF»
set fp to open for access POSIX file %q with write permission
write tiffData to fp
close access fp`,
		tiffFile,
	)
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("osascript clipboard read failed: %w", err)
	}

	// Convert TIFF to PNG via sips.
	sipsCmd := exec.CommandContext(ctx, "sips", "-s", "format", "png", tiffFile, "--out", tmpFile)
	if err := sipsCmd.Run(); err != nil {
		return nil, fmt.Errorf("sips conversion failed: %w", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("read converted image: %w", err)
	}
	return data, nil
}

// readClipboardWindows uses PowerShell to read an image from clipboard.
func readClipboardWindows() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod-clip-%s.png", uuid.New().String()))
	defer os.Remove(tmpFile)

	psScript := fmt.Sprintf(
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$img = [System.Windows.Forms.Clipboard]::GetImage(); `+
			`if ($img -ne $null) { $img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png) } `+
			`else { exit 1 }`,
		strings.ReplaceAll(tmpFile, "'", "''"),
	)

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", psScript)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("powershell clipboard read failed: %w", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("read clipboard image: %w", err)
	}
	return data, nil
}
