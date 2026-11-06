package chatgptauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTripEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chatgpt-auth.enc")
	store := NewStore(path)
	want := &Credential{
		Email: "agent@example.test", Issuer: issuerURL, Subject: "subject",
		ClientID: "oaiapp_test", HostID: "urn:uuid:test", IDToken: "id.secret",
		AccessToken: "access.secret", RefreshToken: "refresh.secret",
		Scopes: []string{requiredScope}, ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.ClientID != want.ClientID {
		t.Fatalf("credencial recuperada difere da salva")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), want.AccessToken) || strings.Contains(string(raw), want.RefreshToken) {
		t.Fatal("tokens foram persistidos em texto claro")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissao = %o, esperado 600", info.Mode().Perm())
	}
}

func TestCredentialRefreshWindow(t *testing.T) {
	if !(&Credential{AccessToken: "x", ExpiresAt: time.Now().Add(time.Minute)}).NeedsRefresh() {
		t.Fatal("token proximo da expiracao deveria renovar")
	}
	if (&Credential{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)}).NeedsRefresh() {
		t.Fatal("token valido nao deveria renovar")
	}
}

func TestModelNamePrefersSlug(t *testing.T) {
	if got := (Model{ID: "legacy", Slug: "gpt-current"}).Name(); got != "gpt-current" {
		t.Fatalf("Name() = %q", got)
	}
}
