package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a small helper that creates parent dirs and writes a file.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Registry basics
// ---------------------------------------------------------------------------

func TestNewRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.Commands == nil {
		t.Error("Commands map is nil; expected initialized map")
	}
	if r.Recipes == nil {
		t.Error("Recipes map is nil; expected initialized map")
	}
	if len(r.Commands) != 0 {
		t.Errorf("expected 0 commands, got %d", len(r.Commands))
	}
	if len(r.Recipes) != 0 {
		t.Errorf("expected 0 recipes, got %d", len(r.Recipes))
	}
}

func TestRegistry_DirectInsertAndGet(t *testing.T) {
	// The Registry has no Register method; commands are added via the
	// Commands map (this is how LoadFromDir populates it). Verify that
	// GetCommand finds a directly-inserted command.
	r := NewRegistry()
	r.Commands["hello"] = &Command{
		Name:        "hello",
		Description: "say hi",
		Content:     "Say hello to $ARGUMENTS",
	}

	cmd, ok := r.GetCommand("hello")
	if !ok {
		t.Fatal("GetCommand('hello') returned ok=false")
	}
	if cmd.Name != "hello" {
		t.Errorf("cmd.Name = %q, want %q", cmd.Name, "hello")
	}
	if cmd.Description != "say hi" {
		t.Errorf("cmd.Description = %q", cmd.Description)
	}
}

func TestGetCommand_Unknown(t *testing.T) {
	r := NewRegistry()
	cmd, ok := r.GetCommand("does-not-exist")
	if ok {
		t.Errorf("GetCommand unknown returned ok=true, cmd=%+v", cmd)
	}
	if cmd != nil {
		t.Errorf("expected nil command for unknown name, got %+v", cmd)
	}
}

func TestGetCommand_StripsSlashPrefix(t *testing.T) {
	r := NewRegistry()
	r.Commands["build"] = &Command{Name: "build", Description: "compile"}

	cmd, ok := r.GetCommand("/build")
	if !ok {
		t.Fatal("GetCommand('/build') returned ok=false")
	}
	if cmd.Name != "build" {
		t.Errorf("cmd.Name = %q, want %q", cmd.Name, "build")
	}
}

// ---------------------------------------------------------------------------
// LoadFromDir
// ---------------------------------------------------------------------------

func TestLoadFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	if err := r.LoadFromDir(dir); err != nil {
		t.Fatalf("LoadFromDir on empty dir returned error: %v", err)
	}
	if len(r.Commands) != 0 {
		t.Errorf("expected 0 commands, got %d", len(r.Commands))
	}
	if len(r.Recipes) != 0 {
		t.Errorf("expected 0 recipes, got %d", len(r.Recipes))
	}
}

func TestLoadFromDir_MissingBaseDir(t *testing.T) {
	// LoadFromDir silently ignores missing commands/ and recipes/ subdirs,
	// so passing a non-existent base should return nil and leave an empty
	// registry rather than erroring out.
	r := NewRegistry()
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if err := r.LoadFromDir(missing); err != nil {
		t.Fatalf("LoadFromDir on missing dir returned error: %v", err)
	}
	if len(r.Commands) != 0 || len(r.Recipes) != 0 {
		t.Errorf("expected empty registry, got %d cmds / %d recipes",
			len(r.Commands), len(r.Recipes))
	}
}

func TestLoadFromDir_SinglePlainMarkdown(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "commands", "test.md"),
		"Run the test suite with verbose output\n")

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	cmd, ok := r.GetCommand("test")
	if !ok {
		t.Fatal("command 'test' not found after load")
	}
	if cmd.Name != "test" {
		t.Errorf("cmd.Name = %q, want %q", cmd.Name, "test")
	}
	// No frontmatter -> Description empty, Content equals the whole body.
	if cmd.Description != "" {
		t.Errorf("cmd.Description = %q, want empty", cmd.Description)
	}
	if !strings.Contains(cmd.Content, "Run the test suite") {
		t.Errorf("cmd.Content = %q; expected body to be preserved", cmd.Content)
	}
	if cmd.FilePath == "" {
		t.Error("cmd.FilePath should be set")
	}
}

func TestLoadFromDir_WithFrontmatter(t *testing.T) {
	base := t.TempDir()
	content := `---
description: Run tests
disabled: false
ai_only: true
---
Run the test suite
`
	writeFile(t, filepath.Join(base, "commands", "runtests.md"), content)

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	cmd, ok := r.GetCommand("runtests")
	if !ok {
		t.Fatal("command 'runtests' not found")
	}
	if cmd.Description != "Run tests" {
		t.Errorf("Description = %q, want %q", cmd.Description, "Run tests")
	}
	if cmd.Disabled {
		t.Error("Disabled = true, want false")
	}
	if !cmd.AIOnly {
		t.Error("AIOnly = false, want true")
	}
	if cmd.Content != "Run the test suite" {
		t.Errorf("Content = %q, want %q", cmd.Content, "Run the test suite")
	}
}

func TestLoadFromDir_FrontmatterNameOverride(t *testing.T) {
	base := t.TempDir()
	content := `---
name: custom-name
description: has custom name
---
body
`
	writeFile(t, filepath.Join(base, "commands", "file.md"), content)

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	// Frontmatter name wins; filename-derived name is not registered.
	if _, ok := r.GetCommand("custom-name"); !ok {
		t.Error("command with frontmatter name 'custom-name' not found")
	}
	if _, ok := r.GetCommand("file"); ok {
		t.Error("command 'file' (filename) should not exist when name is set in frontmatter")
	}
}

func TestLoadFromDir_IgnoresNonMarkdownAndDirs(t *testing.T) {
	base := t.TempDir()
	cmdDir := filepath.Join(base, "commands")
	writeFile(t, filepath.Join(cmdDir, "keep.md"), "kept\n")
	writeFile(t, filepath.Join(cmdDir, "skip.txt"), "ignored\n")
	writeFile(t, filepath.Join(cmdDir, "skip.yaml"), "ignored\n")
	// Nested directory with a markdown file - LoadFromDir is NOT recursive,
	// so this should be ignored.
	writeFile(t, filepath.Join(cmdDir, "nested", "deep.md"), "deep\n")

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	if len(r.Commands) != 1 {
		t.Errorf("expected 1 command, got %d: %v", len(r.Commands), r.Commands)
	}
	if _, ok := r.GetCommand("keep"); !ok {
		t.Error("'keep' command missing")
	}
	if _, ok := r.GetCommand("deep"); ok {
		t.Error("'deep' command should not be loaded (non-recursive)")
	}
}

func TestLoadFromDir_MultipleCommands(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "commands", "a.md"),
		"---\ndescription: first\n---\nAlpha\n")
	writeFile(t, filepath.Join(base, "commands", "b.md"),
		"---\ndescription: second\n---\nBeta\n")
	writeFile(t, filepath.Join(base, "commands", "c.md"),
		"---\ndescription: third\n---\nGamma\n")

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	if len(r.Commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(r.Commands))
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := r.GetCommand(name); !ok {
			t.Errorf("command %q missing after load", name)
		}
	}
}

func TestLoadFromDir_CommandContentPreserved(t *testing.T) {
	base := t.TempDir()
	body := "Line one\nLine two\n\nLine four after blank"
	content := "---\ndescription: multi-line\n---\n" + body + "\n"
	writeFile(t, filepath.Join(base, "commands", "multi.md"), content)

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	cmd, ok := r.GetCommand("multi")
	if !ok {
		t.Fatal("command 'multi' not found")
	}
	// loadCommand trims surrounding whitespace; the interior must survive.
	if !strings.Contains(cmd.Content, "Line one") ||
		!strings.Contains(cmd.Content, "Line four after blank") {
		t.Errorf("Content did not preserve body lines; got %q", cmd.Content)
	}
	if !strings.Contains(cmd.Content, "Line two\n\nLine four") {
		t.Errorf("Content did not preserve blank line; got %q", cmd.Content)
	}
}

// ---------------------------------------------------------------------------
// Recipes via LoadFromDir
// ---------------------------------------------------------------------------

func TestLoadFromDir_Recipe(t *testing.T) {
	base := t.TempDir()
	recipe := `name: deploy
description: deploy to prod
parameters:
  - name: env
    description: environment
    required: true
    default: staging
steps:
  - name: build
    command: make build
    on_error: stop
  - name: ship
    prompt: "Ship to {{env}}"
`
	writeFile(t, filepath.Join(base, "recipes", "deploy.yaml"), recipe)

	r := NewRegistry()
	if err := r.LoadFromDir(base); err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}

	rec, ok := r.GetRecipe("deploy")
	if !ok {
		t.Fatal("recipe 'deploy' not found")
	}
	if rec.Description != "deploy to prod" {
		t.Errorf("Description = %q", rec.Description)
	}
	if len(rec.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(rec.Steps))
	}
	if rec.Steps[0].Name != "build" || rec.Steps[0].Command != "make build" {
		t.Errorf("step[0] = %+v", rec.Steps[0])
	}
	if len(rec.Parameters) != 1 || rec.Parameters[0].Name != "env" {
		t.Errorf("parameters = %+v", rec.Parameters)
	}
}

// ---------------------------------------------------------------------------
// Command struct / listing
// ---------------------------------------------------------------------------

func TestCommandStructFields(t *testing.T) {
	// Exercises every exported field so the test will fail to compile if
	// the struct shape changes underneath us.
	c := &Command{
		Name:        "build",
		Description: "compile the project",
		Disabled:    true,
		AIOnly:      true,
		Content:     "run make",
		FilePath:    "/tmp/build.md",
	}
	if c.Name != "build" || c.Description != "compile the project" {
		t.Errorf("unexpected values: %+v", c)
	}
	if !c.Disabled || !c.AIOnly {
		t.Errorf("bool fields not set: %+v", c)
	}
	if c.Content != "run make" || c.FilePath != "/tmp/build.md" {
		t.Errorf("content/filepath wrong: %+v", c)
	}
}

func TestListCommands_SkipsDisabled(t *testing.T) {
	r := NewRegistry()
	r.Commands["a"] = &Command{Name: "a", Description: "alpha"}
	r.Commands["b"] = &Command{Name: "b", Description: "beta", Disabled: true}
	r.Commands["c"] = &Command{Name: "c"} // empty description

	out := r.ListCommands()
	if len(out) != 2 {
		t.Fatalf("expected 2 entries (disabled skipped), got %d: %v", len(out), out)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "/a") || !strings.Contains(joined, "alpha") {
		t.Errorf("missing /a entry: %v", out)
	}
	if strings.Contains(joined, "/b") {
		t.Errorf("disabled command /b should be skipped: %v", out)
	}
	if !strings.Contains(joined, "/c") {
		t.Errorf("command /c missing: %v", out)
	}
}

func TestListRecipes(t *testing.T) {
	r := NewRegistry()
	r.Recipes["deploy"] = &Recipe{
		Name:        "deploy",
		Description: "ship it",
		Steps:       []RecipeStep{{Name: "one"}, {Name: "two"}},
	}
	r.Recipes["noop"] = &Recipe{Name: "noop"} // empty description

	out := r.ListRecipes()
	if len(out) != 2 {
		t.Fatalf("expected 2 recipe entries, got %d: %v", len(out), out)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "deploy") || !strings.Contains(joined, "ship it") {
		t.Errorf("missing deploy entry: %v", out)
	}
	if !strings.Contains(joined, "2 steps") {
		t.Errorf("step count missing: %v", out)
	}
}

// ---------------------------------------------------------------------------
// ApplyCommand / ApplyRecipeParams
// ---------------------------------------------------------------------------

func TestApplyCommand_Arguments(t *testing.T) {
	cmd := &Command{
		Name:    "greet",
		Content: "Hello $ARGUMENTS, welcome to {{args}}!",
	}
	got := ApplyCommand(cmd, "world")
	want := "Hello world, welcome to world!"
	if got != want {
		t.Errorf("ApplyCommand = %q, want %q", got, want)
	}
}

func TestApplyCommand_NoPlaceholders(t *testing.T) {
	cmd := &Command{Content: "static text"}
	if got := ApplyCommand(cmd, "anything"); got != "static text" {
		t.Errorf("ApplyCommand = %q, want %q", got, "static text")
	}
}

func TestApplyRecipeParams(t *testing.T) {
	recipe := &Recipe{
		Name: "deploy",
		Parameters: []RecipeParam{
			{Name: "env", Default: "staging"},
			{Name: "tag", Default: "latest"},
		},
		Steps: []RecipeStep{
			{Name: "build", Command: "build --tag {{tag}}"},
			{Name: "push", Prompt: "Push to {{env}} with {{tag}}"},
		},
	}

	// Override env, let tag fall back to default.
	steps := ApplyRecipeParams(recipe, map[string]string{"env": "prod"})

	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if !strings.Contains(steps[0].Command, "--tag latest") {
		t.Errorf("tag default not applied: %q", steps[0].Command)
	}
	if !strings.Contains(steps[1].Prompt, "Push to prod") {
		t.Errorf("env override not applied: %q", steps[1].Prompt)
	}
	if !strings.Contains(steps[1].Prompt, "with latest") {
		t.Errorf("tag default not applied in prompt: %q", steps[1].Prompt)
	}

	// Originals must not be mutated.
	if recipe.Steps[0].Command != "build --tag {{tag}}" {
		t.Errorf("original recipe step was mutated: %q", recipe.Steps[0].Command)
	}
}
