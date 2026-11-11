package computeruse

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestDesktopCommandContextBoundsInheritedPipeWait(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires a POSIX shell")
	}
	t.Setenv("POLYPOD_DESKTOP_USER", "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	cmd := desktopCommandContext(ctx, "sh", "-c", "sleep 5 &")
	_, err := cmd.CombinedOutput()
	if !errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded wait for inherited pipe, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("desktop command ignored cancellation for %v", elapsed)
	}
}
