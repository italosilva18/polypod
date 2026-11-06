package cli

import (
	"testing"

	"github.com/costa/polypod/internal/command"
)

func TestProcessCommandFallsBackToRegistry(t *testing.T) {
	reg := command.NewRegistry()
	reg.Register(&command.Command{
		Name: "local-extra",
		Type: command.CommandLocal,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return &command.CommandResult{Display: "executado"}, nil
		},
	})
	reg.Register(&command.Command{
		Name: "prompt-extra",
		Type: command.CommandPrompt,
		GetPrompt: func(args []string, ctx command.CommandContext) (string, error) {
			return "prompt expandido", nil
		},
	})

	deps := commandDeps{commandRegistry: reg}
	local := processCommand("/local-extra", deps, nil)
	if !local.handled || local.output != "executado" {
		t.Fatalf("local registry command = %+v", local)
	}

	prompt := processCommand("/prompt-extra", deps, nil)
	if prompt.handled || prompt.modelInput != "prompt expandido" {
		t.Fatalf("prompt registry command = %+v", prompt)
	}
}
