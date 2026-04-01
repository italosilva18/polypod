package repomap

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildGoProject(t *testing.T) {
	dir := t.TempDir()

	// Create a simple Go file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`
package main

import "fmt"

func main() {
	fmt.Println(Hello())
}

func Hello() string {
	return "hello"
}

type Config struct {
	Name string
}
`), 0644)

	rm := NewRepoMap(dir, slog.Default())
	if err := rm.Build(); err != nil {
		t.Fatalf("Build error: %v", err)
	}

	if len(rm.symbols) == 0 {
		t.Fatal("should have found symbols")
	}

	// Should find main, Hello, Config
	names := make(map[string]bool)
	for _, s := range rm.symbols {
		names[s.Name] = true
	}

	if !names["main"] {
		t.Error("should find 'main' function")
	}
	if !names["Hello"] {
		t.Error("should find 'Hello' function")
	}
	if !names["Config"] {
		t.Error("should find 'Config' type")
	}
}

func TestPageRank(t *testing.T) {
	rm := NewRepoMap(".", slog.Default())

	// Manually add symbols and refs for testing
	rm.symbols = []Symbol{
		{Name: "A", Kind: "function", File: "a.go", Line: 1, Exported: true},
		{Name: "B", Kind: "function", File: "b.go", Line: 1, Exported: true},
		{Name: "C", Kind: "function", File: "c.go", Line: 1, Exported: true},
	}
	rm.graph = map[string][]string{
		"a.go": {"b.go"},
		"b.go": {"c.go"},
		"c.go": {"a.go", "b.go"},
	}

	scores := rm.PageRank(nil, 20, 0.85)
	if len(scores) == 0 {
		t.Fatal("PageRank should return scores")
	}

	// All files should have positive scores
	for file, score := range scores {
		if score <= 0 {
			t.Errorf("file %s has non-positive score: %f", file, score)
		}
	}
}

func TestPageRankPersonalization(t *testing.T) {
	rm := NewRepoMap(".", slog.Default())
	rm.symbols = []Symbol{
		{Name: "A", Kind: "function", File: "important.go", Exported: true},
		{Name: "B", Kind: "function", File: "other.go", Exported: true},
	}
	rm.graph = map[string][]string{}

	// With personalization toward important.go
	scores := rm.PageRank([]string{"important.go"}, 20, 0.85)

	if scores["important.go"] <= scores["other.go"] {
		t.Error("personalized file should have higher score")
	}
}

func TestRankedSymbols(t *testing.T) {
	rm := NewRepoMap(".", slog.Default())
	rm.symbols = []Symbol{
		{Name: "Public", Kind: "function", File: "a.go", Exported: true},
		{Name: "private", Kind: "function", File: "a.go", Exported: false},
		{Name: "Type", Kind: "struct", File: "b.go", Exported: true},
	}
	rm.graph = map[string][]string{}

	ranked := rm.RankedSymbols(nil, 1000)

	// Should include exported, skip unexported
	hasPublic := false
	hasPrivate := false
	for _, s := range ranked {
		if s.Name == "Public" {
			hasPublic = true
		}
		if s.Name == "private" {
			hasPrivate = true
		}
	}
	if !hasPublic {
		t.Error("should include exported 'Public'")
	}
	if hasPrivate {
		t.Error("should skip unexported 'private'")
	}
}

func TestFormatMap(t *testing.T) {
	rm := NewRepoMap(".", slog.Default())
	rm.symbols = []Symbol{
		{Name: "Hello", Kind: "function", File: "main.go", Line: 5, Exported: true},
	}
	rm.graph = map[string][]string{}

	output := rm.FormatMap(nil, 1000)
	if output == "" {
		t.Fatal("FormatMap should not be empty")
	}
	if !contains(output, "Hello") {
		t.Error("output should contain 'Hello'")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
