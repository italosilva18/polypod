package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// newRegistry builds an empty registry and registers the migrate skills.
// We use a fresh *skill.Registry (not skill.NewRegistry) so we do not pull
// in the built-in skills; only the migrate skills under test are present.
func newRegistry(t *testing.T) *skill.Registry {
	t.Helper()
	// skill.Registry has an unexported map field, so we must use NewRegistry.
	// The built-ins do not clash with the migrate_* skill names, so this is
	// safe for our assertions.
	reg := skill.NewRegistry()
	RegisterSkills(reg)
	return reg
}

// hasBinary reports whether the given executable is present in PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func TestRegisterSkills_RegistersAllFour(t *testing.T) {
	reg := newRegistry(t)

	want := []string{"migrate_diff", "migrate_list", "migrate_apply", "migrate_lint"}
	for _, name := range want {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("skill %q was not registered", name)
		}
	}
}

func TestMigrateSkills_HaveDescriptionsAndSchemas(t *testing.T) {
	reg := newRegistry(t)

	cases := []struct {
		name         string
		wantRequired []string
	}{
		{"migrate_diff", []string{"name"}},
		{"migrate_list", nil},
		{"migrate_apply", []string{"dsn"}},
		{"migrate_lint", nil},
	}

	for _, c := range cases {
		s, ok := reg.Get(c.name)
		if !ok {
			t.Fatalf("skill %q missing", c.name)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s: empty description", c.name)
		}
		if s.Execute == nil {
			t.Errorf("%s: nil Execute function", c.name)
		}
		if len(c.wantRequired) > 0 {
			req := map[string]bool{}
			for _, r := range s.Parameters.Required {
				req[r] = true
			}
			for _, r := range c.wantRequired {
				if !req[r] {
					t.Errorf("%s: expected required param %q, got %v",
						c.name, r, s.Parameters.Required)
				}
			}
		}
	}
}

func TestMigrateList_EmptyDirectory(t *testing.T) {
	reg := newRegistry(t)
	s, _ := reg.Get("migrate_list")

	dir := t.TempDir()
	out, err := s.Execute(map[string]string{"dir": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "## Migrations em "+dir) {
		t.Errorf("missing header for empty migrations dir, got: %q", out)
	}
	// No files should be listed.
	if strings.Contains(out, ".sql") {
		t.Errorf("empty dir should not list any .sql files, got: %q", out)
	}
}

func TestMigrateList_MissingDirectory(t *testing.T) {
	reg := newRegistry(t)
	s, _ := reg.Get("migrate_list")

	dir := filepath.Join(t.TempDir(), "does-not-exist")
	out, err := s.Execute(map[string]string{"dir": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Nenhuma migration encontrada") {
		t.Errorf("expected 'Nenhuma migration encontrada' message, got: %q", out)
	}
}

func TestMigrateList_ReportsSqlFilesAndSkipsOthers(t *testing.T) {
	reg := newRegistry(t)
	s, _ := reg.Get("migrate_list")

	dir := t.TempDir()
	upContent := "CREATE TABLE foo(id int);"
	if err := os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte(upContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001_init.down.sql"), []byte("DROP TABLE foo;"), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-SQL file should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	// Sub-directory should be ignored.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	out, err := s.Execute(map[string]string{"dir": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "001_init.up.sql") {
		t.Errorf("expected up.sql to be listed, got: %q", out)
	}
	if !strings.Contains(out, "001_init.down.sql") {
		t.Errorf("expected down.sql to be listed, got: %q", out)
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("non-sql file should not be listed, got: %q", out)
	}
	if strings.Contains(out, "- sub ") || strings.Contains(out, "- sub (") {
		t.Errorf("directories should not be listed, got: %q", out)
	}
	// Size reporting: should contain the up file's byte count.
	wantSize := len(upContent)
	if !strings.Contains(out, "001_init.up.sql ") {
		t.Errorf("expected filename followed by size info, got: %q", out)
	}
	if !strings.Contains(out, "bytes") {
		t.Errorf("expected size to be reported in bytes, got: %q", out)
	}
	// Sanity: the exact size should appear.
	if !strings.Contains(out, "("+itoa(wantSize)+" bytes)") {
		t.Errorf("expected size %d for up file, got: %q", wantSize, out)
	}
}

// itoa is a tiny local helper to avoid importing strconv just for a single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestMigrateDiff_FallbackCreatesUpAndDownFiles(t *testing.T) {
	// The fallback path only runs when neither atlas nor (golang-)migrate
	// are available. Skip gracefully if either is installed on the host,
	// since in that case the skill would shell out to a real tool.
	if hasBinary("atlas") || hasBinary("migrate") {
		t.Skip("atlas or migrate binary present on host; fallback path not exercised")
	}

	reg := newRegistry(t)
	s, _ := reg.Get("migrate_diff")

	dir := filepath.Join(t.TempDir(), "migrations")
	out, err := s.Execute(map[string]string{
		"name": "add_users_table",
		"dir":  dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Migration criada") {
		t.Errorf("expected creation message, got: %q", out)
	}
	if !strings.Contains(out, "add_users_table") {
		t.Errorf("expected migration name in output, got: %q", out)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	var upFound, downFound bool
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_add_users_table.up.sql") {
			upFound = true
			data, _ := os.ReadFile(filepath.Join(dir, name))
			if !strings.Contains(string(data), "Migration UP: add_users_table") {
				t.Errorf("up file content missing header: %q", string(data))
			}
		}
		if strings.HasSuffix(name, "_add_users_table.down.sql") {
			downFound = true
			data, _ := os.ReadFile(filepath.Join(dir, name))
			if !strings.Contains(string(data), "Migration DOWN: add_users_table") {
				t.Errorf("down file content missing header: %q", string(data))
			}
		}
	}
	if !upFound {
		t.Error("expected an *.up.sql file to be created")
	}
	if !downFound {
		t.Error("expected a *.down.sql file to be created")
	}
}

func TestMigrateApply_NoToolsReturnsError(t *testing.T) {
	// This test asserts the error path when neither atlas nor migrate exist.
	if hasBinary("atlas") || hasBinary("migrate") {
		t.Skip("atlas or migrate binary present; cannot assert missing-tool error")
	}

	reg := newRegistry(t)
	s, _ := reg.Get("migrate_apply")

	// migrate_apply.Execute returns an error when no tool is available.
	// Call the underlying Execute directly so we observe the error value.
	out, err := s.Execute(map[string]string{
		"dsn": "postgres://user:pass@localhost/db?sslmode=disable",
		"dir": t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error when no migration tool is installed; got output: %q", out)
	}
	if !strings.Contains(err.Error(), "nenhuma ferramenta de migration") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Via the registry wrapper, errors are converted into an "Erro: ..." string
	// with nil error. Verify that behavior too.
	res, rerr := reg.Execute("migrate_apply", `{"dsn":"postgres://x","dir":"`+t.TempDir()+`"}`)
	if rerr != nil {
		t.Fatalf("registry.Execute should swallow skill errors, got: %v", rerr)
	}
	if !strings.HasPrefix(res, "Erro:") {
		t.Errorf("expected 'Erro:' prefix from registry wrapper, got: %q", res)
	}
}

func TestMigrateLint_ManualDetectsDangerousPatterns(t *testing.T) {
	// The manual lint path is only taken when atlas is missing.
	if hasBinary("atlas") {
		t.Skip("atlas binary present; manual lint path not exercised")
	}

	reg := newRegistry(t)
	s, _ := reg.Get("migrate_lint")

	dir := t.TempDir()
	// Dangerous migration: DROP TABLE, DROP COLUMN, TRUNCATE,
	// plus a NOT NULL without DEFAULT.
	dangerous := `DROP TABLE old_users;
ALTER TABLE accounts DROP COLUMN legacy_field;
TRUNCATE audit_log;
ALTER TABLE items ADD COLUMN qty INT NOT NULL;
`
	if err := os.WriteFile(filepath.Join(dir, "001_bad.up.sql"), []byte(dangerous), 0644); err != nil {
		t.Fatal(err)
	}
	// A safe file should not trigger any warning.
	safe := "CREATE TABLE new_table (id serial primary key, name text);\n"
	if err := os.WriteFile(filepath.Join(dir, "002_safe.up.sql"), []byte(safe), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-up files should be ignored by the linter.
	if err := os.WriteFile(filepath.Join(dir, "001_bad.down.sql"), []byte("DROP TABLE new_table;"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := s.Execute(map[string]string{"dir": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mustContain := []string{
		"## Migration Lint",
		"001_bad.up.sql",
		"DROP TABLE",
		"DROP COLUMN",
		"TRUNCATE",
		"NOT NULL",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("lint output missing %q, got: %q", m, out)
		}
	}
	// The safe file should not appear in any warning line.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[WARN]") || strings.Contains(line, "[INFO]") {
			if strings.Contains(line, "002_safe.up.sql") {
				t.Errorf("safe file incorrectly flagged: %q", line)
			}
		}
	}
}

func TestMigrateLint_ManualCleanWhenNoIssues(t *testing.T) {
	if hasBinary("atlas") {
		t.Skip("atlas binary present; manual lint path not exercised")
	}

	reg := newRegistry(t)
	s, _ := reg.Get("migrate_lint")

	dir := t.TempDir()
	clean := "CREATE TABLE users (id serial primary key, email text);\n"
	if err := os.WriteFile(filepath.Join(dir, "001_init.up.sql"), []byte(clean), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := s.Execute(map[string]string{"dir": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Migrations OK") {
		t.Errorf("expected clean lint result, got: %q", out)
	}
}
