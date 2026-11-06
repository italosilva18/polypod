package computeruse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ydotool >= 1.0 accepts Linux evdev codes, not X11 keysyms.
func ydotoolKeyArgs(combo string) ([]string, error) {
	codes := map[string]int{
		"esc": 1, "escape": 1, "backspace": 14, "tab": 15, "enter": 28, "return": 28,
		"ctrl": 29, "control": 29, "ctrl_l": 29, "shift": 42, "shift_l": 42,
		"alt": 56, "alt_l": 56, "space": 57, "capslock": 58,
		"home": 102, "up": 103, "pageup": 104, "page_up": 104, "prior": 104,
		"left": 105, "right": 106, "end": 107, "down": 108, "pagedown": 109, "page_down": 109, "next": 109,
		"insert": 110, "delete": 111, "super": 125, "meta": 125, "win": 125, "super_l": 125,
		"print": 99, "printscreen": 99, "pause": 119,
		"minus": 12, "equal": 13, "bracketleft": 26, "bracketright": 27, "semicolon": 39,
		"apostrophe": 40, "grave": 41, "backslash": 43, "comma": 51, "period": 52, "slash": 53,
	}
	for i, key := range "1234567890" {
		codes[string(key)] = i + 2
	}
	for i, key := range "qwertyuiop" {
		codes[string(key)] = i + 16
	}
	for i, key := range "asdfghjkl" {
		codes[string(key)] = i + 30
	}
	for i, key := range "zxcvbnm" {
		codes[string(key)] = i + 44
	}
	for i := 1; i <= 10; i++ {
		codes[fmt.Sprintf("f%d", i)] = 58 + i
	}
	codes["f11"], codes["f12"] = 87, 88
	keys := strings.Split(strings.ToLower(combo), "+")
	pressed := make([]int, 0, len(keys))
	for _, key := range keys {
		code, ok := codes[strings.TrimSpace(key)]
		if !ok {
			return nil, fmt.Errorf("tecla nao suportada no Wayland: %s", key)
		}
		pressed = append(pressed, code)
	}
	args := []string{"key"}
	for _, code := range pressed {
		args = append(args, strconv.Itoa(code)+":1")
	}
	for i := len(pressed) - 1; i >= 0; i-- {
		args = append(args, strconv.Itoa(pressed[i])+":0")
	}
	return args, nil
}

func keyPressYdotool(ctx context.Context, combo string) error {
	args, err := ydotoolKeyArgs(combo)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = desktopCommandContext(ctx, "ydotool", args...).Run()
	if err != nil {
		// Cancellation can interrupt the chord after modifier-down events.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		release := append([]string{"key"}, args[1+(len(args)-1)/2:]...)
		if releaseErr := desktopCommandContext(cleanupCtx, "ydotool", release...).Run(); releaseErr != nil {
			return fmt.Errorf("falha de entrada e ao liberar teclas: %w", err)
		}
	}
	return err
}

// pasteTextWayland replaces the regular clipboard and leaves the selection owned
// by wl-copy for asynchronous consumers. Restoring it immediately races the app.
// Ctrl+V requires a focused application that supports that paste shortcut.
func pasteTextWayland(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if DetectDisplayServer() != DisplayWayland || !hasCmd("wl-copy") {
		return fmt.Errorf("texto Unicode requer Wayland e wl-copy (pacote wl-clipboard)")
	}
	copyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := desktopCommandContext(copyCtx, "wl-copy", "--type", "text/plain;charset=utf-8")
	// Never put potentially sensitive text in command arguments or diagnostics.
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("falha ao copiar texto Unicode no Wayland; nenhuma colagem enviada")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return keyPressYdotool(ctx, "ctrl+v")
}
