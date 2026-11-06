package theme

import (
	"strings"
	"testing"
)

func TestThemePackage(t *testing.T) {
	t.Log("theme package compiled and imported")
}

func TestBuiltinThemes_Exist(t *testing.T) {
	expected := []string{"dark", "light", "monokai", "dracula", "solarized", "nord"}
	for _, name := range expected {
		if _, ok := Themes[name]; !ok {
			t.Errorf("expected builtin theme %q to exist in Themes map", name)
		}
	}
	if len(Themes) < len(expected) {
		t.Errorf("expected at least %d themes, got %d", len(expected), len(Themes))
	}
}

func TestTheme_StructFields(t *testing.T) {
	dark := Themes["dark"]
	if dark.Name != "dark" {
		t.Errorf("expected Name=dark, got %q", dark.Name)
	}
	// All color fields should be non-empty hex values
	fields := map[string]string{
		"Background": dark.Background,
		"Foreground": dark.Foreground,
		"Accent":     dark.Accent,
		"Dim":        dark.Dim,
		"Border":     dark.Border,
		"Success":    dark.Success,
		"Error":      dark.Error,
		"Warning":    dark.Warning,
		"UserBubble": dark.UserBubble,
		"AIBubble":   dark.AIBubble,
	}
	for field, val := range fields {
		if val == "" {
			t.Errorf("dark theme field %s is empty", field)
		}
		if !strings.HasPrefix(val, "#") {
			t.Errorf("dark theme field %s should be a hex color, got %q", field, val)
		}
	}
}

func TestSessionColors_Exist(t *testing.T) {
	expected := []string{"default", "gray", "red", "green", "blue", "yellow", "purple", "orange", "pink", "cyan"}
	for _, name := range expected {
		hex, ok := SessionColors[name]
		if !ok {
			t.Errorf("expected session color %q to exist", name)
			continue
		}
		if !strings.HasPrefix(hex, "#") {
			t.Errorf("session color %q should be hex, got %q", name, hex)
		}
	}
}

func TestNewCurrent_DefaultsToDark(t *testing.T) {
	c := NewCurrent()
	if c == nil {
		t.Fatal("NewCurrent returned nil")
	}
	if c.Theme.Name != "dark" {
		t.Errorf("expected default theme 'dark', got %q", c.Theme.Name)
	}
	if c.SessionColor != "" {
		t.Errorf("expected empty SessionColor, got %q", c.SessionColor)
	}
}

func TestCurrent_SetTheme_Valid(t *testing.T) {
	c := NewCurrent()
	msg, err := c.SetTheme("dracula")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Theme.Name != "dracula" {
		t.Errorf("expected theme 'dracula', got %q", c.Theme.Name)
	}
	if !strings.Contains(msg, "dracula") {
		t.Errorf("expected confirmation message to mention 'dracula', got %q", msg)
	}
}

func TestCurrent_SetTheme_CaseInsensitive(t *testing.T) {
	c := NewCurrent()
	if _, err := c.SetTheme("MONOKAI"); err != nil {
		t.Fatalf("expected case-insensitive lookup to succeed, got %v", err)
	}
	if c.Theme.Name != "monokai" {
		t.Errorf("expected theme 'monokai', got %q", c.Theme.Name)
	}
}

func TestCurrent_SetTheme_Invalid(t *testing.T) {
	c := NewCurrent()
	_, err := c.SetTheme("nonexistent-theme")
	if err == nil {
		t.Fatal("expected error for unknown theme, got nil")
	}
	// Theme should be unchanged
	if c.Theme.Name != "dark" {
		t.Errorf("expected theme to remain 'dark' after failed set, got %q", c.Theme.Name)
	}
}

func TestCurrent_SetSessionColor_Named(t *testing.T) {
	c := NewCurrent()
	msg, err := c.SetSessionColor("red")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.SessionColor != SessionColors["red"] {
		t.Errorf("expected SessionColor=%q, got %q", SessionColors["red"], c.SessionColor)
	}
	if !strings.Contains(msg, "red") {
		t.Errorf("expected message to mention 'red', got %q", msg)
	}
}

func TestCurrent_SetSessionColor_Hex(t *testing.T) {
	c := NewCurrent()
	if _, err := c.SetSessionColor("#abcdef"); err != nil {
		t.Fatalf("expected hex color to succeed, got %v", err)
	}
	if c.SessionColor != "#abcdef" {
		t.Errorf("expected SessionColor=#abcdef, got %q", c.SessionColor)
	}
}

func TestCurrent_SetSessionColor_Invalid(t *testing.T) {
	c := NewCurrent()
	_, err := c.SetSessionColor("not-a-color")
	if err == nil {
		t.Error("expected error for invalid color")
	}
}

func TestCurrent_AccentColor_FallsBackToTheme(t *testing.T) {
	c := NewCurrent()
	// No session color set - should use theme accent
	if c.AccentColor() != c.Theme.Accent {
		t.Errorf("expected accent to equal theme accent %q, got %q", c.Theme.Accent, c.AccentColor())
	}
}

func TestCurrent_AccentColor_SessionOverride(t *testing.T) {
	c := NewCurrent()
	if _, err := c.SetSessionColor("green"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.AccentColor() != SessionColors["green"] {
		t.Errorf("expected accent=%q, got %q", SessionColors["green"], c.AccentColor())
	}
}

func TestCurrent_AccentStyle_ProducesOutput(t *testing.T) {
	c := NewCurrent()
	style := c.AccentStyle()
	rendered := style.Render("hello")
	if !strings.Contains(rendered, "hello") {
		t.Errorf("expected rendered output to contain 'hello', got %q", rendered)
	}
}

func TestListThemes_ContainsAllThemes(t *testing.T) {
	out := ListThemes()
	if out == "" {
		t.Fatal("ListThemes returned empty string")
	}
	for name := range Themes {
		if !strings.Contains(out, name) {
			t.Errorf("ListThemes output missing theme %q", name)
		}
	}
	for name := range SessionColors {
		if !strings.Contains(out, name) {
			t.Errorf("ListThemes output missing session color %q", name)
		}
	}
}
