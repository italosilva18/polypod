package computeruse

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ─── Input delay configuration ────────────────────────────────────────

// InputConfig controls timing for input simulation.
type InputConfig struct {
	// TypeDelay is the delay between keystrokes (milliseconds).
	TypeDelay int
	// ClickDelay is the delay after a click (milliseconds).
	ClickDelay int
	// MoveSettleDelay is the delay after moving the mouse (milliseconds).
	MoveSettleDelay int
}

// DefaultInputConfig returns human-like timing defaults.
func DefaultInputConfig() InputConfig {
	return InputConfig{
		TypeDelay:       8, // ~125 chars/sec
		ClickDelay:      50,
		MoveSettleDelay: 50,
	}
}

// ─── Mouse operations ─────────────────────────────────────────────────

// MouseClick simulates a mouse click at (x, y).
func MouseClick(ctx context.Context, x, y int, button string) error {
	return MouseClickN(ctx, x, y, button, 1)
}

// MouseClickN simulates N mouse clicks at (x, y).
func MouseClickN(ctx context.Context, x, y int, button string, count int) error {
	switch runtime.GOOS {
	case "linux":
		return mouseClickLinux(ctx, x, y, button, count)
	case "darwin":
		return mouseClickMacOS(ctx, x, y, button, count)
	case "windows":
		return mouseClickWindows(ctx, x, y, button, count)
	default:
		return fmt.Errorf("mouse click not supported on %s", runtime.GOOS)
	}
}

func mouseClickLinux(ctx context.Context, x, y int, button string, count int) error {
	// Move to position first.
	if err := MouseMove(ctx, x, y); err != nil {
		return err
	}
	time.Sleep(time.Duration(DefaultInputConfig().MoveSettleDelay) * time.Millisecond)

	btn := "1" // left
	switch button {
	case "right":
		btn = "3"
	case "middle":
		btn = "2"
	}

	if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
		args := []string{"click"}
		if count > 1 {
			args = append(args, "--repeat", strconv.Itoa(count))
			args = append(args, "--delay", "50")
		}
		args = append(args, btn)
		return desktopCommandContext(ctx, "xdotool", args...).Run()
	}

	if hasCmd("ydotool") {
		// ydotool >= 1.0: 0xC0 combines button 0 with press and release.
		code := "0xC0"
		switch button {
		case "right":
			code = "0xC1"
		case "middle":
			code = "0xC2"
		}
		for i := 0; i < count; i++ {
			if err := desktopCommandContext(ctx, "ydotool", "click", code).Run(); err != nil {
				return err
			}
			if i < count-1 {
				time.Sleep(50 * time.Millisecond)
			}
		}
		return nil
	}

	return fmt.Errorf("no input tool found (install xdotool or ydotool)")
}

func mouseClickMacOS(ctx context.Context, x, y int, button string, count int) error {
	if hasCmd("cliclick") {
		action := "c"
		switch button {
		case "right":
			action = "rc"
		case "middle":
			action = "mc" // cliclick doesn't have middle, falls back
		}
		coord := fmt.Sprintf("%s:%d,%d", action, x, y)
		for i := 0; i < count; i++ {
			if err := exec.CommandContext(ctx, "cliclick", coord).Run(); err != nil {
				return err
			}
		}
		return nil
	}

	// AppleScript fallback — simulates only left click.
	script := fmt.Sprintf(`
tell application "System Events"
    click at {%d, %d}
end tell`, x, y)
	for i := 0; i < count; i++ {
		if err := exec.CommandContext(ctx, "osascript", "-e", script).Run(); err != nil {
			return err
		}
	}
	return nil
}

func mouseClickWindows(ctx context.Context, x, y int, button string, count int) error {
	btn := "Left"
	switch button {
	case "right":
		btn = "Right"
	case "middle":
		btn = "Middle"
	}

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(%d, %d)
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class MouseOps {
    [DllImport("user32.dll")]
    public static extern void mouse_event(int flags, int dx, int dy, int data, int info);
    public const int LEFT_DOWN = 0x02;
    public const int LEFT_UP = 0x04;
    public const int RIGHT_DOWN = 0x08;
    public const int RIGHT_UP = 0x10;
    public const int MIDDLE_DOWN = 0x20;
    public const int MIDDLE_UP = 0x40;
}
'@
`, x, y)

	downFlag := "LEFT_DOWN"
	upFlag := "LEFT_UP"
	switch btn {
	case "Right":
		downFlag = "RIGHT_DOWN"
		upFlag = "RIGHT_UP"
	case "Middle":
		downFlag = "MIDDLE_DOWN"
		upFlag = "MIDDLE_UP"
	}

	for i := 0; i < count; i++ {
		script += fmt.Sprintf("[MouseOps]::mouse_event([MouseOps]::%s, 0, 0, 0, 0)\n", downFlag)
		script += fmt.Sprintf("[MouseOps]::mouse_event([MouseOps]::%s, 0, 0, 0, 0)\n", upFlag)
		if i < count-1 {
			script += "Start-Sleep -Milliseconds 50\n"
		}
	}

	_, err := runPowerShell(ctx, script)
	return err
}

// MouseMove moves the cursor to (x, y).
func MouseMove(ctx context.Context, x, y int) error {
	switch runtime.GOOS {
	case "linux":
		if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
			return desktopCommandContext(ctx, "xdotool", "mousemove",
				strconv.Itoa(x), strconv.Itoa(y)).Run()
		}
		if hasCmd("ydotool") {
			return desktopCommandContext(ctx, "ydotool", "mousemove", "--absolute",
				"-x", strconv.Itoa(x), "-y", strconv.Itoa(y)).Run()
		}
		return fmt.Errorf("no input tool found (install xdotool or ydotool)")

	case "darwin":
		if hasCmd("cliclick") {
			return exec.CommandContext(ctx, "cliclick",
				fmt.Sprintf("m:%d,%d", x, y)).Run()
		}
		script := fmt.Sprintf(`
tell application "System Events"
    key code 0 using {} -- wake up
end tell
do shell script "python3 -c 'import Quartz; Quartz.CGEventPost(0, Quartz.CGEventCreateMouseEvent(None, Quartz.kCGEventMouseMoved, (%d, %d), 0))'"`, x, y)
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()

	case "windows":
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(%d, %d)
`, x, y)
		_, err := runPowerShell(ctx, script)
		return err

	default:
		return fmt.Errorf("mouse move not supported on %s", runtime.GOOS)
	}
}

// MouseScroll simulates scrolling at position (x, y).
func MouseScroll(ctx context.Context, x, y int, direction string, amount int) error {
	if err := MouseMove(ctx, x, y); err != nil {
		return err
	}
	time.Sleep(time.Duration(DefaultInputConfig().MoveSettleDelay) * time.Millisecond)

	switch runtime.GOOS {
	case "linux":
		return mouseScrollLinux(ctx, direction, amount)
	case "darwin":
		return mouseScrollMacOS(ctx, direction, amount)
	case "windows":
		return mouseScrollWindows(ctx, direction, amount)
	default:
		return fmt.Errorf("scroll not supported on %s", runtime.GOOS)
	}
}

func mouseScrollLinux(ctx context.Context, direction string, amount int) error {
	if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
		// xdotool: button 4=up, 5=down, 6=left, 7=right
		btn := "5" // down
		switch direction {
		case "up":
			btn = "4"
		case "left":
			btn = "6"
		case "right":
			btn = "7"
		}
		args := []string{"click", "--repeat", strconv.Itoa(amount), btn}
		return desktopCommandContext(ctx, "xdotool", args...).Run()
	}
	if hasCmd("ydotool") {
		x, y := 0, 0
		switch direction {
		case "up":
			y = amount
		case "down":
			y = -amount
		case "left":
			x = -amount
		case "right":
			x = amount
		default:
			return fmt.Errorf("direcao de rolagem invalida: %s", direction)
		}
		return desktopCommandContext(ctx, "ydotool", "mousemove", "--wheel", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y)).Run()
	}
	return fmt.Errorf("instale ydotool >= 1.0 para rolagem Wayland")
}

func mouseScrollMacOS(ctx context.Context, direction string, amount int) error {
	delta := -amount // down = negative
	switch direction {
	case "up":
		delta = amount
	}
	if hasCmd("cliclick") {
		// cliclick doesn't support scroll directly.
		// Fall back to AppleScript.
	}
	script := fmt.Sprintf(`
do shell script "python3 -c 'import Quartz; e = Quartz.CGEventCreateScrollWheelEvent(None, 0, 1, %d); Quartz.CGEventPost(0, e)'"`, delta)
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

func mouseScrollWindows(ctx context.Context, direction string, amount int) error {
	delta := -120 * amount // down
	switch direction {
	case "up":
		delta = 120 * amount
	}
	script := fmt.Sprintf(`
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class MouseOps {
    [DllImport("user32.dll")]
    public static extern void mouse_event(int flags, int dx, int dy, int data, int info);
    public const int WHEEL = 0x0800;
}
'@
[MouseOps]::mouse_event([MouseOps]::WHEEL, 0, 0, %d, 0)
`, delta)
	_, err := runPowerShell(ctx, script)
	return err
}

// ─── Keyboard operations ──────────────────────────────────────────────

// KeyPressAction simulates pressing and releasing a key.
func KeyPressAction(ctx context.Context, key string) error {
	switch runtime.GOOS {
	case "linux":
		if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
			return desktopCommandContext(ctx, "xdotool", "key", key).Run()
		}
		if hasCmd("ydotool") {
			return keyPressYdotool(ctx, key)
		}
		return fmt.Errorf("no input tool found")
	case "darwin":
		return keyPressMacOS(ctx, key)
	case "windows":
		return keyPressWindows(ctx, key)
	default:
		return fmt.Errorf("key press not supported on %s", runtime.GOOS)
	}
}

// KeyCombination simulates pressing multiple keys simultaneously (e.g., ctrl, c).
func KeyCombination(ctx context.Context, keys ...string) error {
	combo := strings.Join(keys, "+")

	switch runtime.GOOS {
	case "linux":
		if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
			return desktopCommandContext(ctx, "xdotool", "key", combo).Run()
		}
		if hasCmd("ydotool") {
			return keyPressYdotool(ctx, combo)
		}
		return fmt.Errorf("instale ydotool >= 1.0 para entrada Wayland")
	case "darwin":
		return keyPressMacOS(ctx, combo)
	case "windows":
		return keyPressWindows(ctx, combo)
	default:
		return fmt.Errorf("key combo not supported on %s", runtime.GOOS)
	}
}

func keyPressMacOS(ctx context.Context, key string) error {
	// Map common key names to AppleScript key codes.
	// This is simplified; a full implementation would have a complete mapping.
	script := fmt.Sprintf(`
tell application "System Events"
    keystroke "%s"
end tell`, key)

	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		keystroke := parts[len(parts)-1]
		var modifiers []string
		for _, p := range parts[:len(parts)-1] {
			switch strings.ToLower(p) {
			case "command", "cmd", "super":
				modifiers = append(modifiers, "command down")
			case "control", "ctrl":
				modifiers = append(modifiers, "control down")
			case "alt", "option":
				modifiers = append(modifiers, "option down")
			case "shift":
				modifiers = append(modifiers, "shift down")
			}
		}
		if len(modifiers) > 0 {
			script = fmt.Sprintf(`
tell application "System Events"
    keystroke "%s" using {%s}
end tell`, keystroke, strings.Join(modifiers, ", "))
		}
	}

	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

func keyPressWindows(ctx context.Context, key string) error {
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait('%s')
`, escapeWindowsKeys(key))
	_, err := runPowerShell(ctx, script)
	return err
}

// escapeWindowsKeys converts key notation to SendKeys format.
func escapeWindowsKeys(key string) string {
	replacer := strings.NewReplacer(
		"ctrl+", "^",
		"alt+", "%%",
		"shift+", "+",
		"enter", "{ENTER}",
		"tab", "{TAB}",
		"escape", "{ESC}",
		"backspace", "{BACKSPACE}",
		"delete", "{DELETE}",
		"up", "{UP}",
		"down", "{DOWN}",
		"left", "{LEFT}",
		"right", "{RIGHT}",
		"home", "{HOME}",
		"end", "{END}",
		"pageup", "{PGUP}",
		"pagedown", "{PGDN}",
	)
	return replacer.Replace(strings.ToLower(key))
}

// TypeText simulates typing text character by character.
func TypeText(ctx context.Context, text string) error {
	switch runtime.GOOS {
	case "linux":
		return typeTextLinux(ctx, text)
	case "darwin":
		return typeTextMacOS(ctx, text)
	case "windows":
		return typeTextWindows(ctx, text)
	default:
		return fmt.Errorf("type text not supported on %s", runtime.GOOS)
	}
}

func typeTextLinux(ctx context.Context, text string) error {
	if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
		// xdotool type with delay for human-like speed.
		return desktopCommandContext(ctx, "xdotool", "type", "--delay",
			strconv.Itoa(DefaultInputConfig().TypeDelay), text).Run()
	}
	if hasCmd("ydotool") {
		// Upstream type uses an ASCII/US key table; never silently drop UTF-8.
		for _, r := range text {
			if r > 127 {
				return pasteTextWayland(ctx, text)
			}
		}
		return desktopCommandContext(ctx, "ydotool", "type", "--key-delay", strconv.Itoa(DefaultInputConfig().TypeDelay), "--escape=0", "--", text).Run()
	}
	return fmt.Errorf("no input tool found (install xdotool or ydotool)")
}

func typeTextMacOS(ctx context.Context, text string) error {
	if hasCmd("cliclick") {
		return exec.CommandContext(ctx, "cliclick", fmt.Sprintf("t:%s", text)).Run()
	}
	// AppleScript fallback.
	escaped := strings.ReplaceAll(text, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	script := fmt.Sprintf(`
tell application "System Events"
    keystroke "%s"
end tell`, escaped)
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

func typeTextWindows(ctx context.Context, text string) error {
	// Escape special SendKeys characters.
	escaped := strings.NewReplacer(
		"+", "{+}",
		"^", "{^}",
		"%", "{%%}",
		"~", "{~}",
		"(", "{(}",
		")", "{)}",
		"{", "{{}",
		"}", "{}}",
		"[", "{[}",
		"]", "{]}",
	).Replace(text)

	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.SendKeys]::SendWait('%s')
`, escaped)
	_, err := runPowerShell(ctx, script)
	return err
}

// ─── Drag operation ───────────────────────────────────────────────────

// Drag simulates dragging from one point to another.
func Drag(ctx context.Context, fromX, fromY, toX, toY int) error {
	switch runtime.GOOS {
	case "linux":
		if DetectDisplayServer() != DisplayWayland && hasCmd("xdotool") {
			if err := desktopCommandContext(ctx, "xdotool", "mousemove",
				strconv.Itoa(fromX), strconv.Itoa(fromY)).Run(); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond)
			if err := desktopCommandContext(ctx, "xdotool", "mousedown", "1").Run(); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond)
			if err := desktopCommandContext(ctx, "xdotool", "mousemove",
				strconv.Itoa(toX), strconv.Itoa(toY)).Run(); err != nil {
				_ = desktopCommandContext(ctx, "xdotool", "mouseup", "1").Run()
				return err
			}
			time.Sleep(50 * time.Millisecond)
			return desktopCommandContext(ctx, "xdotool", "mouseup", "1").Run()
		}
		if hasCmd("ydotool") {
			if err := MouseMove(ctx, fromX, fromY); err != nil {
				return err
			}
			if err := desktopCommandContext(ctx, "ydotool", "click", "0x40").Run(); err != nil {
				return err
			}
			moveErr := MouseMove(ctx, toX, toY)
			// Always release, even when the original action was cancelled.
			releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			releaseErr := desktopCommandContext(releaseCtx, "ydotool", "click", "0x80").Run()
			if moveErr != nil {
				return moveErr
			}
			return releaseErr
		}
		return fmt.Errorf("instale ydotool >= 1.0 para arrastar no Wayland")

	case "darwin":
		if hasCmd("cliclick") {
			dd := fmt.Sprintf("dd:%d,%d", fromX, fromY)
			du := fmt.Sprintf("du:%d,%d", toX, toY)
			return exec.CommandContext(ctx, "cliclick", dd, du).Run()
		}
		return fmt.Errorf("drag requires cliclick on macOS")

	case "windows":
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class MouseOps {
    [DllImport("user32.dll")]
    public static extern void mouse_event(int flags, int dx, int dy, int data, int info);
    public const int LEFT_DOWN = 0x02;
    public const int LEFT_UP = 0x04;
    public const int MOVE = 0x01;
    public const int ABSOLUTE = 0x8000;
}
'@
[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(%d, %d)
Start-Sleep -Milliseconds 50
[MouseOps]::mouse_event([MouseOps]::LEFT_DOWN, 0, 0, 0, 0)
Start-Sleep -Milliseconds 50
[System.Windows.Forms.Cursor]::Position = New-Object System.Drawing.Point(%d, %d)
Start-Sleep -Milliseconds 50
[MouseOps]::mouse_event([MouseOps]::LEFT_UP, 0, 0, 0, 0)
`, fromX, fromY, toX, toY)
		_, err := runPowerShell(ctx, script)
		return err

	default:
		return fmt.Errorf("drag not supported on %s", runtime.GOOS)
	}
}
