package computeruse

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestYdotoolChord(t *testing.T) {
	args, err := ydotoolKeyArgs("ctrl+shift+Escape")
	want := []string{"key", "29:1", "42:1", "1:1", "1:0", "42:0", "29:0"}
	if err != nil || !reflect.DeepEqual(args, want) {
		t.Fatalf("got %v, %v", args, err)
	}
	if _, err := ydotoolKeyArgs("ctrl+invalid"); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestWaylandRoutesAllInputToYdotool(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux input")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "events")
	for _, tool := range []string{"xdotool", "ydotool"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$INPUT_TEST_LOG\"\n"
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("INPUT_TEST_LOG", log)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("DISPLAY", ":99")
	t.Setenv("POLYPOD_DESKTOP_USER", "")
	ctx := context.Background()
	for _, err := range []error{MouseClick(ctx, 10, 20, "right"), MouseScroll(ctx, 10, 20, "down", 3), KeyCombination(ctx, "ctrl", "a"), TypeText(ctx, "--hello\\n"), Drag(ctx, 10, 20, 30, 40)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "xdotool") {
		t.Fatalf("Wayland used X11: %s", got)
	}
	for _, want := range []string{"mousemove --absolute -x 10 -y 20", "click 0xC1", "mousemove --wheel -x 0 -y -3", "key 29:1 30:1 30:0 29:0", "--escape=0 -- --hello\\n", "click 0x40", "click 0x80"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestWaylandUnicodeClipboard(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux input")
	}
	for _, tc := range []struct {
		name       string
		copyScript string
		cancel     bool
		timeout    bool
		wantErr    bool
	}{
		{name: "Portuguese and emoji", copyScript: "/bin/cat > \"$INPUT_TEST_CONTENT\"\n"},
		{name: "clipboard failure prevents paste", copyScript: "echo sensitive-text >&2\nexit 1\n", wantErr: true},
		{name: "cancelled before copy", cancel: true, wantErr: true},
		{name: "missing clipboard", wantErr: true},
		{name: "cancel while copying", copyScript: "exec /bin/sleep 10\n", timeout: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "commands")
			content := filepath.Join(dir, "content")
			t.Setenv("PATH", dir)
			t.Setenv("WAYLAND_DISPLAY", "isolated-test")
			t.Setenv("POLYPOD_DESKTOP_USER", "")
			t.Setenv("INPUT_TEST_LOG", log)
			t.Setenv("INPUT_TEST_CONTENT", content)
			prefix := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$INPUT_TEST_LOG\"\n"
			if err := os.WriteFile(filepath.Join(dir, "ydotool"), []byte(prefix), 0755); err != nil {
				t.Fatal(err)
			}
			if tc.copyScript != "" {
				if err := os.WriteFile(filepath.Join(dir, "wl-copy"), []byte(prefix+tc.copyScript), 0755); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.timeout {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			if tc.cancel {
				cancel()
			}
			text := "--ação, português, 日本語 🌍\nsegunda linha\\n"
			err := TypeText(ctx, text)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			logData, readErr := os.ReadFile(log)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			commands := string(logData)
			if strings.Contains(commands, text) {
				t.Fatal("text leaked into arguments")
			}
			if tc.wantErr {
				if strings.Contains(commands, "ydotool") {
					t.Fatal("paste sent after failure")
				}
				if strings.Contains(err.Error(), "sensitive-text") {
					t.Fatal("stderr leaked")
				}
			} else {
				data, err := os.ReadFile(content)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != text {
					t.Fatal("Unicode changed")
				}
				if !strings.Contains(commands, "wl-copy --type text/plain;charset=utf-8") || !strings.Contains(commands, "ydotool key 29:1 47:1 47:0 29:0") {
					t.Fatalf("wrong command order/arguments: %s", commands)
				}
			}
		})
	}
}
