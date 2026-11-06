package commands

import (
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

func TestRegisterAutonomousCommands(t *testing.T) {
	reg := command.NewRegistry()
	RegisterAutonomousCommands(reg)

	for _, name := range []string{"oauth-refresh", "autofix-pr", "secret-scan", "mcp-debug", "testgen", "bughunter"} {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q was not registered", name)
		}
		if cmd.Type != command.CommandPrompt || cmd.GetPrompt == nil {
			t.Fatalf("command %q is not an executable prompt command", name)
		}
		prompt, err := cmd.GetPrompt([]string{"alvo"}, command.CommandContext{WorkingDir: "/workspace"})
		if err != nil {
			t.Fatalf("command %q prompt failed: %v", name, err)
		}
		if !strings.Contains(prompt, "alvo") || !strings.Contains(prompt, "/workspace") {
			t.Fatalf("command %q prompt lost runtime context: %q", name, prompt)
		}
	}
}
