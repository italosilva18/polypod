package privacy

import (
	"testing"
)

// clearEnv unsets all privacy-related env vars and the settings getter so
// each test starts from a known Full baseline.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "")
	t.Setenv("DISABLE_TELEMETRY", "")
	t.Setenv("POLYPOD_PRIVACY_LEVEL", "")
	prev := settingsGetter
	settingsGetter = nil
	t.Cleanup(func() { settingsGetter = prev })
}

func TestLevelString(t *testing.T) {
	cases := []struct {
		level Level
		want  string
	}{
		{Full, "full"},
		{Limited, "limited"},
		{Minimal, "minimal"},
		{None, "none"},
		{Level(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.level.String(); got != c.want {
			t.Errorf("Level(%d).String() = %q, want %q", c.level, got, c.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"":                  Full,
		"full":              Full,
		"FULL":              Full,
		"  default  ":       Full,
		"limited":           Limited,
		"no-telemetry":      Limited,
		"minimal":           Minimal,
		"essential-traffic": Minimal,
		"none":              None,
		"garbage":           Full, // unknown defaults to Full
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGetPrivacyLevel_DefaultsToFull(t *testing.T) {
	clearEnv(t)
	if got := GetPrivacyLevel(); got != Full {
		t.Errorf("GetPrivacyLevel() = %v, want Full", got)
	}
}

func TestGetPrivacyLevel_EnvOverrides(t *testing.T) {
	t.Run("nonessential traffic -> Minimal", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "1")
		if got := GetPrivacyLevel(); got != Minimal {
			t.Errorf("got %v, want Minimal", got)
		}
	})

	t.Run("disable telemetry -> Limited", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DISABLE_TELEMETRY", "true")
		if got := GetPrivacyLevel(); got != Limited {
			t.Errorf("got %v, want Limited", got)
		}
	})

	t.Run("explicit POLYPOD_PRIVACY_LEVEL=none", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("POLYPOD_PRIVACY_LEVEL", "none")
		if got := GetPrivacyLevel(); got != None {
			t.Errorf("got %v, want None", got)
		}
	})

	t.Run("most restrictive wins (nonessential beats telemetry)", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "1")
		t.Setenv("DISABLE_TELEMETRY", "1")
		if got := GetPrivacyLevel(); got != Minimal {
			t.Errorf("got %v, want Minimal", got)
		}
	})

	t.Run("false/0 env values are ignored", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "false")
		t.Setenv("DISABLE_TELEMETRY", "0")
		if got := GetPrivacyLevel(); got != Full {
			t.Errorf("got %v, want Full", got)
		}
	})
}

func TestGetPrivacyLevel_SettingsFallback(t *testing.T) {
	clearEnv(t)
	SetSettingsGetter(func(key string) string {
		if key == "privacyLevel" {
			return "limited"
		}
		return ""
	})
	if got := GetPrivacyLevel(); got != Limited {
		t.Errorf("got %v, want Limited", got)
	}

	// Env must take precedence over settings.
	t.Setenv("POLYPOD_PRIVACY_LEVEL", "none")
	if got := GetPrivacyLevel(); got != None {
		t.Errorf("env did not override settings: got %v, want None", got)
	}
}

func TestPredicates(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(t *testing.T)
		telemetry   bool
		history     bool
		shareCtx    bool
		essOnly     bool
		telDisabled bool
	}{
		{
			name:  "Full",
			setup: func(t *testing.T) { clearEnv(t) },
			// defaults
			telemetry: true, history: true, shareCtx: true,
			essOnly: false, telDisabled: false,
		},
		{
			name: "Limited",
			setup: func(t *testing.T) {
				clearEnv(t)
				t.Setenv("DISABLE_TELEMETRY", "1")
			},
			telemetry: false, history: true, shareCtx: false,
			essOnly: false, telDisabled: true,
		},
		{
			name: "Minimal",
			setup: func(t *testing.T) {
				clearEnv(t)
				t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "1")
			},
			telemetry: false, history: false, shareCtx: false,
			essOnly: true, telDisabled: true,
		},
		{
			name: "None",
			setup: func(t *testing.T) {
				clearEnv(t)
				t.Setenv("POLYPOD_PRIVACY_LEVEL", "none")
			},
			telemetry: false, history: false, shareCtx: false,
			essOnly: true, telDisabled: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup(t)
			if got := ShouldCollectTelemetry(); got != c.telemetry {
				t.Errorf("ShouldCollectTelemetry() = %v, want %v", got, c.telemetry)
			}
			if got := ShouldStoreHistory(); got != c.history {
				t.Errorf("ShouldStoreHistory() = %v, want %v", got, c.history)
			}
			if got := ShouldShareContext(); got != c.shareCtx {
				t.Errorf("ShouldShareContext() = %v, want %v", got, c.shareCtx)
			}
			if got := IsEssentialTrafficOnly(); got != c.essOnly {
				t.Errorf("IsEssentialTrafficOnly() = %v, want %v", got, c.essOnly)
			}
			if got := IsTelemetryDisabled(); got != c.telDisabled {
				t.Errorf("IsTelemetryDisabled() = %v, want %v", got, c.telDisabled)
			}
		})
	}
}

func TestRedactForPrivacy_FullPassThrough(t *testing.T) {
	clearEnv(t)
	in := map[string]string{
		"email":    "alice@example.com",
		"hostname": "prod-db-01",
		"query":    "SELECT * FROM users",
	}
	out := RedactForPrivacy(in)
	for k, v := range in {
		if out[k] != v {
			t.Errorf("key %q: got %q, want %q (pass-through at Full)", k, out[k], v)
		}
	}
}

func TestRedactForPrivacy_LimitedRedactsSensitive(t *testing.T) {
	clearEnv(t)
	t.Setenv("DISABLE_TELEMETRY", "1")

	in := map[string]string{
		"email":       "bob@example.org",
		"username":    "bob",
		"hostname":    "web-server-42",
		"ip":          "192.168.1.100",
		"user_agent":  "Mozilla/5.0",
		"os_user":     "root",
		"machine_id":  "abc-123-def",
		"fingerprint": "sha256:deadbeef",
		"EMAIL":       "case-insensitive@example.com", // test case folding
		"query":       "SELECT * FROM accounts",       // non-sensitive
		"duration":    "123ms",                        // non-sensitive
	}
	out := RedactForPrivacy(in)

	sensitive := []string{
		"email", "username", "hostname", "ip", "user_agent",
		"os_user", "machine_id", "fingerprint", "EMAIL",
	}
	for _, k := range sensitive {
		if out[k] != "[redacted]" {
			t.Errorf("sensitive key %q: got %q, want %q", k, out[k], "[redacted]")
		}
	}

	// Non-sensitive keys retained at Limited.
	if out["query"] != "SELECT * FROM accounts" {
		t.Errorf("non-sensitive key query was redacted: %q", out["query"])
	}
	if out["duration"] != "123ms" {
		t.Errorf("non-sensitive key duration was redacted: %q", out["duration"])
	}
}

func TestRedactForPrivacy_MinimalRedactsAll(t *testing.T) {
	clearEnv(t)
	t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "1")

	in := map[string]string{
		"email":       "charlie@example.net",
		"credit_card": "4111-1111-1111-1111",
		"ssn":         "123-45-6789",
		"api_key":     "sk-live-abc123xyz",
		"password":    "hunter2",
		"duration":    "5s",
		"count":       "42",
	}
	out := RedactForPrivacy(in)

	if len(out) != len(in) {
		t.Fatalf("output has %d keys, want %d", len(out), len(in))
	}
	for k, v := range out {
		if v != "[redacted]" {
			t.Errorf("key %q: got %q, want %q", k, v, "[redacted]")
		}
	}
	// Ensure the original map is not mutated.
	if in["email"] != "charlie@example.net" {
		t.Errorf("input map was mutated: email=%q", in["email"])
	}
}

func TestRedactForPrivacy_NoneRedactsAll(t *testing.T) {
	clearEnv(t)
	t.Setenv("POLYPOD_PRIVACY_LEVEL", "none")

	in := map[string]string{
		"oauth_token":  "ghp_abcdef1234567890",
		"jwt":          "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.foo.bar",
		"private_key":  "-----BEGIN PRIVATE KEY-----",
		"session_id":   "sess_abcdef",
		"phone_number": "+1-555-123-4567",
	}
	out := RedactForPrivacy(in)
	for k, v := range out {
		if v != "[redacted]" {
			t.Errorf("key %q at level None: got %q, want %q", k, v, "[redacted]")
		}
	}
}

func TestRedactForPrivacy_EmptyMap(t *testing.T) {
	clearEnv(t)
	t.Setenv("POLYPOD_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	out := RedactForPrivacy(map[string]string{})
	if out == nil {
		t.Fatal("RedactForPrivacy(empty) returned nil; want non-nil empty map")
	}
	if len(out) != 0 {
		t.Errorf("got %d entries, want 0", len(out))
	}
}

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"email", "Email", "EMAIL",
		"username", "hostname", "ip",
		"user_agent", "os_user", "machine_id", "fingerprint",
	}
	for _, k := range sensitive {
		if !isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = false, want true", k)
		}
	}

	nonSensitive := []string{
		"query", "duration", "count", "status", "",
		"emailish", "user", "ipv6address",
	}
	for _, k := range nonSensitive {
		if isSensitiveKey(k) {
			t.Errorf("isSensitiveKey(%q) = true, want false", k)
		}
	}
}
