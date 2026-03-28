package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `# comment
KEY1=value1
KEY2="quoted value"
KEY3='single quoted'
EMPTY=
SPACED = with spaces
`
	os.WriteFile(envFile, []byte(content), 0644)

	// Clear env first
	os.Unsetenv("KEY1")
	os.Unsetenv("KEY2")
	os.Unsetenv("KEY3")

	n, err := loadFile(envFile)
	if err != nil {
		t.Fatalf("loadFile error: %v", err)
	}
	if n < 4 {
		t.Fatalf("expected at least 4 vars loaded, got %d", n)
	}

	if v := os.Getenv("KEY1"); v != "value1" {
		t.Errorf("KEY1 = %q, want %q", v, "value1")
	}
	if v := os.Getenv("KEY2"); v != "quoted value" {
		t.Errorf("KEY2 = %q, want %q", v, "quoted value")
	}
	if v := os.Getenv("KEY3"); v != "single quoted" {
		t.Errorf("KEY3 = %q, want %q", v, "single quoted")
	}
}

func TestLoadDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	os.WriteFile(envFile, []byte("EXISTING=from_env_file\n"), 0644)
	os.Setenv("EXISTING", "from_environment")
	defer os.Unsetenv("EXISTING")

	loadFile(envFile)

	if v := os.Getenv("EXISTING"); v != "from_environment" {
		t.Errorf("should not override existing env var, got %q", v)
	}
}
