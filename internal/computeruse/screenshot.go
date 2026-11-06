package computeruse

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	// Register decoders.
	_ "image/gif"
)

// ─── Screenshot capture ────────────────────────────────────────────────

// CaptureScreen captures the full primary screen.
func CaptureScreen(ctx context.Context) ([]byte, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod_screenshot_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	var err error
	switch runtime.GOOS {
	case "linux":
		err = captureScreenLinux(ctx, tmpFile)
	case "darwin":
		err = captureScreenMacOS(ctx, tmpFile)
	case "windows":
		err = captureScreenWindows(ctx, tmpFile)
	default:
		return nil, fmt.Errorf("screenshots not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return nil, err
	}

	return os.ReadFile(tmpFile)
}

// CaptureWindow captures a specific window by title.
func CaptureWindow(ctx context.Context, title string) ([]byte, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod_window_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	switch runtime.GOOS {
	case "linux":
		if err := captureWindowLinux(ctx, title, tmpFile); err != nil {
			return nil, err
		}
	case "darwin":
		// macOS screencapture -l requires window ID, fall back to full screen.
		if err := captureScreenMacOS(ctx, tmpFile); err != nil {
			return nil, err
		}
	case "windows":
		if err := captureScreenWindows(ctx, tmpFile); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("window capture not supported on %s", runtime.GOOS)
	}

	return os.ReadFile(tmpFile)
}

// CaptureRegion captures a rectangular region of the screen.
func CaptureRegion(ctx context.Context, x, y, w, h int) ([]byte, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("polypod_region_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	switch runtime.GOOS {
	case "linux":
		if err := captureRegionLinux(ctx, x, y, w, h, tmpFile); err != nil {
			return nil, err
		}
	case "darwin":
		if err := captureRegionMacOS(ctx, x, y, w, h, tmpFile); err != nil {
			return nil, err
		}
	case "windows":
		if err := captureRegionWindows(ctx, x, y, w, h, tmpFile); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("region capture not supported on %s", runtime.GOOS)
	}

	return os.ReadFile(tmpFile)
}

// ─── Linux screenshot tools ───────────────────────────────────────────

func captureScreenLinux(ctx context.Context, outPath string) error {
	// Try tools in order of preference.
	if hasCmd("gnome-screenshot") {
		cmd := exec.CommandContext(ctx, "gnome-screenshot", "-f", outPath)
		return cmd.Run()
	}
	if hasCmd("scrot") {
		cmd := exec.CommandContext(ctx, "scrot", outPath)
		return cmd.Run()
	}
	if hasCmd("maim") {
		cmd := exec.CommandContext(ctx, "maim", outPath)
		return cmd.Run()
	}
	if hasCmd("import") { // ImageMagick
		cmd := exec.CommandContext(ctx, "import", "-window", "root", outPath)
		return cmd.Run()
	}
	if hasCmd("grim") { // Wayland
		cmd := exec.CommandContext(ctx, "grim", outPath)
		return cmd.Run()
	}
	return fmt.Errorf("no screenshot tool found (install scrot, maim, gnome-screenshot, or grim)")
}

func captureWindowLinux(ctx context.Context, title, outPath string) error {
	// Get window ID from title.
	if hasCmd("xdotool") {
		idOut, err := exec.CommandContext(ctx, "xdotool", "search", "--name", title).Output()
		if err != nil {
			return fmt.Errorf("window '%s' not found: %w", title, err)
		}
		windowIDs := strings.Fields(strings.TrimSpace(string(idOut)))
		if len(windowIDs) == 0 {
			return fmt.Errorf("no window found with title '%s'", title)
		}
		windowID := windowIDs[0]

		if hasCmd("import") {
			cmd := exec.CommandContext(ctx, "import", "-window", windowID, outPath)
			return cmd.Run()
		}
		if hasCmd("scrot") {
			// scrot -u captures focused window; we can't target by ID directly.
			// Focus the window first.
			_ = exec.CommandContext(ctx, "xdotool", "windowactivate", windowID).Run()
			time.Sleep(200 * time.Millisecond)
			cmd := exec.CommandContext(ctx, "scrot", "-u", outPath)
			return cmd.Run()
		}
	}
	// Fallback: capture entire screen.
	return captureScreenLinux(ctx, outPath)
}

func captureRegionLinux(ctx context.Context, x, y, w, h int, outPath string) error {
	geometry := fmt.Sprintf("%dx%d+%d+%d", w, h, x, y)

	if hasCmd("maim") {
		cmd := exec.CommandContext(ctx, "maim", "-g", geometry, outPath)
		return cmd.Run()
	}
	if hasCmd("import") {
		cmd := exec.CommandContext(ctx, "import", "-window", "root", "-crop", geometry, outPath)
		return cmd.Run()
	}
	if hasCmd("scrot") {
		cmd := exec.CommandContext(ctx, "scrot", "-a",
			fmt.Sprintf("%d,%d,%d,%d", x, y, w, h), outPath)
		return cmd.Run()
	}
	if hasCmd("grim") {
		cmd := exec.CommandContext(ctx, "grim", "-g",
			fmt.Sprintf("%d,%d %dx%d", x, y, w, h), outPath)
		return cmd.Run()
	}
	return fmt.Errorf("no region capture tool found")
}

// ─── macOS screenshot tools ───────────────────────────────────────────

func captureScreenMacOS(ctx context.Context, outPath string) error {
	cmd := exec.CommandContext(ctx, "screencapture", "-x", outPath)
	return cmd.Run()
}

func captureRegionMacOS(ctx context.Context, x, y, w, h int, outPath string) error {
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-R",
		fmt.Sprintf("%d,%d,%d,%d", x, y, w, h), outPath)
	return cmd.Run()
}

// ─── Windows screenshot tools ─────────────────────────────────────────

func captureScreenWindows(ctx context.Context, outPath string) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$screen = [System.Windows.Forms.Screen]::PrimaryScreen
$bitmap = New-Object System.Drawing.Bitmap($screen.Bounds.Width, $screen.Bounds.Height)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen($screen.Bounds.Location, [System.Drawing.Point]::Empty, $screen.Bounds.Size)
$bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$graphics.Dispose()
$bitmap.Dispose()
`, strings.ReplaceAll(outPath, "'", "''"))

	_, err := runPowerShell(ctx, script)
	return err
}

func captureRegionWindows(ctx context.Context, x, y, w, h int, outPath string) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Drawing
$bitmap = New-Object System.Drawing.Bitmap(%d, %d)
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen(%d, %d, 0, 0, (New-Object System.Drawing.Size(%d, %d)))
$bitmap.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)
$graphics.Dispose()
$bitmap.Dispose()
`, w, h, x, y, w, h, strings.ReplaceAll(outPath, "'", "''"))

	_, err := runPowerShell(ctx, script)
	return err
}

// ─── Image processing ─────────────────────────────────────────────────

// ImageResize resizes image data (PNG or JPEG) to fit within maxW x maxH,
// preserving aspect ratio. Returns the resized image as PNG bytes.
func ImageResize(imgData []byte, maxW, maxH int) ([]byte, error) {
	tmpIn := filepath.Join(os.TempDir(), fmt.Sprintf("polypod_resize_in_%d.png", time.Now().UnixNano()))
	tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("polypod_resize_out_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpIn)
	defer os.Remove(tmpOut)

	if err := os.WriteFile(tmpIn, imgData, 0644); err != nil {
		return nil, err
	}

	// Use ImageMagick convert if available.
	if hasCmd("convert") {
		cmd := exec.Command("convert", tmpIn,
			"-resize", fmt.Sprintf("%dx%d>", maxW, maxH),
			tmpOut)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ImageMagick resize failed: %w", err)
		}
		return os.ReadFile(tmpOut)
	}

	// Use ffmpeg as fallback.
	if hasCmd("ffmpeg") {
		cmd := exec.Command("ffmpeg", "-i", tmpIn,
			"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxW, maxH),
			"-y", tmpOut)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ffmpeg resize failed: %w", err)
		}
		return os.ReadFile(tmpOut)
	}

	// Pure Go fallback using standard library (basic nearest-neighbor).
	return resizeGoFallback(imgData, maxW, maxH)
}

// resizeGoFallback performs a simple resize using Go's image package.
func resizeGoFallback(imgData []byte, maxW, maxH int) ([]byte, error) {
	reader := strings.NewReader(string(imgData))
	img, _, err := image.Decode(reader)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Calculate scale factor.
	scaleW := float64(maxW) / float64(w)
	scaleH := float64(maxH) / float64(h)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale >= 1.0 {
		return imgData, nil // No resize needed.
	}

	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)

	// Simple nearest-neighbor resize.
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)
			if srcX >= w {
				srcX = w - 1
			}
			if srcY >= h {
				srcY = h - 1
			}
			dst.Set(x, y, img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY))
		}
	}

	var buf strings.Builder
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode resized image: %w", err)
	}
	return []byte(buf.String()), nil
}

// ScreenshotToBase64 captures screen and returns base64-encoded PNG.
func ScreenshotToBase64(ctx context.Context) (string, error) {
	data, err := CaptureScreen(ctx)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// ScreenshotToBase64JPEG captures screen and returns base64-encoded JPEG.
func ScreenshotToBase64JPEG(ctx context.Context, quality int) (string, error) {
	data, err := CaptureScreen(ctx)
	if err != nil {
		return "", err
	}

	if quality <= 0 {
		quality = 75
	}

	// Decode the PNG to re-encode as JPEG.
	reader := strings.NewReader(string(data))
	img, _, err := image.Decode(reader)
	if err != nil {
		// If decode fails, return PNG base64 instead.
		return base64.StdEncoding.EncodeToString(data), nil
	}

	var buf strings.Builder
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return base64.StdEncoding.EncodeToString(data), nil
	}

	return base64.StdEncoding.EncodeToString([]byte(buf.String())), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────

func hasCmd(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// TargetImageSize computes the target dimensions for screenshots sent to
// the API, preserving aspect ratio while fitting within model limits.
func TargetImageSize(physW, physH, maxW, maxH int) (int, int) {
	if physW <= maxW && physH <= maxH {
		return physW, physH
	}
	scaleW := float64(maxW) / float64(physW)
	scaleH := float64(maxH) / float64(physH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	return int(float64(physW) * scale), int(float64(physH) * scale)
}

// FormatDimensions returns a "WxH" string.
func FormatDimensions(w, h int) string {
	return strconv.Itoa(w) + "x" + strconv.Itoa(h)
}
