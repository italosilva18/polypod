package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

func TestVaultCRUDAndEncryption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.enc")
	vault := NewVault(path, "test-passphrase")
	record := Record{
		Name:     "Email Principal",
		Value:    "senha-super-secreta",
		Username: "user@example.com",
		URL:      "https://example.com",
		Notes:    "conta pessoal",
	}
	if err := vault.Save(record); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := vault.Get("email principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != record.Value || got.Username != record.Username {
		t.Fatalf("Get = %#v", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), record.Value) || strings.Contains(string(raw), record.Username) {
		t.Fatal("encrypted vault contains plaintext secret data")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("mode = %o, want 600", gotMode)
	}

	records, err := vault.List()
	if err != nil || len(records) != 1 || records[0].Value != record.Value {
		t.Fatalf("List = %#v, %v", records, err)
	}
	if err := vault.Delete("EMAIL PRINCIPAL"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := vault.Get(record.Name); err == nil {
		t.Fatal("Get after Delete succeeded")
	}
}

func TestSecretSkillsExposePlaintextToAgent(t *testing.T) {
	vault := NewVault(filepath.Join(t.TempDir(), "secrets.enc"), "test-passphrase")
	reg := skill.NewRegistry()
	RegisterSkills(reg, vault)

	if _, err := reg.Execute("save_secret", `{"name":"portal","value":"open-value","username":"alice"}`); err != nil {
		t.Fatalf("save_secret: %v", err)
	}
	got, err := reg.Execute("get_secret", `{"name":"portal"}`)
	if err != nil {
		t.Fatalf("get_secret: %v", err)
	}
	if !strings.Contains(got, "value: open-value") || !strings.Contains(got, "username: alice") {
		t.Fatalf("get_secret output = %q", got)
	}
	listed, err := reg.Execute("list_secrets", `{}`)
	if err != nil || !strings.Contains(listed, "open-value") {
		t.Fatalf("list_secrets output = %q, %v", listed, err)
	}
}
