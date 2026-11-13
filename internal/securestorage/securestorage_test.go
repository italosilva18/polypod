package securestorage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// newTestFileStorage returns a FileStorage rooted in t.TempDir() with a
// deterministic passphrase so tests never touch the OS machine-id.
func newTestFileStorage(t *testing.T) (*FileStorage, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	return NewFileStorage(path, "test-passphrase-do-not-use-in-prod"), path
}

// -------------------------------------------------------------------
// FileStorage – Set / Get / Delete round-trip
// -------------------------------------------------------------------

func TestFileStorage_SetGetRoundTrip(t *testing.T) {
	fs, _ := newTestFileStorage(t)

	if err := fs.Set("api_key", "sk-secret-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := fs.Get("api_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "sk-secret-123" {
		t.Errorf("Get = %q, want %q", got, "sk-secret-123")
	}
}

func TestFileStorage_SetOverwritesExistingKey(t *testing.T) {
	fs, _ := newTestFileStorage(t)

	if err := fs.Set("token", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := fs.Set("token", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := fs.Get("token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v2" {
		t.Errorf("Get = %q, want %q", got, "v2")
	}
}

func TestFileStorage_DeleteRemovesKey(t *testing.T) {
	fs, path := newTestFileStorage(t)

	// Set two keys so deleting one does not empty (and therefore remove) the file.
	if err := fs.Set("doomed", "bye"); err != nil {
		t.Fatalf("Set doomed: %v", err)
	}
	if err := fs.Set("keep", "stay"); err != nil {
		t.Fatalf("Set keep: %v", err)
	}
	if err := fs.Delete("doomed"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// The file should still exist because "keep" remains.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat after Delete: %v", err)
	}

	// "doomed" must now be absent — Get returns "" per the interface contract.
	got, err := fs.Get("doomed")
	if err != nil {
		t.Fatalf("Get after Delete: unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("Get doomed = %q, want empty string", got)
	}

	// "keep" must still be retrievable.
	got, err = fs.Get("keep")
	if err != nil {
		t.Fatalf("Get keep: %v", err)
	}
	if got != "stay" {
		t.Errorf("Get keep = %q, want %q", got, "stay")
	}
}

func TestFileStorage_DeleteLastKeyRemovesFile(t *testing.T) {
	fs, path := newTestFileStorage(t)

	if err := fs.Set("only", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat before delete: %v", err)
	}
	if err := fs.Delete("only"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Deleting the last key removes the underlying file entirely.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, Stat err = %v", err)
	}
	// List on an absent file should succeed with no keys.
	keys, err := fs.List()
	if err != nil {
		t.Fatalf("List after removing file: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("List = %v, want empty", keys)
	}
}

func TestFileStorage_DeleteNonExistentKeyIsNoOp(t *testing.T) {
	fs, _ := newTestFileStorage(t)

	// Delete before any file exists.
	if err := fs.Delete("nope"); err != nil {
		t.Fatalf("Delete on empty store: %v", err)
	}
	// Create a file, then delete a key that isn't in it.
	if err := fs.Set("real", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := fs.Delete("still-nope"); err != nil {
		t.Fatalf("Delete of missing key: %v", err)
	}
	// "real" should still be present.
	got, err := fs.Get("real")
	if err != nil || got != "value" {
		t.Errorf("Get real = (%q, %v), want (%q, nil)", got, err, "value")
	}
}

// -------------------------------------------------------------------
// Encryption — ciphertext must not leak plaintext to disk
// -------------------------------------------------------------------

func TestFileStorage_ContentEncryptedOnDisk(t *testing.T) {
	fs, path := newTestFileStorage(t)

	const secret = "super-secret-password-xyz"
	if err := fs.Set("password", secret); err != nil {
		t.Fatalf("Set: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("on-disk file is empty")
	}
	if strings.Contains(string(raw), secret) {
		t.Errorf("plaintext secret leaked to disk: %s", string(raw))
	}
	if strings.Contains(string(raw), "password") {
		// The key name itself is inside the encrypted JSON payload,
		// so it must also not appear as plaintext.
		t.Errorf("plaintext key name leaked to disk: %s", string(raw))
	}
}

func TestFileStorage_WrongPassphraseFailsToDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	good := NewFileStorage(path, "correct-horse-battery-staple")
	if err := good.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	bad := NewFileStorage(path, "wrong-passphrase")
	if _, err := bad.Get("k"); err == nil {
		t.Error("Get with wrong passphrase: expected error, got nil")
	}
}

// -------------------------------------------------------------------
// Persistence across FileStorage instances
// -------------------------------------------------------------------

func TestFileStorage_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	const pp = "stable-pass"

	fs1 := NewFileStorage(path, pp)
	if err := fs1.Set("anthropic", "sk-ant-1"); err != nil {
		t.Fatalf("fs1.Set: %v", err)
	}
	if err := fs1.Set("openai", "sk-oai-2"); err != nil {
		t.Fatalf("fs1.Set: %v", err)
	}

	// Fresh instance against the same file.
	fs2 := NewFileStorage(path, pp)
	got, err := fs2.Get("anthropic")
	if err != nil {
		t.Fatalf("fs2.Get anthropic: %v", err)
	}
	if got != "sk-ant-1" {
		t.Errorf("fs2.Get anthropic = %q, want %q", got, "sk-ant-1")
	}
	got, err = fs2.Get("openai")
	if err != nil {
		t.Fatalf("fs2.Get openai: %v", err)
	}
	if got != "sk-oai-2" {
		t.Errorf("fs2.Get openai = %q, want %q", got, "sk-oai-2")
	}

	keys, err := fs2.List()
	if err != nil {
		t.Fatalf("fs2.List: %v", err)
	}
	sort.Strings(keys)
	want := []string{"anthropic", "openai"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("fs2.List = %v, want %v", keys, want)
	}
}

// -------------------------------------------------------------------
// Missing key behaviour
// -------------------------------------------------------------------

func TestFileStorage_MissingKeyReturnsEmptyString(t *testing.T) {
	fs, _ := newTestFileStorage(t)

	// No file exists yet — Get should surface the underlying read error.
	if _, err := fs.Get("absent"); err == nil {
		t.Log("Get on nonexistent file returned nil error (acceptable)")
	}

	// After at least one Set, the file exists; missing keys must return "".
	if err := fs.Set("present", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := fs.Get("absent")
	if err != nil {
		t.Fatalf("Get absent: unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("Get absent = %q, want empty string", got)
	}
}

func TestFileStorage_ListEmptyBeforeAnyWrites(t *testing.T) {
	fs, _ := newTestFileStorage(t)
	keys, err := fs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("List on empty store = %v, want empty", keys)
	}
}

// -------------------------------------------------------------------
// File permissions — 0600
// -------------------------------------------------------------------

func TestFileStorage_FilePermissions0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits not meaningful on Windows")
	}
	fs, path := newTestFileStorage(t)

	if err := fs.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}
}

func TestFileStorage_ParentDirectoryCreatedWithRestrictedPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits not meaningful on Windows")
	}
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c", "creds.json")

	fs := NewFileStorage(nested, "pp")
	if err := fs.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The immediately containing directory should have been created.
	info, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatalf("Stat parent dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected parent to be a directory")
	}
	// Permissions should not be world-readable.
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("parent dir perms = %o, expected group/other bits cleared", info.Mode().Perm())
	}
}

// -------------------------------------------------------------------
// AES helpers round-trip
// -------------------------------------------------------------------

func TestEncryptDecryptRoundTrip(t *testing.T) {
	salt := []byte("0123456789abcdef") // 16 bytes
	pass := []byte("passphrase")
	plaintext := []byte(`{"hello":"world"}`)

	ct, nonce, err := Encrypt(plaintext, salt, pass)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ct) == 0 || len(nonce) == 0 {
		t.Fatal("Encrypt returned empty ct/nonce")
	}
	got, err := Decrypt(ct, nonce, salt, pass)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestDecryptEmptyCiphertextFails(t *testing.T) {
	_, err := Decrypt(nil, []byte("nonce"), []byte("salt"), []byte("pp"))
	if err == nil {
		t.Fatal("expected error for empty ciphertext")
	}
}

// -------------------------------------------------------------------
// FallbackStorage — falls through to secondary
// -------------------------------------------------------------------

// failingStorage is a SecureStorage that always fails; used to force
// FallbackStorage onto the secondary backend.
type failingStorage struct{}

func (failingStorage) Get(string) (string, error) { return "", errors.New("primary down") }
func (failingStorage) Set(string, string) error   { return errors.New("primary down") }
func (failingStorage) Delete(string) error        { return errors.New("primary down") }
func (failingStorage) List() ([]string, error)    { return nil, errors.New("primary down") }

// emptyStorage is a SecureStorage whose Get always returns "" without error,
// exercising the "empty value means missing" branch in FallbackStorage.Get.
type emptyStorage struct{}

func (emptyStorage) Get(string) (string, error) { return "", nil }
func (emptyStorage) Set(string, string) error   { return nil }
func (emptyStorage) Delete(string) error        { return nil }
func (emptyStorage) List() ([]string, error)    { return nil, nil }

func TestFallbackStorage_FallsThroughOnPrimaryError(t *testing.T) {
	secondary, _ := newTestFileStorage(t)
	fs := &FallbackStorage{
		Primary:   failingStorage{},
		Secondary: secondary,
	}

	if err := fs.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Verify secondary received the write.
	got, err := secondary.Get("k")
	if err != nil {
		t.Fatalf("secondary.Get: %v", err)
	}
	if got != "v" {
		t.Errorf("secondary.Get = %q, want %q", got, "v")
	}
	// Fallback Get should also return the secondary value.
	got, err = fs.Get("k")
	if err != nil {
		t.Fatalf("fallback.Get: %v", err)
	}
	if got != "v" {
		t.Errorf("fallback.Get = %q, want %q", got, "v")
	}
}

func TestFallbackStorage_GetFallsThroughOnEmptyPrimary(t *testing.T) {
	secondary, _ := newTestFileStorage(t)
	if err := secondary.Set("k", "from-secondary"); err != nil {
		t.Fatalf("secondary.Set: %v", err)
	}
	fs := &FallbackStorage{
		Primary:   emptyStorage{}, // Get returns "" nil → must fall through
		Secondary: secondary,
	}
	got, err := fs.Get("k")
	if err != nil {
		t.Fatalf("fallback.Get: %v", err)
	}
	if got != "from-secondary" {
		t.Errorf("fallback.Get = %q, want %q", got, "from-secondary")
	}
}

func TestFallbackStorageListsBothBackends(t *testing.T) {
	primary, _ := newTestFileStorage(t)
	secondary, _ := newTestFileStorage(t)
	if err := primary.Set("apikey:openai", "a"); err != nil {
		t.Fatal(err)
	}
	if err := secondary.Set("apikey:nvidia", "b"); err != nil {
		t.Fatal(err)
	}
	fs := &FallbackStorage{Primary: primary, Secondary: secondary}
	keys, err := fs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "apikey:nvidia" || keys[1] != "apikey:openai" {
		t.Fatalf("chaves de ambos os armazenamentos não listadas: %v", keys)
	}
}

func TestFallbackStorage_DeletePropagatesToBoth(t *testing.T) {
	primary, _ := newTestFileStorage(t)
	secondary, _ := newTestFileStorage(t)
	if err := primary.Set("k", "a"); err != nil {
		t.Fatalf("primary.Set: %v", err)
	}
	if err := secondary.Set("k", "b"); err != nil {
		t.Fatalf("secondary.Set: %v", err)
	}

	fs := &FallbackStorage{Primary: primary, Secondary: secondary}
	if err := fs.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if v, _ := primary.Get("k"); v != "" {
		t.Errorf("primary still has value: %q", v)
	}
	if v, _ := secondary.Get("k"); v != "" {
		t.Errorf("secondary still has value: %q", v)
	}
}
