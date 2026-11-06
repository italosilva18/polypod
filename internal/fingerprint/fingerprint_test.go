package fingerprint

import (
	"testing"
)

func TestGet(t *testing.T) {
	ResetForTest()

	fp, err := Get()
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if fp == "" {
		t.Error("Get returned empty fingerprint")
	}

	if len(fp) != 32 {
		t.Errorf("fingerprint length = %d; want 32", len(fp))
	}

	// Should be valid hex
	for _, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("invalid hex char in fingerprint: %c", c)
			break
		}
	}
}

func TestGet_Cached(t *testing.T) {
	ResetForTest()

	fp1, err := Get()
	if err != nil {
		t.Fatalf("first Get error: %v", err)
	}

	fp2, err := Get()
	if err != nil {
		t.Fatalf("second Get error: %v", err)
	}

	if fp1 != fp2 {
		t.Errorf("cached calls returned different values: %s vs %s", fp1, fp2)
	}
}

func TestMustGet(t *testing.T) {
	ResetForTest()

	fp := MustGet()
	if fp == "" {
		t.Error("MustGet returned empty")
	}
}

func TestAnonymized(t *testing.T) {
	ResetForTest()

	fp, err := Anonymized()
	if err != nil {
		t.Fatalf("Anonymized error: %v", err)
	}
	if len(fp) != 8 {
		t.Errorf("Anonymized length = %d; want 8", len(fp))
	}
}

func TestCurrentInfo(t *testing.T) {
	info := CurrentInfo()

	if info.OS == "" {
		t.Error("CurrentInfo.OS is empty")
	}
	if info.Arch == "" {
		t.Error("CurrentInfo.Arch is empty")
	}
}

func TestResetForTest(t *testing.T) {
	fp1, _ := Get()

	ResetForTest()

	fp2, _ := Get()

	// After reset, the values should still be the same since the input doesn't change
	if fp1 != fp2 {
		t.Errorf("after reset, fingerprint changed: %s vs %s", fp1, fp2)
	}
}

func TestStability(t *testing.T) {
	ResetForTest()
	fp1, _ := Get()

	ResetForTest()
	fp2, _ := Get()

	ResetForTest()
	fp3, _ := Get()

	// The same machine should always produce the same fingerprint
	if fp1 != fp2 || fp2 != fp3 {
		t.Errorf("fingerprint not stable: %s, %s, %s", fp1, fp2, fp3)
	}
}
