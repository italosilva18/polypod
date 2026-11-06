// Package computeruse provides screen interaction capabilities including
// screenshots, mouse/keyboard simulation, and display management.
//
// Platform support:
//   - Linux: xdotool (X11), ydotool (Wayland), scrot/gnome-screenshot
//   - macOS: screencapture, cliclick/AppleScript
//   - Windows: PowerShell with .NET System.Windows.Forms
//
// Ported from Claude Code's TypeScript computerUse/ utilities.
package computeruse

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ─── Platform detection ────────────────────────────────────────────────

// DisplayServer identifies the display server technology.
type DisplayServer string

const (
	DisplayX11     DisplayServer = "x11"
	DisplayWayland DisplayServer = "wayland"
	DisplayMacOS   DisplayServer = "macos"
	DisplayWindows DisplayServer = "windows"
	DisplayUnknown DisplayServer = "unknown"
)

// DetectDisplayServer returns the current display server technology.
func DetectDisplayServer() DisplayServer {
	switch runtime.GOOS {
	case "darwin":
		return DisplayMacOS
	case "windows":
		return DisplayWindows
	case "linux":
		// Check for Wayland first.
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return DisplayWayland
		}
		if os.Getenv("DISPLAY") != "" {
			return DisplayX11
		}
		// Check XDG_SESSION_TYPE as fallback.
		switch os.Getenv("XDG_SESSION_TYPE") {
		case "wayland":
			return DisplayWayland
		case "x11":
			return DisplayX11
		}
		return DisplayUnknown
	default:
		return DisplayUnknown
	}
}

// ─── Screen geometry ───────────────────────────────────────────────────

// DisplayGeometry describes a display's dimensions and position.
type DisplayGeometry struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	ScaleFactor float64 `json:"scaleFactor"`
	DisplayID   int     `json:"displayId"`
	OriginX     int     `json:"originX"`
	OriginY     int     `json:"originY"`
}

// GetDisplaySize returns the primary display's dimensions.
func GetDisplaySize() (*DisplayGeometry, error) {
	switch DetectDisplayServer() {
	case DisplayX11:
		return getDisplaySizeX11()
	case DisplayWayland:
		return getDisplaySizeWayland()
	case DisplayMacOS:
		return getDisplaySizeMacOS()
	case DisplayWindows:
		return getDisplaySizeWindows()
	default:
		return nil, fmt.Errorf("unsupported display server")
	}
}

func getDisplaySizeX11() (*DisplayGeometry, error) {
	out, err := desktopCommand("xdpyinfo").Output()
	if err != nil {
		// Fallback: xdotool getdisplaygeometry
		out2, err2 := desktopCommand("xdotool", "getdisplaygeometry").Output()
		if err2 != nil {
			return nil, fmt.Errorf("xdpyinfo and xdotool both failed: %w", err)
		}
		parts := strings.Fields(strings.TrimSpace(string(out2)))
		if len(parts) != 2 {
			return nil, fmt.Errorf("unexpected xdotool output: %s", string(out2))
		}
		w, _ := strconv.Atoi(parts[0])
		h, _ := strconv.Atoi(parts[1])
		return &DisplayGeometry{Width: w, Height: h, ScaleFactor: 1.0}, nil
	}

	// Parse xdpyinfo output for "dimensions:" line.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "dimensions:") {
			// "dimensions:    1920x1080 pixels"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				dims := strings.Split(parts[1], "x")
				if len(dims) == 2 {
					w, _ := strconv.Atoi(dims[0])
					h, _ := strconv.Atoi(dims[1])
					return &DisplayGeometry{Width: w, Height: h, ScaleFactor: 1.0}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("could not parse display dimensions from xdpyinfo")
}

func getDisplaySizeWayland() (*DisplayGeometry, error) {
	// Try wlr-randr (wlroots-based compositors).
	if out, err := desktopCommand("wlr-randr").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "current") && strings.Contains(line, "x") {
				// "1920x1080 px, 60.000000 Hz (current)"
				parts := strings.Fields(line)
				if len(parts) > 0 {
					dims := strings.Split(parts[0], "x")
					if len(dims) == 2 {
						w, _ := strconv.Atoi(dims[0])
						h, _ := strconv.Atoi(dims[1])
						return &DisplayGeometry{Width: w, Height: h, ScaleFactor: 1.0}, nil
					}
				}
			}
		}
	}

	// Fallback: xdpyinfo under XWayland.
	return getDisplaySizeX11()
}

func getDisplaySizeMacOS() (*DisplayGeometry, error) {
	script := `tell application "Finder" to get bounds of window of desktop`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		// Fallback: system_profiler
		out2, err2 := exec.Command("system_profiler", "SPDisplaysDataType").Output()
		if err2 != nil {
			return nil, fmt.Errorf("could not determine display size on macOS")
		}
		for _, line := range strings.Split(string(out2), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "Resolution:") {
				// "Resolution: 2560 x 1440 Retina"
				parts := strings.Fields(line)
				for i, p := range parts {
					if p == "x" && i > 0 && i+1 < len(parts) {
						w, _ := strconv.Atoi(parts[i-1])
						h, _ := strconv.Atoi(parts[i+1])
						scale := 1.0
						if strings.Contains(line, "Retina") {
							scale = 2.0
						}
						return &DisplayGeometry{Width: w, Height: h, ScaleFactor: scale}, nil
					}
				}
			}
		}
		return nil, fmt.Errorf("could not parse macOS display info")
	}

	// Parse "0, 0, 1920, 1080"
	parts := strings.Split(strings.TrimSpace(string(out)), ", ")
	if len(parts) == 4 {
		w, _ := strconv.Atoi(parts[2])
		h, _ := strconv.Atoi(parts[3])
		return &DisplayGeometry{Width: w, Height: h, ScaleFactor: 2.0}, nil
	}
	return nil, fmt.Errorf("could not parse Finder desktop bounds")
}

func getDisplaySizeWindows() (*DisplayGeometry, error) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
$screen = [System.Windows.Forms.Screen]::PrimaryScreen
Write-Output "$($screen.Bounds.Width) $($screen.Bounds.Height)"
`
	out, err := runPowerShell(context.Background(), script)
	if err != nil {
		return nil, fmt.Errorf("PowerShell display query failed: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		return nil, fmt.Errorf("unexpected PowerShell output: %s", out)
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return &DisplayGeometry{Width: w, Height: h, ScaleFactor: 1.0}, nil
}

// runPowerShell executes a PowerShell script and returns stdout.
func runPowerShell(ctx context.Context, script string) (string, error) {
	psPath := "pwsh"
	if _, err := exec.LookPath("pwsh"); err != nil {
		psPath = "powershell"
	}
	cmd := exec.CommandContext(ctx, psPath, "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ─── ComputerUseService ────────────────────────────────────────────────

// ComputerUseService manages screen interaction operations.
type ComputerUseService struct {
	mu            sync.Mutex
	displayServer DisplayServer
	display       *DisplayGeometry
}

// NewComputerUseService creates a new service with auto-detected platform.
func NewComputerUseService() *ComputerUseService {
	return &ComputerUseService{
		displayServer: DetectDisplayServer(),
	}
}

// GetDisplay returns (and caches) the primary display geometry.
func (s *ComputerUseService) GetDisplay() (*DisplayGeometry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.display != nil {
		return s.display, nil
	}

	d, err := GetDisplaySize()
	if err != nil {
		return nil, err
	}
	s.display = d
	return d, nil
}

// TakeScreenshot captures the screen and returns the image data.
func (s *ComputerUseService) TakeScreenshot(ctx context.Context) ([]byte, error) {
	return CaptureScreen(ctx)
}

// Click simulates a left mouse click at (x, y).
func (s *ComputerUseService) Click(ctx context.Context, x, y int) error {
	return MouseClick(ctx, x, y, "left")
}

// RightClick simulates a right mouse click at (x, y).
func (s *ComputerUseService) RightClick(ctx context.Context, x, y int) error {
	return MouseClick(ctx, x, y, "right")
}

// DoubleClick simulates a double left click at (x, y).
func (s *ComputerUseService) DoubleClick(ctx context.Context, x, y int) error {
	return MouseClickN(ctx, x, y, "left", 2)
}

// Type simulates keyboard text input.
func (s *ComputerUseService) Type(ctx context.Context, text string) error {
	return TypeText(ctx, text)
}

// MoveMouse moves the cursor to (x, y).
func (s *ComputerUseService) MoveMouse(ctx context.Context, x, y int) error {
	return MouseMove(ctx, x, y)
}

// Scroll simulates mouse scroll.
func (s *ComputerUseService) Scroll(ctx context.Context, x, y int, direction string, amount int) error {
	return MouseScroll(ctx, x, y, direction, amount)
}

// KeyPress simulates a key press.
func (s *ComputerUseService) KeyPress(ctx context.Context, key string) error {
	return KeyPressAction(ctx, key)
}

// KeyCombo simulates a key combination (e.g., "ctrl+c").
func (s *ComputerUseService) KeyCombo(ctx context.Context, keys ...string) error {
	return KeyCombination(ctx, keys...)
}

// Platform returns the detected display server.
func (s *ComputerUseService) Platform() DisplayServer {
	return s.displayServer
}
