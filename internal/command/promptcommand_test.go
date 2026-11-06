package command

import (
	"errors"
	"strings"
	"testing"
)

func newTestPromptCommand(name string, aliases []string, body string) *PromptCommand {
	return &PromptCommand{
		Name:        name,
		Description: "test command " + name,
		Aliases:     aliases,
		GetPromptFn: func(ctx PromptContext) (string, error) {
			if ctx.Args != "" {
				return body + " args=" + ctx.Args, nil
			}
			return body, nil
		},
		InjectAsUser:    true,
		ProgressMessage: name + " running",
	}
}

func TestPromptRegistry_RegisterLookupByNameAndAlias(t *testing.T) {
	pr := NewPromptRegistry()
	cmd := newTestPromptCommand("review", []string{"r", "rev"}, "please review")
	pr.Register(cmd)

	// Lookup by canonical name
	got, ok := pr.Get("review")
	if !ok || got == nil {
		t.Fatalf("expected to find review by name")
	}
	if got.Name != "review" {
		t.Errorf("expected name=review, got %q", got.Name)
	}

	// Lookup by aliases
	for _, alias := range []string{"r", "rev"} {
		got, ok := pr.Get(alias)
		if !ok || got == nil {
			t.Fatalf("expected to find command by alias %q", alias)
		}
		if got.Name != "review" {
			t.Errorf("alias %q resolved to %q, want review", alias, got.Name)
		}
	}

	// Miss
	if _, ok := pr.Get("unknown"); ok {
		t.Errorf("expected miss for unknown")
	}

	// Count & List
	if pr.Count() != 1 {
		t.Errorf("expected count=1, got %d", pr.Count())
	}
	list := pr.List()
	if len(list) != 1 || list[0].Name != "review" {
		t.Errorf("unexpected list: %+v", list)
	}

	// Aliases map
	aliases := pr.Aliases()
	if aliases["r"] != "review" || aliases["rev"] != "review" {
		t.Errorf("unexpected aliases map: %v", aliases)
	}
}

func TestPromptRegistry_RegisterNilAndEmptyName(t *testing.T) {
	pr := NewPromptRegistry()
	pr.Register(nil)
	pr.Register(&PromptCommand{Name: ""})
	if pr.Count() != 0 {
		t.Errorf("expected empty registry, got %d", pr.Count())
	}
}

func TestPromptCommand_Expand_CallsFnAndWrapsResult(t *testing.T) {
	cmd := newTestPromptCommand("commit", nil, "create commit")
	res, err := cmd.Expand(PromptContext{Args: "now", SessionID: "s1"})
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if res == nil {
		t.Fatalf("Expand returned nil result")
	}
	if !strings.Contains(res.PromptText, "create commit") || !strings.Contains(res.PromptText, "args=now") {
		t.Errorf("unexpected PromptText: %q", res.PromptText)
	}
	if !res.InjectAsUser {
		t.Errorf("expected InjectAsUser=true")
	}
	if res.Display != "commit running" {
		t.Errorf("unexpected Display: %q", res.Display)
	}

	// Error propagation
	boom := &PromptCommand{
		Name: "boom",
		GetPromptFn: func(ctx PromptContext) (string, error) {
			return "", errors.New("kaboom")
		},
	}
	if _, err := boom.Expand(PromptContext{}); err == nil {
		t.Errorf("expected error from failing GetPromptFn")
	}

	// Missing GetPromptFn
	noFn := &PromptCommand{Name: "nofn"}
	if _, err := noFn.Expand(PromptContext{}); err == nil {
		t.Errorf("expected error when GetPromptFn is nil")
	}

	// Default Display when ProgressMessage empty
	plain := &PromptCommand{
		Name: "plain",
		GetPromptFn: func(ctx PromptContext) (string, error) {
			return "ok", nil
		},
	}
	res2, err := plain.Expand(PromptContext{})
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if res2.Display != "/plain" {
		t.Errorf("expected default Display=/plain, got %q", res2.Display)
	}
	if res2.InjectAsUser {
		t.Errorf("expected default InjectAsUser=false")
	}
}

func TestIsPromptCommandName(t *testing.T) {
	pr := NewPromptRegistry()
	pr.Register(newTestPromptCommand("explain", []string{"ex"}, "explain this"))

	if !IsPromptCommandName("explain", pr) {
		t.Errorf("expected true for canonical name")
	}
	if !IsPromptCommandName("ex", pr) {
		t.Errorf("expected true for alias")
	}
	if IsPromptCommandName("unknown", pr) {
		t.Errorf("expected false for unknown name")
	}
	if IsPromptCommandName("explain", nil) {
		t.Errorf("expected false for nil registry")
	}
	if IsPromptCommandName("", pr) {
		t.Errorf("expected false for empty name")
	}
}
