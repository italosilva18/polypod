package skill

import (
	"strings"
	"testing"
)

func TestParseSkillFile_ValidFrontmatter(t *testing.T) {
	content := []byte(`---
name: greeter
description: Says hello in a friendly way
model: gpt-4o
context: fork
aliases:
  - hi
  - hola
allowed-tools:
  - bash
  - read
version: "1.2.3"
visibility: public
---
# Greeter

This is the body of the skill.
`)
	ps, err := ParseSkillFile("/tmp/greeter/SKILL.md", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Frontmatter.Name != "greeter" {
		t.Errorf("Name = %q, want %q", ps.Frontmatter.Name, "greeter")
	}
	if ps.Frontmatter.Description != "Says hello in a friendly way" {
		t.Errorf("Description = %q", ps.Frontmatter.Description)
	}
	if ps.Frontmatter.Model != "gpt-4o" {
		t.Errorf("Model = %q", ps.Frontmatter.Model)
	}
	if ps.Frontmatter.ExecutionContext != "fork" {
		t.Errorf("ExecutionContext = %q", ps.Frontmatter.ExecutionContext)
	}
	if len(ps.Frontmatter.Aliases) != 2 || ps.Frontmatter.Aliases[0] != "hi" {
		t.Errorf("Aliases = %v", ps.Frontmatter.Aliases)
	}
	if len(ps.Frontmatter.AllowedTools) != 2 {
		t.Errorf("AllowedTools = %v", ps.Frontmatter.AllowedTools)
	}
	if ps.Frontmatter.Version != "1.2.3" {
		t.Errorf("Version = %q", ps.Frontmatter.Version)
	}
	if ps.Frontmatter.Visibility != "public" {
		t.Errorf("Visibility = %q", ps.Frontmatter.Visibility)
	}
	if !strings.Contains(ps.Body, "# Greeter") {
		t.Errorf("Body missing heading: %q", ps.Body)
	}
	if strings.Contains(ps.Body, "name: greeter") {
		t.Errorf("Body should not contain frontmatter: %q", ps.Body)
	}
	if ps.SourcePath != "/tmp/greeter/SKILL.md" {
		t.Errorf("SourcePath = %q", ps.SourcePath)
	}
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	content := []byte(`# Just a markdown file

No frontmatter fences at the start.
`)
	ps, err := ParseSkillFile("/x.md", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Frontmatter.Name != "" {
		t.Errorf("expected empty Name, got %q", ps.Frontmatter.Name)
	}
	if !strings.Contains(ps.Body, "# Just a markdown file") {
		t.Errorf("Body truncated: %q", ps.Body)
	}
	if !strings.Contains(ps.Body, "No frontmatter fences") {
		t.Errorf("Body missing content: %q", ps.Body)
	}
}

func TestParseSkillFile_InvalidYAML(t *testing.T) {
	content := []byte(`---
name: broken
description: "unterminated
  nested: [oops
---
body
`)
	_, err := ParseSkillFile("/x.md", content)
	if err == nil {
		t.Fatalf("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parse frontmatter") {
		t.Errorf("error should mention parse frontmatter: %v", err)
	}
}

func TestParseSkillFile_NoClosingFenceFallsBackToBody(t *testing.T) {
	// A file that starts with --- but never closes is treated as body-only.
	content := []byte(`---
this looks like frontmatter but never closes
more text
`)
	ps, err := ParseSkillFile("/x.md", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Frontmatter.Name != "" {
		t.Errorf("expected empty frontmatter, got %+v", ps.Frontmatter)
	}
	if !strings.Contains(ps.Body, "never closes") {
		t.Errorf("Body lost content: %q", ps.Body)
	}
}

func TestFrontmatter_Validate_Happy(t *testing.T) {
	fm := Frontmatter{
		Name:        "ok",
		Description: "a valid skill",
	}
	if err := fm.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	fmWithModel := Frontmatter{
		Name:        "ok",
		Description: "has model",
		Model:       "claude-3-5-sonnet",
		Visibility:  "private",
	}
	if err := fmWithModel.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFrontmatter_Validate_Sad(t *testing.T) {
	cases := []struct {
		label string
		fm    Frontmatter
		want  string
	}{
		{"missing name", Frontmatter{Description: "x"}, "name is required"},
		{"blank name", Frontmatter{Name: "  ", Description: "x"}, "name is required"},
		{"missing description", Frontmatter{Name: "x"}, "description is required"},
		{"blank model", Frontmatter{Name: "x", Description: "x", Model: "   "}, "model set but empty"},
		{"bad visibility", Frontmatter{Name: "x", Description: "x", Visibility: "internal"}, "visibility"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			err := tc.fm.Validate()
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}
