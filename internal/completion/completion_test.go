package completion

import (
	"strings"
	"testing"
)

func TestBashContainsFunction(t *testing.T) {
	out := Bash()
	if !strings.Contains(out, "_polypod_completions()") {
		t.Errorf("Bash() missing _polypod_completions function definition")
	}
	if !strings.Contains(out, "complete -F _polypod_completions polypod") {
		t.Errorf("Bash() missing 'complete -F' registration")
	}
}

func TestBashContainsKeyOptions(t *testing.T) {
	out := Bash()
	expected := []string{
		"--setup",
		"--help",
		"--version",
		"doctor",
		"init",
		"mcp",
		"completion",
		"bash zsh fish",
		"text json stream-json",
		"edit plan ask auto",
	}
	for _, e := range expected {
		if !strings.Contains(out, e) {
			t.Errorf("Bash() missing expected token %q", e)
		}
	}
}

func TestBashUsesCompgen(t *testing.T) {
	out := Bash()
	if !strings.Contains(out, "compgen") {
		t.Errorf("Bash() output does not reference compgen")
	}
	if !strings.Contains(out, "COMPREPLY") {
		t.Errorf("Bash() output does not reference COMPREPLY")
	}
}

func TestZshHasCompdefDirective(t *testing.T) {
	out := Zsh()
	// The #compdef directive must be the first line for zsh to recognize it.
	if !strings.HasPrefix(out, "#compdef polypod") {
		t.Errorf("Zsh() must begin with '#compdef polypod', got: %q", firstLine(out))
	}
}

func TestZshContainsDescribeAndCommands(t *testing.T) {
	out := Zsh()
	required := []string{
		"_polypod()",
		"_describe",
		"'doctor:",
		"'init:",
		"'mcp:",
		"'completion:",
		"'--setup:",
		"'--help:",
		"'--version:",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Errorf("Zsh() missing required fragment %q", r)
		}
	}
}

func TestFishUsesCompleteCommand(t *testing.T) {
	out := Fish()
	if !strings.Contains(out, "complete -c polypod") {
		t.Errorf("Fish() must use 'complete -c polypod' entries")
	}
	// Fish should NOT contain bash-specific constructs.
	if strings.Contains(out, "compgen") {
		t.Errorf("Fish() output should not contain bash compgen")
	}
	if strings.Contains(out, "#compdef") {
		t.Errorf("Fish() output should not contain zsh #compdef")
	}
}

func TestFishContainsSubcommandsAndOptions(t *testing.T) {
	out := Fish()
	required := []string{
		"-l setup",
		"-l help",
		"-l version",
		"-a doctor",
		"-a init",
		"-a mcp",
		"-a completion",
		"text json stream-json",
		"edit plan ask auto",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Errorf("Fish() missing required fragment %q", r)
		}
	}
}

func TestGenerateDispatchesCorrectly(t *testing.T) {
	cases := []struct {
		shell string
		want  string
	}{
		{"bash", Bash()},
		{"zsh", Zsh()},
		{"fish", Fish()},
	}
	for _, c := range cases {
		got, err := Generate(c.shell)
		if err != nil {
			t.Errorf("Generate(%q) returned unexpected error: %v", c.shell, err)
			continue
		}
		if got != c.want {
			t.Errorf("Generate(%q) returned script that does not match %s()", c.shell, c.shell)
		}
	}
}

func TestGenerateUnknownShellReturnsError(t *testing.T) {
	out, err := Generate("powershell")
	if err == nil {
		t.Fatalf("Generate(\"powershell\") expected error, got nil")
	}
	if out != "" {
		t.Errorf("Generate unknown shell should return empty string, got %q", out)
	}
	if !strings.Contains(err.Error(), "powershell") {
		t.Errorf("error message should mention the unknown shell name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bash") ||
		!strings.Contains(err.Error(), "zsh") ||
		!strings.Contains(err.Error(), "fish") {
		t.Errorf("error message should list supported shells, got: %v", err)
	}
}

func TestGenerateEmptyShellReturnsError(t *testing.T) {
	_, err := Generate("")
	if err == nil {
		t.Errorf("Generate(\"\") expected error for empty shell name")
	}
}

func TestAllScriptsAreNonEmpty(t *testing.T) {
	if strings.TrimSpace(Bash()) == "" {
		t.Errorf("Bash() returned empty script")
	}
	if strings.TrimSpace(Zsh()) == "" {
		t.Errorf("Zsh() returned empty script")
	}
	if strings.TrimSpace(Fish()) == "" {
		t.Errorf("Fish() returned empty script")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
