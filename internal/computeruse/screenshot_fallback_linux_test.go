//go:build linux

package computeruse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeScreenshotTestCommand(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func screenshotTestPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	t.Setenv("POLYPOD_DESKTOP_USER", "")
	return dir
}

func TestCaptureScreenLinuxPrefersGrimOnWayland(t *testing.T) {
	dir := screenshotTestPath(t)
	writeScreenshotTestCommand(t, dir, "grim", `printf grim > "$1"`)
	writeScreenshotTestCommand(t, dir, "cosmic-screenshot", `exit 73`)
	out := filepath.Join(t.TempDir(), "screen.png")
	if err := captureScreenLinux(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "grim" {
		t.Fatalf("Wayland capture should use grim: content=%q err=%v", got, err)
	}
}

func TestCaptureScreenLinuxFallsBackAfterGrimFailure(t *testing.T) {
	dir := screenshotTestPath(t)
	writeScreenshotTestCommand(t, dir, "grim", `exit 73`)
	writeScreenshotTestCommand(t, dir, "cosmic-screenshot", `
while test "$#" -gt 0; do
  if test "$1" = "--save-dir"; then
    shift
    printf cosmic > "$1/shot.png"
    exit 0
  fi
  shift
done
exit 74`)
	out := filepath.Join(t.TempDir(), "screen.png")
	if err := captureScreenLinux(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "cosmic" {
		t.Fatalf("native fallback should run: content=%q err=%v", got, err)
	}
}

func TestCaptureRegionLinuxPrefersGrimOnWayland(t *testing.T) {
	dir := screenshotTestPath(t)
	writeScreenshotTestCommand(t, dir, "grim", `for last; do :; done; printf grim > "$last"`)
	writeScreenshotTestCommand(t, dir, "import", `exit 73`)
	out := filepath.Join(t.TempDir(), "region.png")
	if err := captureRegionLinux(context.Background(), 1, 2, 3, 4, out); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "grim" {
		t.Fatalf("Wayland region should use grim: content=%q err=%v", got, err)
	}
}
