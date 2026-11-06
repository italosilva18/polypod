package depsgraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// TestRegisterSkills_RegistersExpectedNames verifies that all expected skills
// are added to the registry by RegisterSkills.
func TestRegisterSkills_RegistersExpectedNames(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	expected := []string{"deps_tree", "deps_circular", "deps_graph_image"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected skill %q to be registered", name)
		}
	}
}

// TestRegisterSkills_SkillsHaveDescriptionAndExecute verifies that registered
// skills are not empty shells: each one has a description and an Execute func.
func TestRegisterSkills_SkillsHaveDescriptionAndExecute(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	names := []string{"deps_tree", "deps_circular", "deps_graph_image"}
	for _, name := range names {
		s, ok := reg.Get(name)
		if !ok {
			t.Fatalf("skill %q not registered", name)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("skill %q has empty description", name)
		}
		if s.Execute == nil {
			t.Errorf("skill %q has nil Execute func", name)
		}
	}
}

// TestDepsTree_UnknownProjectType ensures the deps_tree skill returns a
// graceful message when no recognised project file is present.
func TestDepsTree_UnknownProjectType(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, ok := reg.Get("deps_tree")
	if !ok {
		t.Fatal("deps_tree not registered")
	}

	dir := t.TempDir()
	out, err := s.Execute(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "nao reconhecido") {
		t.Errorf("expected unknown project message, got: %q", out)
	}
}

// TestDepsCircular_NoCircularInEmptyDir ensures deps_circular reports no
// circular deps for a non-Go directory (no go.mod present).
func TestDepsCircular_NoCircularInEmptyDir(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, ok := reg.Get("deps_circular")
	if !ok {
		t.Fatal("deps_circular not registered")
	}

	dir := t.TempDir()
	out, err := s.Execute(map[string]string{"path": dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not crash; output should be a non-empty string.
	if strings.TrimSpace(out) == "" {
		t.Error("expected non-empty output from deps_circular")
	}
}

// TestFormatGoGraph_BuildsMarkdown verifies the formatter produces markdown
// containing the expected modules and a header.
func TestFormatGoGraph_BuildsMarkdown(t *testing.T) {
	input := strings.Join([]string{
		"github.com/foo/bar@v1.0.0 github.com/x/y@v1.2.3",
		"github.com/foo/bar@v1.0.0 github.com/a/b@v0.1.0",
		"github.com/x/y@v1.2.3 github.com/c/d@v2.0.0",
	}, "\n")

	out := formatGoGraph(input)

	if !strings.Contains(out, "## Dependencias Go") {
		t.Errorf("expected header in output, got: %q", out)
	}
	for _, mod := range []string{
		"github.com/foo/bar",
		"github.com/x/y",
		"github.com/a/b",
		"github.com/c/d",
	} {
		if !strings.Contains(out, mod) {
			t.Errorf("expected %q in output, got: %q", mod, out)
		}
	}
	// Versions must be stripped.
	if strings.Contains(out, "@v1.0.0") || strings.Contains(out, "@v1.2.3") {
		t.Errorf("expected versions to be stripped, got: %q", out)
	}
}

// TestFormatGoGraph_HandlesEmptyInput ensures the formatter doesn't crash on
// empty input and still emits the header.
func TestFormatGoGraph_HandlesEmptyInput(t *testing.T) {
	out := formatGoGraph("")
	if !strings.Contains(out, "## Dependencias Go") {
		t.Errorf("expected header in empty output, got: %q", out)
	}
}

// TestFormatGoGraph_TruncatesManyChildren ensures the "... (N total)" marker
// is emitted when a node has more than 10 children.
func TestFormatGoGraph_TruncatesManyChildren(t *testing.T) {
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, "github.com/root/mod@v1.0.0 github.com/child/m"+string(rune('a'+i))+"@v1")
	}
	out := formatGoGraph(strings.Join(lines, "\n"))
	if !strings.Contains(out, "total)") {
		t.Errorf("expected truncation marker, got: %q", out)
	}
}

// TestGoGraphToDot_ProducesValidDigraph verifies the DOT serializer produces
// a digraph with quoted edges and stripped versions.
func TestGoGraphToDot_ProducesValidDigraph(t *testing.T) {
	input := strings.Join([]string{
		"github.com/foo/bar@v1.0.0 github.com/x/y@v1.2.3",
		"github.com/x/y@v1.2.3 github.com/c/d@v2.0.0",
	}, "\n")

	out := goGraphToDot(input)

	if !strings.HasPrefix(out, "digraph deps {") {
		t.Errorf("expected digraph header, got: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("expected closing brace, got: %q", out)
	}
	if !strings.Contains(out, "rankdir=LR") {
		t.Errorf("expected rankdir=LR directive, got: %q", out)
	}
	wantEdges := []string{
		`"github.com/foo/bar" -> "github.com/x/y";`,
		`"github.com/x/y" -> "github.com/c/d";`,
	}
	for _, e := range wantEdges {
		if !strings.Contains(out, e) {
			t.Errorf("expected edge %q in DOT output, got: %q", e, out)
		}
	}
}

// TestGoGraphToDot_EmptyInput ensures DOT serialization handles empty input.
func TestGoGraphToDot_EmptyInput(t *testing.T) {
	out := goGraphToDot("")
	if !strings.Contains(out, "digraph deps {") || !strings.Contains(out, "}") {
		t.Errorf("expected minimal valid digraph, got: %q", out)
	}
	// Should not contain any edge arrows.
	if strings.Contains(out, "->") {
		t.Errorf("did not expect any edges, got: %q", out)
	}
}

// TestSimplifyModule strips the @version suffix.
func TestSimplifyModule(t *testing.T) {
	cases := map[string]string{
		"github.com/foo/bar@v1.2.3": "github.com/foo/bar",
		"github.com/foo/bar":        "github.com/foo/bar",
		"example.com/x@v0.0.0-2024": "example.com/x",
		"":                          "",
		"@onlyversion":              "@onlyversion", // idx must be > 0
	}
	for in, want := range cases {
		if got := simplifyModule(in); got != want {
			t.Errorf("simplifyModule(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestQuote wraps strings in quotes and escapes inner quotes.
func TestQuote(t *testing.T) {
	cases := map[string]string{
		"hello":      `"hello"`,
		"":           `""`,
		`a"b`:        `"a\"b"`,
		`"x"`:        `"\"x\""`,
		"with/slash": `"with/slash"`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFileExists detects existing and missing paths.
func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "marker")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !fileExists(path) {
		t.Errorf("expected fileExists(%q) = true", path)
	}
	missing := filepath.Join(dir, "nope")
	if fileExists(missing) {
		t.Errorf("expected fileExists(%q) = false", missing)
	}
	// Directories also count as existing.
	if !fileExists(dir) {
		t.Errorf("expected fileExists(%q) = true for dir", dir)
	}
}

// TestRegisterSkills_AppearsInListing ensures the registered skills are
// reachable via Registry.List().
func TestRegisterSkills_AppearsInListing(t *testing.T) {
	reg := skill.NewRegistry()
	RegisterSkills(reg)

	listed := reg.List()
	set := make(map[string]bool, len(listed))
	for _, n := range listed {
		set[n] = true
	}
	for _, want := range []string{"deps_tree", "deps_circular", "deps_graph_image"} {
		if !set[want] {
			t.Errorf("expected %q in listing, got %v", want, listed)
		}
	}
}
