package autoupdate

import "testing"

func TestCurrentVersion(t *testing.T) {
	if got := CurrentVersion(); got != "0.6.0" {
		t.Fatalf("CurrentVersion() = %q; want 0.6.0", got)
	}
}
