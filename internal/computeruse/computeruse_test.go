package computeruse

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPackageCompiles is a trivial smoke test that ensures the package
// builds and its test binary can run.
func TestPackageCompiles(t *testing.T) {
	t.Log("computeruse loaded")
}

// ─── DisplayServer constants ──────────────────────────────────────────

func TestDisplayServerConstants(t *testing.T) {
	cases := map[DisplayServer]string{
		DisplayX11:     "x11",
		DisplayWayland: "wayland",
		DisplayMacOS:   "macos",
		DisplayWindows: "windows",
		DisplayUnknown: "unknown",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("DisplayServer %q: got %q want %q", want, string(k), want)
		}
	}
}

// ─── DetectDisplayServer ──────────────────────────────────────────────

// saveAndClearLinuxEnv saves relevant env vars and clears them; returns
// a restore func. Safe to call on non-Linux; it's a no-op effectively.
func saveAndClearLinuxEnv(t *testing.T) func() {
	t.Helper()
	keys := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"}
	saved := make(map[string]string, len(keys))
	had := make(map[string]bool, len(keys))
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = v
			had[k] = true
		}
		_ = os.Unsetenv(k)
	}
	return func() {
		for _, k := range keys {
			if had[k] {
				_ = os.Setenv(k, saved[k])
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

func TestDetectDisplayServer_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-only test (running on %s)", runtime.GOOS)
	}

	restore := saveAndClearLinuxEnv(t)
	defer restore()

	// No env set → DisplayUnknown.
	if got := DetectDisplayServer(); got != DisplayUnknown {
		t.Errorf("no env vars: got %q want %q", got, DisplayUnknown)
	}

	// WAYLAND_DISPLAY → Wayland (takes priority).
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if got := DetectDisplayServer(); got != DisplayWayland {
		t.Errorf("WAYLAND_DISPLAY set: got %q want %q", got, DisplayWayland)
	}
	_ = os.Unsetenv("WAYLAND_DISPLAY")

	// DISPLAY → X11.
	t.Setenv("DISPLAY", ":0")
	if got := DetectDisplayServer(); got != DisplayX11 {
		t.Errorf("DISPLAY set: got %q want %q", got, DisplayX11)
	}
	_ = os.Unsetenv("DISPLAY")

	// XDG_SESSION_TYPE=wayland fallback.
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	if got := DetectDisplayServer(); got != DisplayWayland {
		t.Errorf("XDG_SESSION_TYPE=wayland: got %q want %q", got, DisplayWayland)
	}

	// XDG_SESSION_TYPE=x11 fallback.
	t.Setenv("XDG_SESSION_TYPE", "x11")
	if got := DetectDisplayServer(); got != DisplayX11 {
		t.Errorf("XDG_SESSION_TYPE=x11: got %q want %q", got, DisplayX11)
	}
}

func TestDetectDisplayServer_OSDefaults(t *testing.T) {
	got := DetectDisplayServer()
	switch runtime.GOOS {
	case "darwin":
		if got != DisplayMacOS {
			t.Errorf("darwin: got %q want %q", got, DisplayMacOS)
		}
	case "windows":
		if got != DisplayWindows {
			t.Errorf("windows: got %q want %q", got, DisplayWindows)
		}
	case "linux":
		// Known values; don't assert a specific one.
		valid := map[DisplayServer]bool{
			DisplayX11: true, DisplayWayland: true, DisplayUnknown: true,
		}
		if !valid[got] {
			t.Errorf("linux: unexpected %q", got)
		}
	}
}

// ─── IsDisplayAvailable ───────────────────────────────────────────────

func TestIsDisplayAvailable(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "windows":
		if !IsDisplayAvailable() {
			t.Errorf("%s: IsDisplayAvailable should be true", runtime.GOOS)
		}
	case "linux":
		restore := saveAndClearLinuxEnv(t)
		defer restore()

		if IsDisplayAvailable() {
			t.Error("linux, no env: IsDisplayAvailable should be false")
		}

		t.Setenv("DISPLAY", ":0")
		if !IsDisplayAvailable() {
			t.Error("linux, DISPLAY set: should be true")
		}
		_ = os.Unsetenv("DISPLAY")

		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		if !IsDisplayAvailable() {
			t.Error("linux, WAYLAND_DISPLAY set: should be true")
		}
		_ = os.Unsetenv("WAYLAND_DISPLAY")

		t.Setenv("XDG_SESSION_TYPE", "x11")
		if !IsDisplayAvailable() {
			t.Error("linux, XDG_SESSION_TYPE=x11: should be true")
		}
		t.Setenv("XDG_SESSION_TYPE", "wayland")
		if !IsDisplayAvailable() {
			t.Error("linux, XDG_SESSION_TYPE=wayland: should be true")
		}
		t.Setenv("XDG_SESSION_TYPE", "tty")
		if IsDisplayAvailable() {
			t.Error("linux, XDG_SESSION_TYPE=tty: should be false")
		}
	}
}

// ─── InputConfig ──────────────────────────────────────────────────────

func TestDefaultInputConfig(t *testing.T) {
	c := DefaultInputConfig()
	if c.TypeDelay <= 0 {
		t.Errorf("TypeDelay should be positive, got %d", c.TypeDelay)
	}
	if c.ClickDelay <= 0 {
		t.Errorf("ClickDelay should be positive, got %d", c.ClickDelay)
	}
	if c.MoveSettleDelay <= 0 {
		t.Errorf("MoveSettleDelay should be positive, got %d", c.MoveSettleDelay)
	}
	// Pinned-ish sanity checks.
	if c.TypeDelay > 500 {
		t.Errorf("TypeDelay unreasonably large: %d", c.TypeDelay)
	}
}

// ─── DisplayGeometry JSON round-trip ──────────────────────────────────

func TestDisplayGeometryJSON(t *testing.T) {
	g := DisplayGeometry{
		Width:       1920,
		Height:      1080,
		ScaleFactor: 2.0,
		DisplayID:   1,
		OriginX:     0,
		OriginY:     0,
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, key := range []string{`"width":1920`, `"height":1080`, `"scaleFactor":2`, `"displayId":1`, `"originX":0`, `"originY":0`} {
		if !strings.Contains(s, key) {
			t.Errorf("marshaled JSON missing %s: %s", key, s)
		}
	}

	var back DisplayGeometry
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != g {
		t.Errorf("round trip: got %+v want %+v", back, g)
	}
}

// ─── NewComputerUseService ────────────────────────────────────────────

func TestNewComputerUseService(t *testing.T) {
	s := NewComputerUseService()
	if s == nil {
		t.Fatal("NewComputerUseService returned nil")
	}
	// Platform() should match runtime/env-based detection.
	if s.Platform() != DetectDisplayServer() {
		t.Errorf("Platform(): got %q want %q", s.Platform(), DetectDisplayServer())
	}
}

// ─── GetDisplaySize on an un-displayed system ─────────────────────────

func TestGetDisplaySize_NoDisplayOnLinuxReturnsError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("linux-only (running on %s)", runtime.GOOS)
	}
	restore := saveAndClearLinuxEnv(t)
	defer restore()

	// With no display env vars, GetDisplaySize → DisplayUnknown → error.
	if _, err := GetDisplaySize(); err == nil {
		t.Error("expected error when no display server detected")
	}
}

// ─── ToolAction / ToolResult JSON round-trip ──────────────────────────

func TestToolActionJSON(t *testing.T) {
	a := ToolAction{
		Action:    "click",
		X:         100,
		Y:         200,
		Button:    "left",
		Text:      "hello",
		Key:       "enter",
		Direction: "up",
		Amount:    3,
		ToX:       300,
		ToY:       400,
		Count:     2,
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ToolAction
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != a {
		t.Errorf("round trip: got %+v want %+v", back, a)
	}
}

func TestToolActionJSON_OmitEmpty(t *testing.T) {
	// An action with only Action set should omit empty fields.
	a := ToolAction{Action: "screenshot"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	for _, k := range []string{"x", "y", "text", "key", "button", "direction", "amount", "to_x", "to_y", "count"} {
		if strings.Contains(s, `"`+k+`":`) {
			t.Errorf("empty field %q should be omitted, got: %s", k, s)
		}
	}
}

func TestToolResultJSON(t *testing.T) {
	r := ToolResult{
		Success:    true,
		Message:    "ok",
		Screenshot: "base64data",
		Width:      1920,
		Height:     1080,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ToolResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != r {
		t.Errorf("round trip mismatch: got %+v want %+v", back, r)
	}
}

// ─── needsCoordinates helper ──────────────────────────────────────────

func TestNeedsCoordinates(t *testing.T) {
	yes := []string{"click", "double_click", "right_click", "move_mouse", "scroll", "drag"}
	no := []string{"screenshot", "type", "key_press", "key", "unknown", ""}

	for _, a := range yes {
		if !needsCoordinates(a) {
			t.Errorf("needsCoordinates(%q): want true", a)
		}
	}
	for _, a := range no {
		if needsCoordinates(a) {
			t.Errorf("needsCoordinates(%q): want false", a)
		}
	}
}

// ─── TargetImageSize pure function ────────────────────────────────────

func TestTargetImageSize(t *testing.T) {
	cases := []struct {
		name         string
		physW, physH int
		maxW, maxH   int
		wantW, wantH int
	}{
		{"within bounds returns unchanged", 800, 600, 1024, 768, 800, 600},
		{"equal to max returns unchanged", 1024, 768, 1024, 768, 1024, 768},
		{"scale down by width", 2000, 1000, 1000, 1000, 1000, 500},
		{"scale down by height", 1000, 2000, 1000, 1000, 500, 1000},
		{"proportional 2x downscale", 3840, 2160, 1920, 1080, 1920, 1080},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := TargetImageSize(c.physW, c.physH, c.maxW, c.maxH)
			if w != c.wantW || h != c.wantH {
				t.Errorf("TargetImageSize(%d,%d,%d,%d) = (%d,%d), want (%d,%d)",
					c.physW, c.physH, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
			}
		})
	}
}

// ─── FormatDimensions pure function ───────────────────────────────────

func TestFormatDimensions(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{1920, 1080, "1920x1080"},
		{0, 0, "0x0"},
		{800, 600, "800x600"},
	}
	for _, c := range cases {
		if got := FormatDimensions(c.w, c.h); got != c.want {
			t.Errorf("FormatDimensions(%d,%d) = %q, want %q", c.w, c.h, got, c.want)
		}
	}
}

// ─── escapeWindowsKeys pure function ──────────────────────────────────

func TestEscapeWindowsKeys(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"enter", "{ENTER}"},
		{"tab", "{TAB}"},
		{"escape", "{ESC}"},
		{"ctrl+c", "^c"},
		{"shift+tab", "+{TAB}"},
	}
	for _, c := range cases {
		if got := escapeWindowsKeys(c.in); got != c.want {
			t.Errorf("escapeWindowsKeys(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── hasCmd helper ────────────────────────────────────────────────────

func TestHasCmd(t *testing.T) {
	// There should always be SOME command present; and a nonsense one should not be.
	if hasCmd("definitely_not_a_real_binary_kilo_test_xyzzy") {
		t.Error("hasCmd returned true for a fake binary")
	}
	// Test a very likely-present binary depending on platform.
	switch runtime.GOOS {
	case "linux", "darwin":
		if !hasCmd("sh") {
			t.Error("hasCmd(sh) returned false on unix")
		}
	}
}

// ─── NewComputerUseTool ───────────────────────────────────────────────

func TestNewComputerUseTool(t *testing.T) {
	tool := NewComputerUseTool()
	if tool == nil {
		t.Fatal("NewComputerUseTool returned nil")
	}
	if tool.service == nil {
		t.Error("service not initialized")
	}
	if tool.screenshotCache <= 0 {
		t.Errorf("screenshotCache should be positive, got %v", tool.screenshotCache)
	}
	if tool.rateLimit <= 0 {
		t.Errorf("rateLimit should be positive, got %v", tool.rateLimit)
	}
	// Expected defaults from source: 2s cache, 100ms rate limit.
	if tool.screenshotCache != 2*time.Second {
		t.Errorf("screenshotCache: got %v want 2s", tool.screenshotCache)
	}
	if tool.rateLimit != 100*time.Millisecond {
		t.Errorf("rateLimit: got %v want 100ms", tool.rateLimit)
	}
}

// ─── ComputerUseTool.Execute parses JSON ──────────────────────────────

func TestComputerUseTool_Execute_InvalidJSON(t *testing.T) {
	tool := NewComputerUseTool()
	_, err := tool.Execute(context.Background(), "{not valid json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestComputerUseTool_ExecuteAction_UnknownAction(t *testing.T) {
	tool := NewComputerUseTool()
	res, err := tool.ExecuteAction(context.Background(), ToolAction{Action: "nope"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Success {
		t.Error("unknown action should not succeed")
	}
	if !strings.Contains(res.Error, "unknown action") {
		t.Errorf("error should mention unknown action: %q", res.Error)
	}
}

func TestComputerUseTool_ExecuteAction_TypeWithoutText(t *testing.T) {
	tool := NewComputerUseTool()
	// The validation for type-without-text short-circuits before invoking
	// keyboard simulation. needsCoordinates("type") is false, so this is safe.
	res, err := tool.ExecuteAction(context.Background(), ToolAction{Action: "type"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("type without text should fail")
	}
	if !strings.Contains(strings.ToLower(res.Error), "text") {
		t.Errorf("error should mention text: %q", res.Error)
	}
}

func TestComputerUseTool_ExecuteAction_KeyPressWithoutKey(t *testing.T) {
	tool := NewComputerUseTool()
	res, err := tool.ExecuteAction(context.Background(), ToolAction{Action: "key_press"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("key_press without key should fail")
	}
	if !strings.Contains(strings.ToLower(res.Error), "key") {
		t.Errorf("error should mention key: %q", res.Error)
	}
}

// ─── validateCoordinates via executeAction when coords are invalid ────

func TestComputerUseTool_ValidateCoordinates_Negative(t *testing.T) {
	// validateCoordinates is only strict when display info is available.
	// On a headless runner it may not be, in which case the validator
	// lets negatives through. We therefore only check the branch that
	// runs regardless: either the coord error OR an input-tool error,
	// but never Success=true with negatives on a working display.
	tool := NewComputerUseTool()
	res, err := tool.ExecuteAction(context.Background(), ToolAction{
		Action: "move_mouse",
		X:      -10,
		Y:      -10,
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	// Either the coord validator rejected it (if display known) or
	// downstream tool invocation failed (no display / no xdotool etc.).
	if res.Success {
		t.Errorf("negative coords should not succeed: %+v", res)
	}
}

// ─── intArg, intArgDefault, stringArg helpers ─────────────────────────

func TestIntArg(t *testing.T) {
	args := map[string]any{
		"a": float64(42), // JSON numbers decode to float64
		"b": 17,
		"c": "9",
		"d": "notanumber",
		"e": true, // unsupported → 0
	}
	if intArg(args, "a") != 42 {
		t.Errorf("float64: got %d want 42", intArg(args, "a"))
	}
	if intArg(args, "b") != 17 {
		t.Errorf("int: got %d want 17", intArg(args, "b"))
	}
	if intArg(args, "c") != 9 {
		t.Errorf("string: got %d want 9", intArg(args, "c"))
	}
	if intArg(args, "d") != 0 {
		t.Errorf("bad string: got %d want 0", intArg(args, "d"))
	}
	if intArg(args, "e") != 0 {
		t.Errorf("bool: got %d want 0", intArg(args, "e"))
	}
	if intArg(args, "missing") != 0 {
		t.Errorf("missing: want 0")
	}
}

func TestIntArgDefault(t *testing.T) {
	args := map[string]any{
		"present":  5,
		"zerolike": 0,
	}
	if got := intArgDefault(args, "present", 99); got != 5 {
		t.Errorf("present: got %d want 5", got)
	}
	if got := intArgDefault(args, "missing", 99); got != 99 {
		t.Errorf("missing: got %d want 99", got)
	}
	// intArg returns 0 for the zerolike value, and intArgDefault treats 0 as default.
	if got := intArgDefault(args, "zerolike", 99); got != 99 {
		t.Errorf("zerolike: got %d want 99", got)
	}
}

func TestStringArg(t *testing.T) {
	args := map[string]any{
		"a": "hello",
		"b": "",
		"c": 42, // wrong type
	}
	if got := stringArg(args, "a", "def"); got != "hello" {
		t.Errorf("a: got %q want hello", got)
	}
	if got := stringArg(args, "b", "def"); got != "def" {
		t.Errorf("empty string: got %q want def", got)
	}
	if got := stringArg(args, "c", "def"); got != "def" {
		t.Errorf("wrong type: got %q want def", got)
	}
	if got := stringArg(args, "missing", "def"); got != "def" {
		t.Errorf("missing: got %q want def", got)
	}
}

// ─── toolResultFromCU ─────────────────────────────────────────────────

func TestToolResultFromCU_Error(t *testing.T) {
	r := toolResultFromCU(&ToolResult{Success: false, Error: "boom"})
	if !r.IsError {
		t.Error("IsError should be true")
	}
	if !strings.Contains(r.Content, "boom") {
		t.Errorf("content should contain error: %q", r.Content)
	}
}

func TestToolResultFromCU_ErrorFallback(t *testing.T) {
	r := toolResultFromCU(&ToolResult{Success: false})
	if !r.IsError {
		t.Error("IsError should be true")
	}
	if r.Content == "" {
		t.Error("content should have a fallback message")
	}
}

func TestToolResultFromCU_SuccessWithScreenshot(t *testing.T) {
	r := toolResultFromCU(&ToolResult{
		Success:    true,
		Message:    "Screenshot captured",
		Screenshot: "aGVsbG8=", // "hello" base64
	})
	if r.IsError {
		t.Error("IsError should be false")
	}
	if !strings.Contains(r.Content, "Screenshot captured") {
		t.Errorf("content should contain message: %q", r.Content)
	}
	if !strings.Contains(r.Content, "screenshot") {
		t.Errorf("content should mention screenshot marker: %q", r.Content)
	}
}

func TestToolResultFromCU_SuccessEmpty(t *testing.T) {
	r := toolResultFromCU(&ToolResult{Success: true})
	if r.IsError {
		t.Error("IsError should be false")
	}
	if r.Content != "OK" {
		t.Errorf("empty success content: got %q want OK", r.Content)
	}
}

// ─── Image processing ────────────────────────────────────────────────

// makePNG creates a solid-color PNG of the given size for tests.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestResizeGoFallback_NoUpscale(t *testing.T) {
	// When the image is smaller than max dimensions, should return unchanged.
	in := makePNG(t, 50, 50)
	out, err := resizeGoFallback(in, 200, 200)
	if err != nil {
		t.Fatalf("resizeGoFallback: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Error("expected unchanged bytes when no downscale needed")
	}
}

func TestResizeGoFallback_Downscale(t *testing.T) {
	in := makePNG(t, 400, 200)
	out, err := resizeGoFallback(in, 100, 100)
	if err != nil {
		t.Fatalf("resizeGoFallback: %v", err)
	}
	// Decode the result and check dimensions.
	img, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode resized png: %v (len=%d, first bytes=%x)", err, len(out), out[:min(16, len(out))])
	}
	b := img.Bounds()
	// Expect 100x50 (scale factor 0.25).
	if b.Dx() != 100 || b.Dy() != 50 {
		t.Errorf("resized dims: got %dx%d want 100x50", b.Dx(), b.Dy())
	}
}

func TestResizeGoFallback_InvalidImage(t *testing.T) {
	_, err := resizeGoFallback([]byte("not an image"), 100, 100)
	if err == nil {
		t.Error("expected error for invalid image data")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
