//go:build linux

package computeruse

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestScreenshotCommandStopsDescendantsAfterCancellation(t *testing.T) {
	t.Setenv("POLYPOD_DESKTOP_USER", "")
	marker := filepath.Join(t.TempDir(), "orphan-ran")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cmd := desktopCommandContext(ctx, "sh", "-c", `(sleep 0.4; printf alive > "$1") & wait`, "sh", marker)
	superviseScreenshotCommand(cmd)
	defer terminateScreenshotCommand(cmd)
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected the screenshot command to be canceled")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("screenshot descendant survived cancellation: %v", err)
	}
}

func TestScreenshotCommandStopsDescendantsAfterPipeTimeout(t *testing.T) {
	t.Setenv("POLYPOD_DESKTOP_USER", "")
	marker := filepath.Join(t.TempDir(), "orphan-ran")
	cmd := desktopCommandContext(context.Background(), "sh", "-c", `(sleep 0.4; printf alive > "$1") &`, "sh", marker)
	cmd.WaitDelay = 40 * time.Millisecond
	superviseScreenshotCommand(cmd)
	defer terminateScreenshotCommand(cmd)
	if _, err := cmd.CombinedOutput(); !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected a pipe wait timeout, got %v", err)
	}
	if err := terminateScreenshotCommand(cmd); err != nil {
		t.Fatalf("stop screenshot descendants: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("screenshot descendant survived pipe timeout: %v", err)
	}
}
