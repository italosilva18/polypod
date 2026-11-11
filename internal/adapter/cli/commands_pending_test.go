package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentSwitchStopsAtUnresolvedTurn(t *testing.T) {
	setCalls := 0
	result := handleAgentSwitch(commandDeps{
		setAgent: func(string) { setCalls++ },
		checkPendingTurn: func() error {
			return errors.New("ferramenta anterior ainda pendente")
		},
	}, "coder")
	if !result.handled || setCalls != 0 || !strings.Contains(result.output, "pendente") {
		t.Fatalf("agent switch bypassed pending guard: %+v, calls=%d", result, setCalls)
	}
}
