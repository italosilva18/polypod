package setup

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/config"
)

// -----------------------------------------------------------------------------
// validateKey
// -----------------------------------------------------------------------------

func TestValidateKey_EmptyInputs(t *testing.T) {
	if validateKey("", "k", "m") {
		t.Error("expected false for empty baseURL")
	}
	if validateKey("http://x", "", "m") {
		t.Error("expected false for empty apiKey")
	}
}

func TestValidateKey_AcceptsOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("unexpected auth header: %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("unexpected content-type: %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if !validateKey(srv.URL, "sk-test", "my-model") {
		t.Error("expected true for 200 response")
	}
}

func TestValidateKey_RejectsOnAuthFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		status := status
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		if validateKey(srv.URL, "sk-bad", "m") {
			t.Errorf("expected false for status %d", status)
		}
		srv.Close()
	}
}

func TestValidateKey_AcceptsNon401Errors(t *testing.T) {
	// Per the implementation, only 401/403 are treated as invalid.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if !validateKey(srv.URL, "sk-test", "m") {
		t.Error("expected true for 500 (only 401/403 count as invalid)")
	}
}

func TestValidateKey_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !validateKey(srv.URL+"/", "sk", "m") {
		t.Error("expected true")
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path not normalized, got %q", gotPath)
	}
}

func TestValidateKey_DefaultsModelWhenEmpty(t *testing.T) {
	// With model == "", the request body should reference "deepseek-chat".
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !validateKey(srv.URL, "sk", "") {
		t.Error("expected true")
	}
	if !strings.Contains(gotBody, `"model":"deepseek-chat"`) {
		t.Errorf("expected default model in body, got: %s", gotBody)
	}
}

func TestValidateKey_NetworkError(t *testing.T) {
	// Port 1 on localhost should fail to connect.
	if validateKey("http://127.0.0.1:1", "sk", "m") {
		t.Error("expected false on network error")
	}
}

// -----------------------------------------------------------------------------
// needsAPIKey
// -----------------------------------------------------------------------------

func TestNeedsAPIKey_EmptyKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.AI.APIKey = ""
	if !needsAPIKey(cfg) {
		t.Error("expected true when APIKey is empty")
	}
}

func TestNeedsAPIKey_EmptyBaseURLAssumedValid(t *testing.T) {
	cfg := &config.Config{}
	cfg.AI.APIKey = "sk-something"
	cfg.AI.BaseURL = ""
	if needsAPIKey(cfg) {
		t.Error("expected false when BaseURL is empty (assume valid)")
	}
}

func TestNeedsAPIKey_ValidRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.AI.APIKey = "sk-good"
	cfg.AI.BaseURL = srv.URL
	cfg.AI.Model = "m"
	if needsAPIKey(cfg) {
		t.Error("expected false when remote accepts the key")
	}
}

func TestNeedsAPIKey_InvalidRemote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.AI.APIKey = "sk-bad"
	cfg.AI.BaseURL = srv.URL
	cfg.AI.Model = "m"
	if !needsAPIKey(cfg) {
		t.Error("expected true when remote rejects the key")
	}
}

// -----------------------------------------------------------------------------
// generateAPIKey
// -----------------------------------------------------------------------------

func TestGenerateAPIKey_FormatAndUniqueness(t *testing.T) {
	k1 := generateAPIKey()
	k2 := generateAPIKey()
	if !strings.HasPrefix(k1, "pk-") {
		t.Errorf("expected prefix pk-, got %q", k1)
	}
	// "pk-" + 32 hex chars = 35
	if len(k1) != 35 {
		t.Errorf("expected length 35, got %d (%q)", len(k1), k1)
	}
	if k1 == k2 {
		t.Error("expected two generated keys to differ")
	}
}

// -----------------------------------------------------------------------------
// providers table
// -----------------------------------------------------------------------------

func TestProviders_AllFieldsPopulated(t *testing.T) {
	if len(providers) < 4 {
		t.Fatalf("expected at least 4 providers, got %d", len(providers))
	}
	seen := map[string]bool{}
	for i, p := range providers {
		if p.Name == "" {
			t.Errorf("providers[%d] missing Name", i)
		}
		if !strings.HasPrefix(p.BaseURL, "http") {
			t.Errorf("providers[%d] BaseURL not an http URL: %q", i, p.BaseURL)
		}
		if p.Model == "" {
			t.Errorf("providers[%d] missing Model", i)
		}
		if seen[p.Name] {
			t.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
	}
}

// -----------------------------------------------------------------------------
// askString / askBool / askChoice with an injected scanner
// These helpers take a *bufio.Scanner explicitly, so they're safe to exercise
// without touching real stdin.
// -----------------------------------------------------------------------------

func newScanner(input string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(input))
}

// redirectStdout pipes os.Stdout to /dev/null for the duration of fn so
// prompt output doesn't pollute test logs.
func silenceStdout(t *testing.T, fn func()) {
	t.Helper()
	orig := os.Stdout
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = null
	defer func() {
		os.Stdout = orig
		null.Close()
	}()
	fn()
}

func TestAskString_UsesInputWhenProvided(t *testing.T) {
	var got string
	silenceStdout(t, func() {
		got = askString(newScanner("hello\n"), "prompt", "def")
	})
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestAskString_FallsBackToDefault(t *testing.T) {
	var got string
	silenceStdout(t, func() {
		got = askString(newScanner("\n"), "prompt", "def")
	})
	if got != "def" {
		t.Errorf("expected 'def', got %q", got)
	}
}

func TestAskBool_Defaults(t *testing.T) {
	cases := []struct {
		input   string
		def     bool
		want    bool
		comment string
	}{
		{"\n", true, true, "empty -> default true"},
		{"\n", false, false, "empty -> default false"},
		{"s\n", false, true, "s -> true"},
		{"sim\n", false, true, "sim -> true"},
		{"y\n", false, true, "y -> true"},
		{"yes\n", false, true, "yes -> true"},
		{"n\n", true, false, "n -> false"},
		{"no\n", true, false, "no -> false"},
	}
	for _, tc := range cases {
		var got bool
		silenceStdout(t, func() {
			got = askBool(newScanner(tc.input), "q?", tc.def)
		})
		if got != tc.want {
			t.Errorf("%s: got %v want %v", tc.comment, got, tc.want)
		}
	}
}

func TestAskChoice_ValidAndInvalid(t *testing.T) {
	opts := []string{"a", "b", "c"}

	var got int
	silenceStdout(t, func() {
		got = askChoice(newScanner("2\n"), "pick", opts, 1)
	})
	if got != 2 {
		t.Errorf("expected 2, got %d", got)
	}

	silenceStdout(t, func() {
		got = askChoice(newScanner("\n"), "pick", opts, 3)
	})
	if got != 3 {
		t.Errorf("expected default 3, got %d", got)
	}

	// Out of range -> default
	silenceStdout(t, func() {
		got = askChoice(newScanner("99\n"), "pick", opts, 1)
	})
	if got != 1 {
		t.Errorf("expected default 1 for out-of-range, got %d", got)
	}

	// Non-numeric -> default
	silenceStdout(t, func() {
		got = askChoice(newScanner("abc\n"), "pick", opts, 2)
	})
	if got != 2 {
		t.Errorf("expected default 2 for non-numeric, got %d", got)
	}
}

// -----------------------------------------------------------------------------
// Config save round-trip (simulates the write step that CheckAPIKey/Run perform)
// -----------------------------------------------------------------------------

func TestConfigSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "polypod.yaml")

	cfg := config.DefaultConfig()
	cfg.AI.Provider = "deepseek"
	cfg.AI.BaseURL = providers[0].BaseURL
	cfg.AI.Model = providers[0].Model
	cfg.AI.APIKey = "sk-testkey"

	data, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AI.APIKey != "sk-testkey" {
		t.Errorf("APIKey not persisted, got %q", loaded.AI.APIKey)
	}
	if loaded.AI.Provider != "deepseek" {
		t.Errorf("Provider not persisted, got %q", loaded.AI.Provider)
	}
	if loaded.AI.BaseURL != providers[0].BaseURL {
		t.Errorf("BaseURL not persisted, got %q", loaded.AI.BaseURL)
	}
}
