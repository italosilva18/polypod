package computeruse

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveDesktopScreenshotAndOCR(t *testing.T) {
	if os.Getenv("POLYPOD_TEST_LIVE_DESKTOP") != "1" {
		t.Skip("set POLYPOD_TEST_LIVE_DESKTOP=1 to test the active desktop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	imageData, err := CaptureScreen(ctx)
	if err != nil {
		t.Fatalf("CaptureScreen: %v", err)
	}
	if len(imageData) < 1024 {
		t.Fatalf("screenshot unexpectedly small: %d bytes", len(imageData))
	}
	description, err := DescribeScreenshot(ctx, imageData)
	if err != nil {
		t.Fatalf("DescribeScreenshot: %v", err)
	}
	if !strings.Contains(description, "[x=") {
		t.Fatalf("OCR returned no coordinates: %q", description)
	}
	if len(description) > 4000 {
		description = description[:4000]
	}
	t.Logf("screenshot=%d bytes\n%s", len(imageData), description)
}
