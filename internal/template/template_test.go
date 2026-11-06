package template

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// writeTemplateFile is a helper to write a template YAML file in a directory.
func writeTemplateFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write template file %s: %v", path, err)
	}
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry("/some/path")
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if r.dir != "/some/path" {
		t.Errorf("expected dir '/some/path', got %q", r.dir)
	}
	if r.templates == nil {
		t.Error("templates map not initialized")
	}
	if len(r.templates) != 0 {
		t.Errorf("expected empty templates map, got %d entries", len(r.templates))
	}
}

func TestLoadAll_NonExistentDir(t *testing.T) {
	// Should return nil (no error) when the directory does not exist.
	r := NewRegistry(filepath.Join(os.TempDir(), "polypod-nonexistent-dir-xyz-12345"))
	if err := r.LoadAll(); err != nil {
		t.Errorf("expected nil error for non-existent dir, got %v", err)
	}
	if len(r.templates) != 0 {
		t.Errorf("expected no templates, got %d", len(r.templates))
	}
}

func TestLoadAll_ValidTemplates(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "summarize.yaml", `
name: summarize
description: Summarize text
category: writing
system: You are a summarizer.
user: "Summarize this: {{input}}"
author: tester
`)

	writeTemplateFile(t, dir, "review.yml", `
name: review
description: Code review
category: coding
system: You are a senior engineer.
user: "Review: {{input}}"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	if len(r.templates) != 2 {
		t.Errorf("expected 2 templates, got %d", len(r.templates))
	}

	sum, ok := r.Get("summarize")
	if !ok {
		t.Fatal("summarize template not loaded")
	}
	if sum.Description != "Summarize text" {
		t.Errorf("unexpected description: %q", sum.Description)
	}
	if sum.Category != "writing" {
		t.Errorf("unexpected category: %q", sum.Category)
	}
	if sum.Author != "tester" {
		t.Errorf("unexpected author: %q", sum.Author)
	}

	if _, ok := r.Get("review"); !ok {
		t.Error("review template not loaded (.yml extension)")
	}
}

func TestLoadAll_IgnoresNonYamlAndSubdirs(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "valid.yaml", `
name: valid
description: Valid template
system: Sys
user: "U: {{input}}"
`)
	writeTemplateFile(t, dir, "readme.txt", "not a template")
	writeTemplateFile(t, dir, "data.json", `{"name":"x"}`)

	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	writeTemplateFile(t, dir, filepath.Join("subdir", "ignored.yaml"), `
name: ignored
description: should not load
system: s
user: u
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	if len(r.templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(r.templates))
	}
	if _, ok := r.Get("valid"); !ok {
		t.Error("valid template not loaded")
	}
	if _, ok := r.Get("ignored"); ok {
		t.Error("subdir template should not be loaded")
	}
}

func TestLoadAll_SkipsMalformedYAML(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "broken.yaml", "this: is: not: valid: yaml: [[[")
	writeTemplateFile(t, dir, "good.yaml", `
name: good
description: Good template
system: Hello
user: "Say: {{input}}"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll should not return error on malformed yaml, got %v", err)
	}

	if _, ok := r.Get("good"); !ok {
		t.Error("good template not loaded")
	}
	if _, ok := r.Get("broken"); ok {
		t.Error("broken template should not be loaded")
	}
}

func TestLoadAll_NameDefaultsToFilename(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "noname.yaml", `
description: Template without explicit name
system: Sys
user: "U: {{input}}"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	tpl, ok := r.Get("noname")
	if !ok {
		t.Fatal("expected template registered under filename 'noname'")
	}
	if tpl.Name != "noname" {
		t.Errorf("expected name 'noname', got %q", tpl.Name)
	}
}

func TestGet(t *testing.T) {
	dir := t.TempDir()
	writeTemplateFile(t, dir, "alpha.yaml", `
name: alpha
description: Alpha template
system: Sys
user: "U: {{input}}"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	tests := []struct {
		name      string
		key       string
		wantFound bool
	}{
		{"existing template", "alpha", true},
		{"missing template", "beta", false},
		{"empty name", "", false},
		{"case sensitive miss", "Alpha", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := r.Get(tc.key)
			if ok != tc.wantFound {
				t.Errorf("Get(%q) found=%v, want %v", tc.key, ok, tc.wantFound)
			}
		})
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "a.yaml", `
name: a
description: A
category: coding
system: s
user: u
`)
	writeTemplateFile(t, dir, "b.yaml", `
name: b
description: B
category: writing
system: s
user: u
`)
	writeTemplateFile(t, dir, "c.yaml", `
name: c
description: C
category: coding
system: s
user: u
`)
	writeTemplateFile(t, dir, "d.yaml", `
name: d
description: D
system: s
user: u
`) // no category -> "geral"

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	got := r.List()

	expected := map[string][]string{
		"coding":  {"a", "c"},
		"writing": {"b"},
		"geral":   {"d"},
	}

	if len(got) != len(expected) {
		t.Errorf("expected %d categories, got %d (%v)", len(expected), len(got), got)
	}

	for cat, wantNames := range expected {
		gotNames, ok := got[cat]
		if !ok {
			t.Errorf("missing category %q", cat)
			continue
		}
		// Names should be sorted already by List, but sort again to be safe.
		sort.Strings(gotNames)
		if !reflect.DeepEqual(gotNames, wantNames) {
			t.Errorf("category %q: got %v, want %v", cat, gotNames, wantNames)
		}
	}
}

func TestList_SortsNames(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "zebra.yaml", `
name: zebra
description: Z
category: animals
system: s
user: u
`)
	writeTemplateFile(t, dir, "ant.yaml", `
name: ant
description: A
category: animals
system: s
user: u
`)
	writeTemplateFile(t, dir, "monkey.yaml", `
name: monkey
description: M
category: animals
system: s
user: u
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	got := r.List()
	want := []string{"ant", "monkey", "zebra"}
	if !reflect.DeepEqual(got["animals"], want) {
		t.Errorf("expected sorted %v, got %v", want, got["animals"])
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "translate.yaml", `
name: translate
description: Translate text
category: writing
system: You translate to English.
user: "Translate: {{input}}"
`)

	writeTemplateFile(t, dir, "multi.yaml", `
name: multi
description: Multi placeholder
system: S
user: "First: {{input}} | Second: {{input}} | End"
`)

	writeTemplateFile(t, dir, "noplaceholder.yaml", `
name: noplaceholder
description: Static user prompt
system: S
user: "Hello world"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	tests := []struct {
		name       string
		template   string
		input      string
		wantSystem string
		wantUser   string
		wantErr    bool
	}{
		{
			name:       "simple substitution",
			template:   "translate",
			input:      "ola mundo",
			wantSystem: "You translate to English.",
			wantUser:   "Translate: ola mundo",
		},
		{
			name:       "multiple placeholder occurrences",
			template:   "multi",
			input:      "X",
			wantSystem: "S",
			wantUser:   "First: X | Second: X | End",
		},
		{
			name:       "empty input",
			template:   "translate",
			input:      "",
			wantSystem: "You translate to English.",
			wantUser:   "Translate: ",
		},
		{
			name:       "no placeholder in user prompt",
			template:   "noplaceholder",
			input:      "ignored",
			wantSystem: "S",
			wantUser:   "Hello world",
		},
		{
			name:       "input with special chars",
			template:   "translate",
			input:      "a\tb\nc \"d\" '{{input}}'",
			wantSystem: "You translate to English.",
			wantUser:   "Translate: a\tb\nc \"d\" '{{input}}'",
		},
		{
			name:     "unknown template",
			template: "missing",
			input:    "anything",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sys, usr, err := r.Apply(tc.template, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.template) {
					t.Errorf("error should mention template name %q: %v", tc.template, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sys != tc.wantSystem {
				t.Errorf("system: got %q, want %q", sys, tc.wantSystem)
			}
			if usr != tc.wantUser {
				t.Errorf("user: got %q, want %q", usr, tc.wantUser)
			}
		})
	}
}

func TestApply_DoesNotMutateTemplate(t *testing.T) {
	dir := t.TempDir()

	writeTemplateFile(t, dir, "t.yaml", `
name: t
description: D
system: S
user: "Echo: {{input}}"
`)

	r := NewRegistry(dir)
	if err := r.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// First apply
	_, usr1, err := r.Apply("t", "first")
	if err != nil {
		t.Fatalf("first Apply failed: %v", err)
	}
	if usr1 != "Echo: first" {
		t.Errorf("first user: got %q, want %q", usr1, "Echo: first")
	}

	// Second apply should get a fresh substitution, not accumulate.
	_, usr2, err := r.Apply("t", "second")
	if err != nil {
		t.Fatalf("second Apply failed: %v", err)
	}
	if usr2 != "Echo: second" {
		t.Errorf("second user: got %q, want %q", usr2, "Echo: second")
	}

	// Verify underlying template still has placeholder intact.
	tpl, _ := r.Get("t")
	if tpl.User != "Echo: {{input}}" {
		t.Errorf("template User was mutated: %q", tpl.User)
	}
}
