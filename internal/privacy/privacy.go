// Package privacy implements privacy level controls for telemetry, history,
// and data sharing. Mirrors the TypeScript privacyLevel.ts module.
package privacy

import (
	"os"
	"strings"
)

// Level describes how restrictive privacy controls are.
// Ordered by restrictiveness: Full < Limited < Minimal < None.
type Level int

const (
	// Full — everything enabled (default).
	Full Level = iota
	// Limited — analytics/telemetry disabled (equivalent to TS "no-telemetry").
	Limited
	// Minimal — all nonessential network traffic disabled (equivalent to TS "essential-traffic").
	Minimal
	// None — all collection disabled.
	None
)

// String returns the human-readable name.
func (l Level) String() string {
	switch l {
	case Full:
		return "full"
	case Limited:
		return "limited"
	case Minimal:
		return "minimal"
	case None:
		return "none"
	default:
		return "unknown"
	}
}

// ParseLevel converts a string to Level. Unknown values default to Full.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "full", "default", "":
		return Full
	case "limited", "no-telemetry":
		return Limited
	case "minimal", "essential-traffic":
		return Minimal
	case "none":
		return None
	default:
		return Full
	}
}

// -------------------------------------------------------------------
// Level resolution from environment / settings
// -------------------------------------------------------------------

// settingsGetter allows injecting configuration for testing without
// importing the config package (avoids circular dependencies).
var settingsGetter func(key string) string

// SetSettingsGetter installs a function that reads a settings value by key.
// Called once during application init.
func SetSettingsGetter(fn func(string) string) {
	settingsGetter = fn
}

// GetPrivacyLevel resolves the effective privacy level from environment variables
// and settings. The most restrictive signal wins.
//
// Environment:
//
//	POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC → Minimal
//	DISABLE_TELEMETRY                    → Limited
//	POLYPOD_PRIVACY_LEVEL                → parsed directly
//
// Settings key: "privacyLevel"
func GetPrivacyLevel() Level {
	// Env-var overrides (most restrictive wins)
	if envSet("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC") {
		return Minimal
	}
	if envSet("DISABLE_TELEMETRY") {
		return Limited
	}

	// Explicit env level
	if v := os.Getenv("POLYPOD_PRIVACY_LEVEL"); v != "" {
		return ParseLevel(v)
	}

	// Settings file
	if settingsGetter != nil {
		if v := settingsGetter("privacyLevel"); v != "" {
			return ParseLevel(v)
		}
	}

	return Full
}

// -------------------------------------------------------------------
// Convenience predicates
// -------------------------------------------------------------------

// ShouldCollectTelemetry returns true when analytics/telemetry is allowed.
func ShouldCollectTelemetry() bool {
	return GetPrivacyLevel() == Full
}

// ShouldStoreHistory returns true when conversation/command history may be persisted.
func ShouldStoreHistory() bool {
	return GetPrivacyLevel() < Minimal
}

// ShouldShareContext returns true when context may be shared with the server
// beyond what's strictly necessary for inference.
func ShouldShareContext() bool {
	return GetPrivacyLevel() < Limited
}

// IsEssentialTrafficOnly returns true when all nonessential network traffic
// should be suppressed.
func IsEssentialTrafficOnly() bool {
	return GetPrivacyLevel() >= Minimal
}

// IsTelemetryDisabled returns true when telemetry is disabled (Limited or stricter).
func IsTelemetryDisabled() bool {
	return GetPrivacyLevel() >= Limited
}

// -------------------------------------------------------------------
// Data redaction
// -------------------------------------------------------------------

// RedactForPrivacy redacts fields in a string map according to the current
// privacy level. At Minimal or above, all values are replaced with "[redacted]".
// At Limited, only keys in sensitiveKeys are redacted.
func RedactForPrivacy(data map[string]string) map[string]string {
	level := GetPrivacyLevel()
	if level == Full {
		return data
	}
	out := make(map[string]string, len(data))
	for k, v := range data {
		switch {
		case level >= Minimal:
			out[k] = "[redacted]"
		case level >= Limited && isSensitiveKey(k):
			out[k] = "[redacted]"
		default:
			out[k] = v
		}
	}
	return out
}

// sensitiveKeys are redacted at the Limited level.
var sensitiveKeySet = map[string]bool{
	"email": true, "username": true, "hostname": true,
	"ip": true, "user_agent": true, "os_user": true,
	"machine_id": true, "fingerprint": true,
}

func isSensitiveKey(k string) bool {
	return sensitiveKeySet[strings.ToLower(k)]
}

// -------------------------------------------------------------------
// helpers
// -------------------------------------------------------------------

func envSet(key string) bool {
	v := os.Getenv(key)
	return v != "" && v != "0" && strings.ToLower(v) != "false"
}
