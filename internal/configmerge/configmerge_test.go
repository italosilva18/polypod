package configmerge

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// writeFragment is a helper to write a yaml fragment into dir.
func writeFragment(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fragment %s: %v", name, err)
	}
}

func TestMergeConfigDir_SingleFragment(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "a.yaml", "foo: bar\nnum: 42\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["foo"] != "bar" {
		t.Errorf("expected foo=bar, got %v", result["foo"])
	}
	if result["num"] != 42 {
		t.Errorf("expected num=42, got %v", result["num"])
	}
}

func TestMergeConfigDir_MultipleFragmentsAlphabeticalOrder(t *testing.T) {
	dir := t.TempDir()
	// 'b' is written first to ensure ordering is by filename, not write order.
	writeFragment(t, dir, "b.yaml", "key: from_b\nonly_b: yes\n")
	writeFragment(t, dir, "a.yaml", "key: from_a\nonly_a: yes\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 'b.yaml' comes after 'a.yaml' alphabetically, so 'b' wins for shared keys.
	if result["key"] != "from_b" {
		t.Errorf("expected key=from_b (later wins), got %v", result["key"])
	}
	if result["only_a"] != "yes" {
		t.Errorf("expected only_a to be preserved, got %v", result["only_a"])
	}
	if result["only_b"] != "yes" {
		t.Errorf("expected only_b to be preserved, got %v", result["only_b"])
	}
}

func TestMergeConfigDir_DeepMerge(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "01-base.yaml", `
server:
  host: localhost
  port: 8080
  options:
    timeout: 30
`)
	writeFragment(t, dir, "02-override.yaml", `
server:
  port: 9090
  options:
    retries: 3
`)

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	server, ok := result["server"].(map[string]any)
	if !ok {
		t.Fatalf("expected server to be a map, got %T", result["server"])
	}
	if server["host"] != "localhost" {
		t.Errorf("expected host=localhost (preserved), got %v", server["host"])
	}
	if server["port"] != 9090 {
		t.Errorf("expected port=9090 (overridden), got %v", server["port"])
	}
	opts, ok := server["options"].(map[string]any)
	if !ok {
		t.Fatalf("expected options to be a map, got %T", server["options"])
	}
	if opts["timeout"] != 30 {
		t.Errorf("expected timeout=30 (preserved), got %v", opts["timeout"])
	}
	if opts["retries"] != 3 {
		t.Errorf("expected retries=3 (added), got %v", opts["retries"])
	}
}

func TestMergeConfigDir_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestMergeConfigDir_NonexistentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for nonexistent dir, got %v", result)
	}
}

func TestMergeConfigDir_IgnoresNonYamlFiles(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "real.yaml", "included: true\n")
	writeFragment(t, dir, "notes.txt", "included: false\n")
	writeFragment(t, dir, "README.md", "# ignored\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["included"] != true {
		t.Errorf("expected included=true from yaml, got %v", result["included"])
	}
	if len(result) != 1 {
		t.Errorf("expected only 1 key from yaml file, got %d keys: %v", len(result), result)
	}
}

func TestMergeConfigDir_AcceptsYmlExtension(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "a.yml", "from_yml: yes\n")
	writeFragment(t, dir, "b.yaml", "from_yaml: yes\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["from_yml"] != "yes" {
		t.Errorf("expected from_yml=yes, got %v", result["from_yml"])
	}
	if result["from_yaml"] != "yes" {
		t.Errorf("expected from_yaml=yes, got %v", result["from_yaml"])
	}
}

func TestMergeConfigDir_IgnoresSubdirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	writeFragment(t, sub, "inner.yaml", "inner: true\n")
	writeFragment(t, dir, "outer.yaml", "outer: true\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := result["inner"]; present {
		t.Errorf("expected subdirectory fragments to be ignored, got inner in result: %v", result)
	}
	if result["outer"] != true {
		t.Errorf("expected outer=true, got %v", result["outer"])
	}
}

func TestMergeConfigDir_EmptyFragmentFile(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "empty.yaml", "")
	writeFragment(t, dir, "data.yaml", "x: 1\n")

	result, err := MergeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["x"] != 1 {
		t.Errorf("expected x=1 from data fragment, got %v", result["x"])
	}
}

func TestMergeIntoConfig_NoOverrides(t *testing.T) {
	base := []byte("key: value\n")
	out, err := MergeIntoConfig(base, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, base) {
		t.Errorf("expected base returned unchanged, got %s", string(out))
	}

	out, err = MergeIntoConfig(base, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, base) {
		t.Errorf("expected base returned unchanged for empty overrides, got %s", string(out))
	}
}

func TestMergeIntoConfig_DeepMerge(t *testing.T) {
	base := []byte(`
db:
  host: localhost
  port: 5432
log:
  level: info
`)
	overrides := map[string]any{
		"db": map[string]any{
			"port":     6543,
			"password": "secret",
		},
		"new_key": "added",
	}

	out, err := MergeIntoConfig(base, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var merged map[string]any
	if err := yaml.Unmarshal(out, &merged); err != nil {
		t.Fatalf("failed to parse merged output: %v", err)
	}

	db, ok := merged["db"].(map[string]any)
	if !ok {
		t.Fatalf("expected db to be a map, got %T", merged["db"])
	}
	if db["host"] != "localhost" {
		t.Errorf("expected db.host=localhost (preserved), got %v", db["host"])
	}
	if db["port"] != 6543 {
		t.Errorf("expected db.port=6543 (overridden), got %v", db["port"])
	}
	if db["password"] != "secret" {
		t.Errorf("expected db.password=secret (added), got %v", db["password"])
	}

	logSection, ok := merged["log"].(map[string]any)
	if !ok {
		t.Fatalf("expected log to be a map, got %T", merged["log"])
	}
	if logSection["level"] != "info" {
		t.Errorf("expected log.level=info (preserved), got %v", logSection["level"])
	}

	if merged["new_key"] != "added" {
		t.Errorf("expected new_key=added, got %v", merged["new_key"])
	}
}

func TestMergeIntoConfig_EmptyBase(t *testing.T) {
	overrides := map[string]any{
		"hello": "world",
	}
	out, err := MergeIntoConfig(nil, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var merged map[string]any
	if err := yaml.Unmarshal(out, &merged); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if merged["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", merged["hello"])
	}
}

func TestMergeIntoConfig_InvalidBase(t *testing.T) {
	// A YAML that cannot unmarshal into map[string]any (it's a sequence, not a mapping).
	base := []byte("- just\n- a\n- list\n")
	overrides := map[string]any{"k": "v"}
	_, err := MergeIntoConfig(base, overrides)
	if err == nil {
		t.Errorf("expected an error when base YAML is not a mapping, got nil")
	}
}

func TestMergeIntoConfig_ScalarOverridesMap(t *testing.T) {
	// When src scalar replaces dst map, the scalar should win (non-both-map branch).
	base := []byte(`
feature:
  enabled: true
  level: high
`)
	overrides := map[string]any{
		"feature": "disabled",
	}
	out, err := MergeIntoConfig(base, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var merged map[string]any
	if err := yaml.Unmarshal(out, &merged); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if merged["feature"] != "disabled" {
		t.Errorf("expected feature=disabled (scalar overrode map), got %v (%T)", merged["feature"], merged["feature"])
	}
}

func TestListFragments(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "zeta.yaml", "a: 1\n")
	writeFragment(t, dir, "alpha.yml", "a: 1\n")
	writeFragment(t, dir, "beta.yaml", "a: 1\n")
	writeFragment(t, dir, "ignored.txt", "not yaml\n")

	// Also verify subdirectories are excluded.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	got := ListFragments(dir)
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v (sorted, without extensions), got %v", want, got)
	}
}

func TestListFragments_NonexistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	got := ListFragments(dir)
	if got != nil {
		t.Errorf("expected nil for nonexistent dir, got %v", got)
	}
}
