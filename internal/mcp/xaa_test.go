package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestExchangeForServer_Success — the happy path: the fake exchange
// endpoint returns a valid OAuth 2.0 token response and
// ExchangeForServer parses all fields correctly.
func TestExchangeForServer_Success(t *testing.T) {
	var gotBody XAATokenRequest
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected json content-type, got %q", ct)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "srv-token-abc",
			"token_type": "Bearer",
			"expires_in": 3600,
			"scope": "read write"
		}`))
	}))
	defer srv.Close()

	p := NewXAAProvider("claude-session-xyz", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := p.ExchangeForServer(ctx, "my-mcp-server", []string{"read", "write"})
	if err != nil {
		t.Fatalf("ExchangeForServer: %v", err)
	}
	if resp.AccessToken != "srv-token-abc" {
		t.Errorf("access_token = %q, want %q", resp.AccessToken, "srv-token-abc")
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("token_type = %q, want Bearer", resp.TokenType)
	}
	if resp.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", resp.ExpiresIn)
	}
	if resp.Scope != "read write" {
		t.Errorf("scope = %q, want %q", resp.Scope, "read write")
	}
	if resp.IssuedAt.IsZero() {
		t.Errorf("IssuedAt should be populated")
	}
	if got := resp.ExpiresAt(); got.IsZero() {
		t.Errorf("ExpiresAt should be populated for non-zero expires_in")
	}

	// Verify the request was shaped correctly.
	if gotBody.ClaudeToken != "claude-session-xyz" {
		t.Errorf("claude_token = %q, want %q", gotBody.ClaudeToken, "claude-session-xyz")
	}
	if gotBody.ServerAudience != "my-mcp-server" {
		t.Errorf("audience = %q, want %q", gotBody.ServerAudience, "my-mcp-server")
	}
	if len(gotBody.Scopes) != 2 || gotBody.Scopes[0] != "read" || gotBody.Scopes[1] != "write" {
		t.Errorf("scopes = %v, want [read write]", gotBody.Scopes)
	}
	if gotAuth != "Bearer claude-session-xyz" {
		t.Errorf("Authorization = %q, want bearer with claude token", gotAuth)
	}
}

// TestExchangeForServer_400Error — a 400 from the exchange endpoint
// surfaces as a non-nil error that is not a StepUpError.
func TestExchangeForServer_400Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"bad audience"}`))
	}))
	defer srv.Close()

	p := NewXAAProvider("tok", srv.URL)
	_, err := p.ExchangeForServer(context.Background(), "srv", nil)
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if _, ok := IsStepUpRequired(err); ok {
		t.Errorf("plain 400 should not be a step-up error: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error should mention HTTP 400: %v", err)
	}
}

// TestExchangeForServer_StepUp — a 401 with a WWW-Authenticate header
// carrying `insufficient_scope` converts to a *StepUpError with the
// parsed scope list and server name.
func TestExchangeForServer_StepUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="admin read"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewXAAProvider("tok", srv.URL)
	_, err := p.ExchangeForServer(context.Background(), "audit-server", []string{"read"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	stepUp, ok := IsStepUpRequired(err)
	if !ok {
		t.Fatalf("expected StepUpError, got %T: %v", err, err)
	}
	if stepUp.ServerName != "audit-server" {
		t.Errorf("ServerName = %q, want audit-server", stepUp.ServerName)
	}
	if len(stepUp.Required) != 2 || stepUp.Required[0] != "admin" || stepUp.Required[1] != "read" {
		t.Errorf("Required = %v, want [admin read]", stepUp.Required)
	}
}

// TestDetectStepUpFromResponse_JSONBody — the body-only parser
// recognises an OAuth error envelope.
func TestDetectStepUpFromResponse_JSONBody(t *testing.T) {
	body := []byte(`{"error":"insufficient_scope","scope":"read write"}`)
	err := DetectStepUpFromResponse(http.StatusForbidden, body)
	if err == nil {
		t.Fatal("expected step-up, got nil")
	}
	stepUp, ok := IsStepUpRequired(err)
	if !ok {
		t.Fatalf("expected StepUpError, got %T", err)
	}
	want := []string{"read", "write"}
	if len(stepUp.Required) != len(want) {
		t.Fatalf("Required = %v, want %v", stepUp.Required, want)
	}
	for i, s := range want {
		if stepUp.Required[i] != s {
			t.Errorf("Required[%d] = %q, want %q", i, stepUp.Required[i], s)
		}
	}
}

// TestDetectStepUpFromResponse_Unrelated — a generic 500 body is
// not a step-up.
func TestDetectStepUpFromResponse_Unrelated(t *testing.T) {
	if err := DetectStepUpFromResponse(http.StatusInternalServerError, []byte("boom")); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := DetectStepUpFromResponse(http.StatusOK, []byte(`{"error":"insufficient_scope"}`)); err != nil {
		t.Errorf("2xx should never be step-up: %v", err)
	}
}

// TestIsStepUpRequired_WrappedError — IsStepUpRequired must walk
// errors wrapped via fmt.Errorf("%w", ...).
func TestIsStepUpRequired_WrappedError(t *testing.T) {
	base := &StepUpError{Required: []string{"read"}, ServerName: "svr"}
	wrapped := fmt.Errorf("retry failed: %w", base)
	doubleWrapped := fmt.Errorf("outer: %w", wrapped)

	stepUp, ok := IsStepUpRequired(doubleWrapped)
	if !ok {
		t.Fatalf("expected step-up through wrapping, got ok=false")
	}
	if stepUp != base {
		t.Errorf("unwrapped error != base; got %+v", stepUp)
	}
	if stepUp.Error() == "" {
		t.Errorf("Error() should not be empty")
	}

	// A plain error must not match.
	if _, ok := IsStepUpRequired(errors.New("nope")); ok {
		t.Errorf("non-stepup error should not match")
	}
	if _, ok := IsStepUpRequired(nil); ok {
		t.Errorf("nil should not match")
	}
}

// TestStepUpError_ErrorMessage covers the string formatter for both
// "with scopes" and "without scopes" branches.
func TestStepUpError_ErrorMessage(t *testing.T) {
	withScopes := &StepUpError{Required: []string{"a", "b"}, ServerName: "s1"}
	if msg := withScopes.Error(); !strings.Contains(msg, "s1") || !strings.Contains(msg, "a b") {
		t.Errorf("unexpected message: %q", msg)
	}
	empty := &StepUpError{ServerName: "s2"}
	if msg := empty.Error(); !strings.Contains(msg, "s2") {
		t.Errorf("unexpected message: %q", msg)
	}
	var nilErr *StepUpError
	if msg := nilErr.Error(); msg == "" {
		t.Errorf("nil StepUpError Error() should still return a string")
	}
}

// TestXAACache_RoundTrip verifies Get/Put works for (server, scopes)
// and that scope ordering is normalised — [read write] and
// [write read] must hit the same entry.
func TestXAACache_RoundTrip(t *testing.T) {
	cache := NewXAACache()

	if _, ok := cache.Get("s1", []string{"read"}); ok {
		t.Fatalf("empty cache should miss")
	}

	resp := &XAATokenResponse{
		AccessToken: "abc",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
		IssuedAt:    time.Now(),
	}
	cache.Put("s1", []string{"read", "write"}, resp)

	got, ok := cache.Get("s1", []string{"read", "write"})
	if !ok {
		t.Fatalf("expected hit, got miss")
	}
	if got.AccessToken != "abc" {
		t.Errorf("AccessToken = %q, want abc", got.AccessToken)
	}

	// Reversed scope order must hit the same entry.
	got2, ok := cache.Get("s1", []string{"write", "read"})
	if !ok {
		t.Fatalf("reversed scopes should hit same entry")
	}
	if got2 != got {
		t.Errorf("reversed-scope Get returned a different pointer")
	}

	// Different server should miss.
	if _, ok := cache.Get("s2", []string{"read", "write"}); ok {
		t.Errorf("different server should miss")
	}
	// Different scope set should miss.
	if _, ok := cache.Get("s1", []string{"read"}); ok {
		t.Errorf("different scope set should miss")
	}
}

// TestXAACache_Expiration verifies that expired entries are not
// returned. We drive the cache clock manually to avoid real-time
// sleeps.
func TestXAACache_Expiration(t *testing.T) {
	cache := NewXAACache()
	cache.safetyMargin = 0 // disable for clearer arithmetic

	fakeNow := time.Unix(1_000_000, 0)
	cache.now = func() time.Time { return fakeNow }

	resp := &XAATokenResponse{
		AccessToken: "tok",
		ExpiresIn:   60,
		IssuedAt:    fakeNow,
	}
	cache.Put("s", []string{"x"}, resp)

	if _, ok := cache.Get("s", []string{"x"}); !ok {
		t.Fatalf("entry should be live immediately after Put")
	}

	// Advance past expiry.
	fakeNow = fakeNow.Add(61 * time.Second)
	if _, ok := cache.Get("s", []string{"x"}); ok {
		t.Errorf("entry should be expired after 61s")
	}

	// An entry with no expires_in is cached indefinitely.
	permanent := &XAATokenResponse{AccessToken: "perm"}
	cache.Put("p", nil, permanent)
	fakeNow = fakeNow.Add(1_000_000 * time.Second)
	if _, ok := cache.Get("p", nil); !ok {
		t.Errorf("entry with zero expires_in should never expire")
	}
}

// TestXAACache_SafetyMargin — the default 30s safety margin means a
// token expiring in 10s is already considered stale.
func TestXAACache_SafetyMargin(t *testing.T) {
	cache := NewXAACache()
	fakeNow := time.Unix(2_000_000, 0)
	cache.now = func() time.Time { return fakeNow }

	resp := &XAATokenResponse{
		AccessToken: "soon-dead",
		ExpiresIn:   10, // less than the 30s safety margin
		IssuedAt:    fakeNow,
	}
	cache.Put("s", []string{"x"}, resp)

	// expiresAt = now + 10s - 30s = now - 20s → already stale.
	if _, ok := cache.Get("s", []string{"x"}); ok {
		t.Errorf("token within safety margin should be treated as expired")
	}
}

// TestExchangeForServer_EmptyFields — defensive: nil provider and
// empty claude-token/exchange-URL all return specific errors.
func TestExchangeForServer_EmptyFields(t *testing.T) {
	var nilProv *XAAProvider
	if _, err := nilProv.ExchangeForServer(context.Background(), "s", nil); err == nil {
		t.Errorf("nil provider should error")
	}
	if _, err := (&XAAProvider{}).ExchangeForServer(context.Background(), "s", nil); err == nil {
		t.Errorf("empty provider should error")
	}
	if _, err := (&XAAProvider{ClaudeToken: "t"}).ExchangeForServer(context.Background(), "s", nil); err == nil {
		t.Errorf("missing exchange URL should error")
	}
}

// TestExchangeForServer_MissingAccessToken — a 200 with a body that
// omits access_token is a protocol violation.
func TestExchangeForServer_MissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"Bearer","expires_in":60}`))
	}))
	defer srv.Close()

	p := NewXAAProvider("tok", srv.URL)
	_, err := p.ExchangeForServer(context.Background(), "s", nil)
	if err == nil {
		t.Fatal("expected error for missing access_token")
	}
	if !strings.Contains(err.Error(), "access_token") {
		t.Errorf("error should mention access_token: %v", err)
	}
}
