package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Test doubles ───────────────────────────────────────────────────────────

// fakeRefresher is a deterministic TokenRefresher for tests.
type fakeRefresher struct {
	tokens     []string // popped in order; last value is reused when exhausted
	expires    time.Time
	err        error
	calls      int32
	calledWith []context.Context
}

func (f *fakeRefresher) Refresh(ctx context.Context) (string, time.Time, error) {
	atomic.AddInt32(&f.calls, 1)
	f.calledWith = append(f.calledWith, ctx)
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	if len(f.tokens) == 0 {
		return "fallback-token", f.expires, nil
	}
	tok := f.tokens[0]
	if len(f.tokens) > 1 {
		f.tokens = f.tokens[1:]
	}
	return tok, f.expires, nil
}

func (f *fakeRefresher) Calls() int32 { return atomic.LoadInt32(&f.calls) }

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestOAuthRetryClient_HappyPath(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()

	const server = "test.mcp.example.com"
	if err := store.SetAccessToken(ctx, server, "initial-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	refresher := &fakeRefresher{tokens: []string{"never-used"}}
	client := NewOAuthRetryClient(server, OAuthRetryConfig{
		Store:     store,
		Refresher: refresher,
	})

	var fnCalls int
	var seenToken string
	err := client.Execute(ctx, func(token string) error {
		fnCalls++
		seenToken = token
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fnCalls != 1 {
		t.Errorf("fn called %d times, want 1", fnCalls)
	}
	if seenToken != "initial-token" {
		t.Errorf("fn saw token %q, want %q", seenToken, "initial-token")
	}
	if got := refresher.Calls(); got != 0 {
		t.Errorf("refresher called %d times, want 0", got)
	}
}

func TestOAuthRetryClient_Retries_On401_Success(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()

	const server = "test.mcp.example.com"
	_ = store.SetAccessToken(ctx, server, "stale-token", time.Now().Add(-time.Minute))

	expiry := time.Now().Add(90 * time.Minute)
	refresher := &fakeRefresher{tokens: []string{"fresh-token"}, expires: expiry}
	client := NewOAuthRetryClient(server, OAuthRetryConfig{
		Store:     store,
		Refresher: refresher,
	})

	var calls int
	var sawTokens []string
	err := client.Execute(ctx, func(token string) error {
		calls++
		sawTokens = append(sawTokens, token)
		if calls == 1 {
			// Simulate a 401 from streamablehttp.go, which wraps
			// ErrStreamableHTTPUnauthorized.
			return fmt.Errorf("%w: body", ErrStreamableHTTPUnauthorized)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2", calls)
	}
	if len(sawTokens) != 2 || sawTokens[0] != "stale-token" || sawTokens[1] != "fresh-token" {
		t.Errorf("fn saw tokens %v, want [stale-token fresh-token]", sawTokens)
	}
	if got := refresher.Calls(); got != 1 {
		t.Errorf("refresher called %d times, want 1", got)
	}
	// Store should now hold the fresh token.
	stored, _ := store.GetAccessToken(ctx, server)
	if stored != "fresh-token" {
		t.Errorf("stored token = %q, want fresh-token", stored)
	}
	if exp, ok := store.Expiry(server); !ok || !exp.Equal(expiry) {
		t.Errorf("stored expiry = %v, want %v", exp, expiry)
	}
}

func TestOAuthRetryClient_Retries_Exhausted(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()

	const server = "persistent401.mcp.example.com"
	_ = store.SetAccessToken(ctx, server, "stale-token", time.Time{})

	refresher := &fakeRefresher{tokens: []string{"another-token"}, expires: time.Now().Add(time.Hour)}
	client := NewOAuthRetryClient(server, OAuthRetryConfig{
		Store:     store,
		Refresher: refresher,
		// Explicit MaxRetries=1 to assert the default.
		MaxRetries: 1,
	})

	var calls int
	err := client.Execute(ctx, func(token string) error {
		calls++
		return errors.New("HTTP 401 Unauthorized: still bad")
	})
	if err == nil {
		t.Fatalf("Execute: expected error, got nil")
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2 (initial + 1 retry)", calls)
	}
	if !IsUnauthorizedError(err) {
		t.Errorf("final error should still be classified unauthorized: %v", err)
	}
	if got := refresher.Calls(); got != 1 {
		t.Errorf("refresher called %d times, want 1", got)
	}
}

func TestOAuthRetryClient_MultipleRetries(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()

	const server = "multi.mcp.example.com"
	_ = store.SetAccessToken(ctx, server, "v0", time.Time{})

	refresher := &fakeRefresher{tokens: []string{"v1", "v2", "v3"}, expires: time.Now().Add(time.Hour)}
	client := NewOAuthRetryClient(server, OAuthRetryConfig{
		Store:      store,
		Refresher:  refresher,
		MaxRetries: 3,
	})

	var calls int
	err := client.Execute(ctx, func(token string) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("%w: retry me", ErrUnauthorized401)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calls != 3 {
		t.Errorf("fn called %d times, want 3", calls)
	}
}

func TestOAuthRetryClient_NonUnauthorizedPassThrough(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()
	_ = store.SetAccessToken(ctx, "srv", "tok", time.Time{})

	refresher := &fakeRefresher{}
	client := NewOAuthRetryClient("srv", OAuthRetryConfig{
		Store:     store,
		Refresher: refresher,
	})

	boom := errors.New("connection reset by peer")
	err := client.Execute(ctx, func(token string) error { return boom })
	if !errors.Is(err, boom) {
		t.Errorf("Execute err = %v, want %v", err, boom)
	}
	if got := refresher.Calls(); got != 0 {
		t.Errorf("refresher unexpectedly called %d times", got)
	}
}

func TestOAuthRetryClient_RefresherError(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()
	_ = store.SetAccessToken(ctx, "srv", "tok", time.Time{})

	refresherErr := errors.New("idp unreachable")
	refresher := &fakeRefresher{err: refresherErr}
	client := NewOAuthRetryClient("srv", OAuthRetryConfig{
		Store:     store,
		Refresher: refresher,
	})

	err := client.Execute(ctx, func(token string) error {
		return fmt.Errorf("%w: boom", ErrUnauthorized401)
	})
	if err == nil {
		t.Fatalf("Execute: expected error, got nil")
	}
	if !errors.Is(err, refresherErr) {
		t.Errorf("Execute err = %v, should wrap refresher error", err)
	}
}

func TestOAuthRetryClient_NoRefresherConfigured(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()
	_ = store.SetAccessToken(ctx, "srv", "tok", time.Time{})

	client := NewOAuthRetryClient("srv", OAuthRetryConfig{Store: store})
	orig := fmt.Errorf("%w: nope", ErrUnauthorized401)
	err := client.Execute(ctx, func(token string) error { return orig })
	if err == nil {
		t.Fatalf("Execute: expected error, got nil")
	}
	if !errors.Is(err, orig) {
		t.Errorf("Execute err should wrap original 401: %v", err)
	}
}

func TestIsUnauthorizedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("connection refused"), false},
		{"sentinel ErrUnauthorized401", ErrUnauthorized401, true},
		{"wrapped ErrUnauthorized401", fmt.Errorf("mcp call: %w", ErrUnauthorized401), true},
		{"streamable-http sentinel", ErrStreamableHTTPUnauthorized, true},
		{"wrapped streamable-http sentinel", fmt.Errorf("send: %w: body", ErrStreamableHTTPUnauthorized), true},
		{"message contains 401", errors.New("HTTP 401 from upstream"), true},
		{"message contains Unauthorized", errors.New("Unauthorized: token rejected"), true},
		{"message contains unauthenticated", errors.New("rpc: unauthenticated"), true},
		{"message contains invalid_token", errors.New(`{"error":"invalid_token"}`), true},
		{"message contains token expired", errors.New("token expired at 2025-01-01"), true},
		{"case-insensitive UNAUTHORIZED", errors.New("UNAUTHORIZED REQUEST"), true},
		{"no keywords", errors.New("internal server error"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnauthorizedError(tc.err); got != tc.want {
				t.Errorf("IsUnauthorizedError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMemoryTokenStore_Roundtrip(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryTokenStore()

	// Missing key returns empty, not error.
	tok, err := s.GetAccessToken(ctx, "nope")
	if err != nil {
		t.Errorf("GetAccessToken on missing key: %v", err)
	}
	if tok != "" {
		t.Errorf("GetAccessToken on missing key = %q, want empty", tok)
	}

	exp := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	if err := s.SetAccessToken(ctx, "srv", "abc", exp); err != nil {
		t.Fatalf("SetAccessToken: %v", err)
	}
	tok, err = s.GetAccessToken(ctx, "srv")
	if err != nil || tok != "abc" {
		t.Errorf("GetAccessToken: tok=%q err=%v, want abc/nil", tok, err)
	}
	if gotExp, ok := s.Expiry("srv"); !ok || !gotExp.Equal(exp) {
		t.Errorf("Expiry = %v/%v, want %v/true", gotExp, ok, exp)
	}

	// Overwrite.
	if err := s.SetAccessToken(ctx, "srv", "def", time.Time{}); err != nil {
		t.Fatalf("SetAccessToken overwrite: %v", err)
	}
	tok, _ = s.GetAccessToken(ctx, "srv")
	if tok != "def" {
		t.Errorf("after overwrite tok = %q, want def", tok)
	}
}

func TestOAuthRetryClient_NilFn(t *testing.T) {
	client := NewOAuthRetryClient("srv", OAuthRetryConfig{Store: NewMemoryTokenStore()})
	if err := client.Execute(context.Background(), nil); err == nil {
		t.Errorf("expected error for nil fn")
	}
}

func TestOAuthRetryClient_NilStore(t *testing.T) {
	client := NewOAuthRetryClient("srv", OAuthRetryConfig{})
	err := client.Execute(context.Background(), func(string) error { return nil })
	if err == nil {
		t.Errorf("expected error for nil store")
	}
}

func TestOAuthRetryClient_Server(t *testing.T) {
	c := NewOAuthRetryClient("my-server", OAuthRetryConfig{Store: NewMemoryTokenStore()})
	if got := c.Server(); got != "my-server" {
		t.Errorf("Server() = %q, want my-server", got)
	}
}
