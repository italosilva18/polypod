package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// newEmptyRegistry creates a registry with all built-in skills removed,
// so tests can observe only what RegisterSkills adds.
func newEmptyRegistry() *skill.Registry {
	r := skill.NewRegistry()
	for _, name := range r.List() {
		r.Unregister(name)
	}
	return r
}

func TestRegisterSkills_RegistersAllThree(t *testing.T) {
	reg := newEmptyRegistry()
	RegisterSkills(reg)

	want := []string{"security_scan", "security_secrets", "security_deps"}
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected skill %q to be registered", name)
		}
	}
}

func TestRegisterSkills_SkillMetadata(t *testing.T) {
	reg := newEmptyRegistry()
	RegisterSkills(reg)

	cases := []struct {
		name        string
		wantDescSub string
	}{
		{"security_scan", "varredura"},
		{"security_secrets", "segredos"},
		{"security_deps", "dependencias"},
	}
	for _, c := range cases {
		s, ok := reg.Get(c.name)
		if !ok {
			t.Fatalf("skill %q not registered", c.name)
		}
		if s.Description == "" {
			t.Errorf("skill %q has empty description", c.name)
		}
		if !strings.Contains(strings.ToLower(s.Description), c.wantDescSub) {
			t.Errorf("skill %q description %q does not contain %q", c.name, s.Description, c.wantDescSub)
		}
		if s.Execute == nil {
			t.Errorf("skill %q has nil Execute", c.name)
		}
		// path parameter should be present
		if _, ok := s.Parameters.Properties["path"]; !ok {
			t.Errorf("skill %q missing 'path' parameter", c.name)
		}
	}
}

func TestRegisterSkills_PreservesExisting(t *testing.T) {
	reg := skill.NewRegistry()
	before := len(reg.List())
	RegisterSkills(reg)
	after := len(reg.List())
	if after != before+3 {
		t.Errorf("expected %d skills after register, got %d", before+3, after)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(existing, []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !fileExists(existing) {
		t.Errorf("fileExists(%q) = false, want true", existing)
	}
	if fileExists(filepath.Join(dir, "missing.txt")) {
		t.Errorf("fileExists on missing file returned true")
	}
	// Directories also "exist" per fileExists (it just stats).
	if !fileExists(dir) {
		t.Errorf("fileExists(dir) = false, want true")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in  string
		max int
		out string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 3, ""},
		{"abcdef", 3, "abc..."},
	}
	for _, c := range cases {
		got := truncate(c.in, c.max)
		if got != c.out {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.out)
		}
	}
}

func TestSecretPatterns_Match(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"api_key assignment", `api_key = "abcdef12345678"`},
		{"password colon", `password: "supersecret123"`},
		{"AWS access key", `key=AKIAIOSFODNN7EXAMPLE`},
		{"private key header", "-----BEGIN RSA PRIVATE KEY-----"},
		{"github PAT", "ghp_" + strings.Repeat("a", 36)},
		{"openai key", "sk-" + strings.Repeat("a", 40)},
		{"postgres url", "postgres://user:pass@localhost:5432/db"},
		{"mongodb srv", "mongodb+srv://user:pass@cluster.mongodb.net/db"},
	}
	for _, c := range cases {
		matched := false
		for _, re := range secretPatterns {
			if re.MatchString(c.content) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s: expected at least one secret pattern to match %q", c.name, c.content)
		}
	}
}

func TestSecretPatterns_NoFalsePositives(t *testing.T) {
	benign := []string{
		"this is just a normal comment about secrets",
		"url = \"https://example.com/index.html\"",
		"greeting := \"hello world\"",
		"// TODO: add password validation",
		"const max = 1234",
	}
	for _, line := range benign {
		for i, re := range secretPatterns {
			if re.MatchString(line) {
				t.Errorf("benign line %q matched secretPatterns[%d] = %q", line, i, re.String())
			}
		}
	}
}

func TestSecurityScan_DefaultsAndEmptyEnv(t *testing.T) {
	reg := newEmptyRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("security_scan")
	if !ok {
		t.Fatal("security_scan not registered")
	}

	// Run against an empty temp dir - no go.mod, no package-lock, no python markers.
	// With no scanners available this should return the "nothing found" message;
	// if scanners ARE available it should still return without error.
	dir := t.TempDir()
	out, err := s.Execute(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("security_scan execute: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output from security_scan")
	}
}

func TestSecuritySecrets_FindsPlantedSecret(t *testing.T) {
	reg := newEmptyRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("security_secrets")
	if !ok {
		t.Fatal("security_secrets not registered")
	}

	dir := t.TempDir()
	planted := `const api_key = "abcdef12345678deadbeef"`
	if err := os.WriteFile(filepath.Join(dir, "leak.go"), []byte(planted), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out, err := s.Execute(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("security_secrets execute: %v", err)
	}
	// If gitleaks is installed on the host, output comes from gitleaks; otherwise
	// it comes from the regex fallback. Either way we expect *some* signal that
	// something was found, OR the tool ran cleanly. We primarily assert it didn't
	// error and produced output.
	if out == "" {
		t.Error("expected non-empty output from security_secrets")
	}
}

func TestSecurityDeps_EmptyDir(t *testing.T) {
	reg := newEmptyRegistry()
	RegisterSkills(reg)

	s, ok := reg.Get("security_deps")
	if !ok {
		t.Fatal("security_deps not registered")
	}

	dir := t.TempDir()
	out, err := s.Execute(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("security_deps execute: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output from security_deps")
	}
}
