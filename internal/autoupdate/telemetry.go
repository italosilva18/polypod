package autoupdate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/fingerprint"
)

// TelemetryPayload is the anonymized install information sent with update checks.
type TelemetryPayload struct {
	InstallID   string    `json:"install_id"`
	Version     string    `json:"version"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	CheckedAt   time.Time `json:"checked_at"`
	IsCI        bool      `json:"is_ci"`
	IsContainer bool      `json:"is_container"`
}

// CollectTelemetry gathers anonymized install info. InstallID is a stable
// SHA-256 hash of os+arch+hostname+exe+mac — cannot be reversed to identify
// a user.
func CollectTelemetry(version string) (TelemetryPayload, error) {
	fp, err := fingerprint.Anonymized()
	if err != nil {
		return TelemetryPayload{}, fmt.Errorf("fingerprint: %w", err)
	}

	info := fingerprint.CurrentInfo()

	return TelemetryPayload{
		InstallID:   fp,
		Version:     version,
		OS:          info.OS,
		Arch:        info.Arch,
		CheckedAt:   time.Now().UTC(),
		IsCI:        isCIEnv(),
		IsContainer: isContainerEnv(),
	}, nil
}

// FingerprintShort returns just the short anonymized fingerprint.
func FingerprintShort(ctx context.Context) string {
	fp, err := fingerprint.Anonymized()
	if err != nil {
		return "unknown"
	}
	return fp
}

func isCIEnv() bool {
	// Common CI env vars
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_URL", "CIRCLECI", "TRAVIS"} {
		if v, ok := lookupEnv(key); ok && v != "" && v != "false" && v != "0" {
			return true
		}
	}
	return false
}

func isContainerEnv() bool {
	// /.dockerenv exists in Docker containers
	if _, err := statFile("/.dockerenv"); err == nil {
		return true
	}
	// /proc/1/cgroup contains "docker" or "kubepods" in containers
	if data, err := readFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		if containsAny(s, "docker", "kubepods", "containerd") {
			return true
		}
	}
	return false
}

func lookupEnv(key string) (string, bool) { return os.LookupEnv(key) }
func statFile(path string) (any, error)   { _, err := os.Stat(path); return nil, err }
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
