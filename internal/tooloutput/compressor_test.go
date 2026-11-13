package tooloutput

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestCompressForModel_ExtractsDiagnosticsAndArchivesFullLog(t *testing.T) {
	t.Setenv("POLYPOD_DATA_DIR", t.TempDir())

	var b strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&b, "ok   github.com/example/pkg%03d   0.012s [passing noise line %d padding-padding-padding]\n", i, i)
		if i == 90 {
			b.WriteString("--- FAIL: TestOAuthTokenRotation (0.04s)\n")
			b.WriteString("    rotation_test.go:54: expected second account token, got HTTP 429\n")
		}
		if i == 165 {
			b.WriteString("internal/auth/handler.go:42:9: undefined: refreshSecondarySession\n")
		}
	}
	raw := b.String()
	if len(raw) <= CompressThreshold {
		t.Fatalf("expected test fixture > %d bytes, got %d", CompressThreshold, len(raw))
	}

	compressed := CompressForModel("bash", raw)
	if len(compressed) >= len(raw)/2 {
		t.Fatalf("expected compressed output to be <50%% of raw (%d bytes), got %d bytes", len(raw), len(compressed))
	}
	if !strings.Contains(compressed, "TestOAuthTokenRotation") {
		t.Fatalf("expected failing test name preserved in compressed output:\n%s", compressed)
	}
	if !strings.Contains(compressed, "internal/auth/handler.go:42:9: undefined: refreshSecondarySession") {
		t.Fatalf("expected compiler error preserved in compressed output:\n%s", compressed)
	}

	reID := regexp.MustCompile(`id=([0-9a-f]{32})`)
	m := reID.FindStringSubmatch(compressed)
	if len(m) != 2 {
		t.Fatalf("expected archived tool_output id in compressed output, got:\n%s", compressed)
	}
	page, _, total, err := Read(m[1], 0, 4096)
	if err != nil {
		t.Fatalf("Read archived output: %v", err)
	}
	if int(total) != len(raw) || len(page) == 0 {
		t.Fatalf("expected archived total=%d, got total=%d page=%d", len(raw), total, len(page))
	}

	// Non-command tools (e.g., read_file) below PreviewLimit must NOT be structurally compressed.
	untouched := CompressForModel("read_file", raw)
	if untouched != raw {
		t.Fatalf("expected read_file output <= PreviewLimit to remain verbatim (len %d vs %d)", len(untouched), len(raw))
	}
}
