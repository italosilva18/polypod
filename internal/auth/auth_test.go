package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

// clearProviderEnv zeroes every provider env var so tests that exercise
// AutoDetect / GetCredentials env fallback aren't polluted by the host.
// t.Setenv("X", "") effectively unsets the variable for the code under test,
// because the implementation checks `os.Getenv(v) != ""`.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for envVar := range EnvVarProviderMap {
		t.Setenv(envVar, "")
	}
}

// ─── KeyStore ───────────────────────────────────────────────────────────────

func TestKeyStore_SetGetRoundtrip(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	if err := ks.SetKey("deepseek", "sk-test123"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	got, err := ks.GetKey("deepseek")
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got != "sk-test123" {
		t.Errorf("GetKey = %q, want %q", got, "sk-test123")
	}
}

func TestKeyStore_GetKey_Missing(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	// Observed behaviour: when no key has ever been stored, the file-backed
	// fallback surfaces a "no such file" error from the underlying
	// os.ReadFile, because FileStorage.Get does not mask os.IsNotExist.
	// Either (a) empty string with nil error (if a keychain primary handled
	// the read) or (b) an os.IsNotExist error is acceptable. The value must
	// always be empty.
	got, err := ks.GetKey("nonexistent-provider")
	if got != "" {
		t.Errorf("GetKey on missing provider = %q, want empty", got)
	}
	if err != nil && !os.IsNotExist(err) {
		t.Errorf("GetKey on missing provider returned unexpected error: %v", err)
	}
}

func TestKeyStore_DeleteKey(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	if err := ks.SetKey("openai", "sk-del"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	if got, _ := ks.GetKey("openai"); got != "sk-del" {
		t.Fatalf("precondition: GetKey = %q, want %q", got, "sk-del")
	}

	if err := ks.DeleteKey("openai"); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}

	// Delete removes the last entry and unlinks credentials.enc; the next
	// Get therefore sees "no such file". Accept either an IsNotExist error
	// or a clean empty-string response. The value must be empty in both.
	got, err := ks.GetKey("openai")
	if got != "" {
		t.Errorf("GetKey after Delete = %q, want empty", got)
	}
	if err != nil && !os.IsNotExist(err) {
		t.Errorf("GetKey after Delete returned unexpected error: %v", err)
	}
}

func TestKeyStore_DeleteKey_Idempotent(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	// Deleting a key that was never stored should not error (file missing case
	// short-circuits to nil in FileStorage; keychain primaries typically return
	// an error but the fallback tolerates it).
	if err := ks.DeleteKey("never-existed"); err != nil {
		// On platforms with a working keychain primary this could return an
		// error wrapped by FallbackStorage. We tolerate either outcome but
		// log so flakes are visible.
		t.Logf("DeleteKey on unknown provider returned: %v (tolerated)", err)
	}
}

func TestKeyStore_ListProviders(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	wanted := []string{"anthropic", "openai", "deepseek"}
	for _, p := range wanted {
		if err := ks.SetKey(p, "sk-"+p); err != nil {
			t.Fatalf("SetKey(%q): %v", p, err)
		}
	}

	got := ks.ListProviders()
	sort.Strings(got)

	want := append([]string(nil), wanted...)
	sort.Strings(want)

	if len(got) < len(want) {
		t.Fatalf("ListProviders = %v, want at least %v", got, want)
	}

	// Every requested provider must be present (the list may contain extras
	// if the keychain primary persists sibling entries, but it must not miss
	// any we just stored).
	have := make(map[string]bool, len(got))
	for _, p := range got {
		have[p] = true
	}
	for _, p := range want {
		if !have[p] {
			t.Errorf("ListProviders missing %q (got %v)", p, got)
		}
	}
}

func TestKeyStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	ks1 := NewKeyStore(dir)
	if err := ks1.SetKey("anthropic", "sk-ant-test"); err != nil {
		t.Fatalf("ks1.SetKey: %v", err)
	}

	// A fresh KeyStore backed by the same directory must see the previously
	// stored key (via file-storage fallback, or via the keychain primary
	// which is process-independent).
	ks2 := NewKeyStore(dir)
	got, err := ks2.GetKey("anthropic")
	if err != nil {
		t.Fatalf("ks2.GetKey: %v", err)
	}
	if got != "sk-ant-test" {
		t.Errorf("persisted key = %q, want %q", got, "sk-ant-test")
	}
}

func TestKeyStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode semantics do not apply on Windows")
	}

	dir := t.TempDir()
	ks := NewKeyStore(dir)

	if err := ks.SetKey("deepseek", "sk-perm-check"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	credPath := filepath.Join(dir, "credentials.enc")
	info, err := os.Stat(credPath)
	if err != nil {
		// If the primary keychain backend (secret-tool / security) is
		// available, the file fallback is never written. That's a valid
		// code path — skip rather than fail.
		if os.IsNotExist(err) {
			t.Skipf("credentials.enc not present; keychain primary handled the write (%v)", err)
		}
		t.Fatalf("stat credentials.enc: %v", err)
	}

	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("credentials.enc mode = %o, want 0600", mode)
	}
}

func TestKeyStore_OverwriteExistingKey(t *testing.T) {
	ks := NewKeyStore(t.TempDir())

	if err := ks.SetKey("openai", "sk-old"); err != nil {
		t.Fatalf("SetKey old: %v", err)
	}
	if err := ks.SetKey("openai", "sk-new"); err != nil {
		t.Fatalf("SetKey new: %v", err)
	}

	got, err := ks.GetKey("openai")
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got != "sk-new" {
		t.Errorf("GetKey = %q, want %q (overwrite failed)", got, "sk-new")
	}
}

// ─── Credentials struct ─────────────────────────────────────────────────────

func TestCredentials_Token_PrefersOAuth(t *testing.T) {
	c := &Credentials{
		Provider:   AuthProviderOAuth,
		APIKey:     "sk-api",
		OAuthToken: "oauth-xyz",
	}
	if got := c.Token(); got != "oauth-xyz" {
		t.Errorf("Token() = %q, want OAuth token %q", got, "oauth-xyz")
	}
}

func TestCredentials_Token_FallsBackToAPIKey(t *testing.T) {
	c := &Credentials{
		Provider: AuthProviderAPIKey,
		APIKey:   "sk-only",
	}
	if got := c.Token(); got != "sk-only" {
		t.Errorf("Token() = %q, want %q", got, "sk-only")
	}
}

func TestCredentials_IsExpired(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"zero time is not expired", time.Time{}, false},
		{"future is not expired", time.Now().Add(1 * time.Hour), false},
		{"past is expired", time.Now().Add(-1 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Credentials{ExpiresAt: tc.expires}
			if got := c.IsExpired(); got != tc.want {
				t.Errorf("IsExpired() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ─── MultiProviderAuth ──────────────────────────────────────────────────────

func TestMultiProviderAuth_NewMultiProviderAuth(t *testing.T) {
	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)
	if auth == nil {
		t.Fatal("NewMultiProviderAuth returned nil")
	}
	if auth.keyStore != ks {
		t.Error("NewMultiProviderAuth did not retain the provided KeyStore")
	}
	if auth.cache == nil {
		t.Error("NewMultiProviderAuth did not initialise cache map")
	}
}

func TestMultiProviderAuth_SetGetCredentials(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	in := Credentials{
		Provider: AuthProviderAPIKey,
		APIKey:   "sk-mpa-test",
	}
	if err := auth.SetCredentials("deepseek", in); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}

	got, err := auth.GetCredentials("deepseek")
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got == nil {
		t.Fatal("GetCredentials returned nil creds")
	}
	if got.APIKey != "sk-mpa-test" {
		t.Errorf("APIKey = %q, want %q", got.APIKey, "sk-mpa-test")
	}

	// SetCredentials must also persist to the KeyStore.
	stored, err := ks.GetKey("deepseek")
	if err != nil {
		t.Fatalf("ks.GetKey: %v", err)
	}
	if stored != "sk-mpa-test" {
		t.Errorf("keystore value = %q, want %q", stored, "sk-mpa-test")
	}
}

func TestMultiProviderAuth_GetCredentials_NotFound(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	_, err := auth.GetCredentials("nonexistent")
	if err == nil {
		t.Error("GetCredentials on unknown provider: expected error, got nil")
	}
}

func TestMultiProviderAuth_GetCredentials_FromKeyStore(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	if err := ks.SetKey("openai", "sk-from-ks"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	auth := NewMultiProviderAuth(ks)
	got, err := auth.GetCredentials("openai")
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.APIKey != "sk-from-ks" {
		t.Errorf("APIKey = %q, want %q", got.APIKey, "sk-from-ks")
	}
	if got.Provider != AuthProviderAPIKey {
		t.Errorf("Provider = %q, want %q", got.Provider, AuthProviderAPIKey)
	}
}

func TestMultiProviderAuth_GetCredentials_FromEnvVar(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-env")

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	got, err := auth.GetCredentials("anthropic")
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.APIKey != "sk-ant-env" {
		t.Errorf("APIKey = %q, want %q", got.APIKey, "sk-ant-env")
	}
	if got.Provider != AuthProviderEnvVar {
		t.Errorf("Provider = %q, want %q", got.Provider, AuthProviderEnvVar)
	}
}

func TestMultiProviderAuth_DeleteCredentials(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	if err := auth.SetCredentials("openai", Credentials{
		Provider: AuthProviderAPIKey,
		APIKey:   "sk-del-me",
	}); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}

	if _, err := auth.GetCredentials("openai"); err != nil {
		t.Fatalf("precondition GetCredentials: %v", err)
	}

	if err := auth.DeleteCredentials("openai"); err != nil {
		t.Fatalf("DeleteCredentials: %v", err)
	}

	// Cache must be cleared and the keystore entry gone. With no env var
	// set (clearProviderEnv) this must now return an error.
	if _, err := auth.GetCredentials("openai"); err == nil {
		t.Error("GetCredentials after Delete: expected error, got nil")
	}
}

func TestMultiProviderAuth_ClearCache(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	auth.cache["openai"] = &Credentials{APIKey: "cached"}
	auth.ClearCache()
	if len(auth.cache) != 0 {
		t.Errorf("cache not cleared: %v", auth.cache)
	}
}

// ─── AutoDetect ─────────────────────────────────────────────────────────────

func TestMultiProviderAuth_AutoDetect_Anthropic(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-auto")

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	detected := auth.AutoDetect()
	if !detected["anthropic"] {
		t.Errorf("anthropic not detected; got %v", detected)
	}
	if detected["openai"] {
		t.Errorf("openai unexpectedly detected; got %v", detected)
	}
}

func TestMultiProviderAuth_AutoDetect_OpenAI(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai-auto")

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	detected := auth.AutoDetect()
	if !detected["openai"] {
		t.Errorf("openai not detected; got %v", detected)
	}
}

func TestMultiProviderAuth_AutoDetect_NoEnv(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	auth := NewMultiProviderAuth(ks)

	detected := auth.AutoDetect()
	// No env vars set and an empty keystore ⇒ every provider must be absent
	// or explicitly false. An entirely empty map is also acceptable.
	for prov, ok := range detected {
		if ok {
			t.Errorf("expected no detections, but %q was detected", prov)
		}
	}
}

func TestMultiProviderAuth_AutoDetect_IncludesKeyStore(t *testing.T) {
	clearProviderEnv(t)

	ks := NewKeyStore(t.TempDir())
	if err := ks.SetKey("deepseek", "sk-ds-ks"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	auth := NewMultiProviderAuth(ks)
	detected := auth.AutoDetect()
	if !detected["deepseek"] {
		t.Errorf("deepseek (from keystore) not detected; got %v", detected)
	}
}

func TestMultiProviderAuth_AutoDetect_MergesEnvAndKeyStore(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-openai-env")

	ks := NewKeyStore(t.TempDir())
	if err := ks.SetKey("anthropic", "sk-ant-ks"); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	auth := NewMultiProviderAuth(ks)
	detected := auth.AutoDetect()

	if !detected["openai"] {
		t.Errorf("openai (env) not detected; got %v", detected)
	}
	if !detected["anthropic"] {
		t.Errorf("anthropic (keystore) not detected; got %v", detected)
	}
}

// ─── Provider maps ──────────────────────────────────────────────────────────

func TestEnvVarProviderMap_BidirectionalConsistency(t *testing.T) {
	for envVar, prov := range EnvVarProviderMap {
		reverse, ok := ProviderEnvVarMap[prov]
		if !ok {
			t.Errorf("ProviderEnvVarMap missing provider %q (from env var %q)", prov, envVar)
			continue
		}
		if reverse != envVar {
			t.Errorf("ProviderEnvVarMap[%q] = %q, want %q", prov, reverse, envVar)
		}
	}
	for prov, envVar := range ProviderEnvVarMap {
		if got := EnvVarProviderMap[envVar]; got != prov {
			t.Errorf("EnvVarProviderMap[%q] = %q, want %q", envVar, got, prov)
		}
	}
}
