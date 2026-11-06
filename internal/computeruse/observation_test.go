package computeruse

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPostActionObservationBypassesCache(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // OCR unavailable must not lose image.
	cu := NewComputerUseTool()
	cu.lastScreenshot = []byte("stale")
	cu.lastScreenTime = time.Now()
	calls := 0
	cu.captureScreen = func(context.Context) ([]byte, error) { calls++; return []byte("fresh"), nil }
	result := &ToolResult{Success: true, Message: "Clicked"}
	cu.observeAfterAction(context.Background(), result)
	if calls != 1 || result.Screenshot != base64.StdEncoding.EncodeToString([]byte("fresh")) {
		t.Fatal("post-action capture reused stale screen")
	}
	if !strings.Contains(result.Message, "verifique") {
		t.Fatal("observation must request effect verification")
	}
}

func TestPostActionObservationFailureDoesNotReplayOrFailAction(t *testing.T) {
	cu := NewComputerUseTool()
	calls := 0
	cu.captureScreen = func(context.Context) ([]byte, error) { calls++; return nil, errors.New("capture unavailable") }
	result := &ToolResult{Success: true, Message: "Typed"}
	cu.observeAfterAction(context.Background(), result)
	if !result.Success || calls != 1 || result.Screenshot != "" || !strings.Contains(result.Message, "verificação visual pendente") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCancelledActionDoesNotCapture(t *testing.T) {
	cu := NewComputerUseTool()
	cu.captureScreen = func(context.Context) ([]byte, error) { t.Fatal("capture after cancellation"); return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cu.ExecuteAction(ctx, ToolAction{Action: "screenshot"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
