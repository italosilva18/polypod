package autoupdate

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---------- CompareVersions ----------

func TestCompareVersions_Same(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.IsSame {
		t.Error("IsSame should be true")
	}
	if cmp.IsOutdated {
		t.Error("IsOutdated should be false")
	}
	if cmp.IsNewer {
		t.Error("IsNewer should be false")
	}
	if cmp.DiffKind != "none" {
		t.Errorf("DiffKind = %q; want none", cmp.DiffKind)
	}
}

func TestCompareVersions_OutdatedMajor(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "2.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.IsOutdated {
		t.Error("IsOutdated should be true")
	}
	if cmp.IsSame || cmp.IsNewer {
		t.Error("IsSame/IsNewer should be false")
	}
	if cmp.DiffKind != "major" {
		t.Errorf("DiffKind = %q; want major", cmp.DiffKind)
	}
}

func TestCompareVersions_OutdatedMinor(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.2.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.IsOutdated {
		t.Error("IsOutdated should be true")
	}
	if cmp.DiffKind != "minor" {
		t.Errorf("DiffKind = %q; want minor", cmp.DiffKind)
	}
}

func TestCompareVersions_OutdatedPatch(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.3")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.IsOutdated {
		t.Error("IsOutdated should be true")
	}
	if cmp.DiffKind != "patch" {
		t.Errorf("DiffKind = %q; want patch", cmp.DiffKind)
	}
}

func TestCompareVersions_Newer(t *testing.T) {
	cmp, err := CompareVersions("2.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.IsNewer {
		t.Error("IsNewer should be true")
	}
	if cmp.IsOutdated {
		t.Error("IsOutdated should be false")
	}
}

func TestCompareVersions_InvalidVersion(t *testing.T) {
	if _, err := CompareVersions("not-a-version", "1.0.0"); err == nil {
		t.Error("expected error for invalid current version")
	}
	if _, err := CompareVersions("1.0.0", "also-bad"); err == nil {
		t.Error("expected error for invalid latest version")
	}
}

// ---------- ShouldNotifyUpdate ----------

func TestShouldNotifyUpdate_NotOutdated(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if ShouldNotifyUpdate(cmp, "patch") {
		t.Error("not-outdated should never notify")
	}
	if ShouldNotifyUpdate(cmp, "major") {
		t.Error("not-outdated should never notify")
	}
}

func TestShouldNotifyUpdate_MajorMeetsPatchThreshold(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "2.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !ShouldNotifyUpdate(cmp, "patch") {
		t.Error("major update should satisfy patch threshold")
	}
}

func TestShouldNotifyUpdate_PatchBelowMajorThreshold(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.1")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if ShouldNotifyUpdate(cmp, "major") {
		t.Error("patch update should not satisfy major threshold")
	}
}

func TestShouldNotifyUpdate_PatchMeetsPatchThreshold(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.1")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !ShouldNotifyUpdate(cmp, "patch") {
		t.Error("patch update should satisfy patch threshold")
	}
}

// ---------- FormatUpdateMessage ----------

func TestFormatUpdateMessage_Same(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	msg := FormatUpdateMessage(cmp)
	if !strings.Contains(msg, "latest") {
		t.Errorf("message %q should contain 'latest'", msg)
	}
}

func TestFormatUpdateMessage_Outdated(t *testing.T) {
	cmp, err := CompareVersions("1.0.0", "2.0.0")
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	msg := FormatUpdateMessage(cmp)
	if !strings.Contains(msg, "1.0.0") {
		t.Errorf("message %q missing current version", msg)
	}
	if !strings.Contains(msg, "2.0.0") {
		t.Errorf("message %q missing latest version", msg)
	}
	if !strings.Contains(msg, "major") {
		t.Errorf("message %q missing diff kind", msg)
	}
}

// ---------- PickLatest ----------

func TestPickLatest(t *testing.T) {
	latest, ok := PickLatest([]string{"1.0.0", "2.0.0", "1.5.0"})
	if !ok {
		t.Fatal("PickLatest returned !ok")
	}
	if latest != "2.0.0" {
		t.Errorf("latest = %q; want 2.0.0", latest)
	}
}

func TestPickLatest_Empty(t *testing.T) {
	latest, ok := PickLatest([]string{})
	if ok {
		t.Error("PickLatest on empty slice should return ok=false")
	}
	if latest != "" {
		t.Errorf("latest = %q; want empty", latest)
	}
}

func TestPickLatest_SkipsInvalid(t *testing.T) {
	latest, ok := PickLatest([]string{"not-a-version", "1.0.0"})
	if !ok {
		t.Fatal("PickLatest returned !ok")
	}
	if latest != "1.0.0" {
		t.Errorf("latest = %q; want 1.0.0", latest)
	}
}

// ---------- VersionMatchesConstraint ----------

func TestVersionMatchesConstraint_Match(t *testing.T) {
	ok, err := VersionMatchesConstraint("1.2.3", "^1.0.0")
	if err != nil {
		t.Fatalf("VersionMatchesConstraint: %v", err)
	}
	if !ok {
		t.Error("1.2.3 should match ^1.0.0")
	}
}

func TestVersionMatchesConstraint_NoMatch(t *testing.T) {
	ok, err := VersionMatchesConstraint("2.0.0", "^1.0.0")
	if err != nil {
		t.Fatalf("VersionMatchesConstraint: %v", err)
	}
	if ok {
		t.Error("2.0.0 should not match ^1.0.0")
	}
}

// ---------- CollectTelemetry ----------

func TestCollectTelemetry(t *testing.T) {
	payload, err := CollectTelemetry("1.2.3")
	if err != nil {
		t.Fatalf("CollectTelemetry: %v", err)
	}
	if payload.Version != "1.2.3" {
		t.Errorf("Version = %q; want 1.2.3", payload.Version)
	}
	if payload.InstallID == "" {
		t.Error("InstallID empty")
	}
	if len(payload.InstallID) != 8 {
		t.Errorf("InstallID length = %d; want 8", len(payload.InstallID))
	}
	if payload.OS == "" {
		t.Error("OS empty")
	}
	if payload.Arch == "" {
		t.Error("Arch empty")
	}
	if time.Since(payload.CheckedAt) > 10*time.Second {
		t.Error("CheckedAt not recent")
	}
	if time.Since(payload.CheckedAt) < 0 {
		t.Error("CheckedAt in the future")
	}
}

func TestFingerprintShort(t *testing.T) {
	fp := FingerprintShort(context.Background())
	if fp == "" {
		t.Error("FingerprintShort returned empty string")
	}
	// Should be 8-char anonymized fingerprint or "unknown" on error
	if fp != "unknown" && len(fp) != 8 {
		t.Errorf("FingerprintShort = %q; want 8 chars or 'unknown'", fp)
	}
}
