// Package commands — batch22_mcp_cmds_test.go
//
// Smoke tests for the batch22 MCP slash-command placeholders. The commands
// are placeholders pending the internal/mcp wiring, so the assertions pin
// down:
//
//   - all four names are registered on the registry
//   - each command is CommandLocal / SourceBuiltin with a non-nil Execute
//   - each Execute returns the agreed placeholder string (with or without
//     args)
//   - registration does NOT clobber the pre-existing /mcp (singular) command
//     from batch4_security.go (collision sanity check)
package commands

import (
	"testing"

	"github.com/costa/polypod/internal/command"
)

// newBatch22Ctx returns a CommandContext suitable for batch22 tests.
func newBatch22Ctx(sessionID string) command.CommandContext {
	return command.CommandContext{
		WorkingDir: ".",
		SessionID:  sessionID,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegisterBatch22MCPCmds_RegistersAll(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch22MCPCmds(reg)

	want := []string{"mcp-refresh", "mcp-token", "mcp-debug", "mcp-reconnect"}
	for _, name := range want {
		cmd, ok := reg.Get(name)
		if !ok {
			t.Fatalf("command %q not registered", name)
		}
		if cmd.Execute == nil {
			t.Errorf("command %q has nil Execute handler", name)
		}
		if cmd.Type != command.CommandLocal {
			t.Errorf("command %q: expected CommandLocal, got %q", name, cmd.Type)
		}
		if cmd.Source != command.SourceBuiltin {
			t.Errorf("command %q: expected SourceBuiltin, got %q", name, cmd.Source)
		}
		if cmd.Description == "" {
			t.Errorf("command %q: empty description", name)
		}
	}
}

// TestRegisterBatch22MCPCmds_DoesNotClobberMCPSingular confirms that
// registering batch22 on a fresh registry leaves the /mcp (singular) name
// alone — batch22 owns the hyphenated /mcp-* variants, not /mcp itself.
func TestRegisterBatch22MCPCmds_DoesNotClobberMCPSingular(t *testing.T) {
	reg := command.NewRegistry()
	RegisterBatch22MCPCmds(reg)

	if _, ok := reg.Get("mcp"); ok {
		t.Errorf("batch22 should NOT register %q (lives in batch4_security.go)", "mcp")
	}
}

// ---------------------------------------------------------------------------
// Placeholder outputs
// ---------------------------------------------------------------------------

func TestCmdMCPRefreshB22_Placeholder(t *testing.T) {
	res, err := cmdMCPRefreshB22().Execute(nil, newBatch22Ctx("batch22-mcp-refresh-session"))
	if err != nil {
		t.Fatalf("cmdMCPRefreshB22 error: %v", err)
	}
	if res == nil || res.Display != mcpRefreshPlaceholderB22 {
		t.Errorf("unexpected /mcp-refresh placeholder: got %q, want %q",
			displayOrNil(res), mcpRefreshPlaceholderB22)
	}
}

func TestCmdMCPTokenB22_Placeholder(t *testing.T) {
	res, err := cmdMCPTokenB22().Execute(
		[]string{"github"},
		newBatch22Ctx("batch22-mcp-token-session"),
	)
	if err != nil {
		t.Fatalf("cmdMCPTokenB22 error: %v", err)
	}
	if res == nil || res.Display != mcpTokenPlaceholderB22 {
		t.Errorf("unexpected /mcp-token placeholder: got %q, want %q",
			displayOrNil(res), mcpTokenPlaceholderB22)
	}
}

func TestCmdMCPDebugB22_Placeholder(t *testing.T) {
	res, err := cmdMCPDebugB22().Execute(nil, newBatch22Ctx("batch22-mcp-debug-session"))
	if err != nil {
		t.Fatalf("cmdMCPDebugB22 error: %v", err)
	}
	if res == nil || res.Display != mcpDebugPlaceholderB22 {
		t.Errorf("unexpected /mcp-debug placeholder: got %q, want %q",
			displayOrNil(res), mcpDebugPlaceholderB22)
	}
}

func TestCmdMCPReconnectB22_Placeholder(t *testing.T) {
	res, err := cmdMCPReconnectB22().Execute(
		[]string{"filesystem"},
		newBatch22Ctx("batch22-mcp-reconnect-session"),
	)
	if err != nil {
		t.Fatalf("cmdMCPReconnectB22 error: %v", err)
	}
	if res == nil || res.Display != mcpReconnectPlaceholderB22 {
		t.Errorf("unexpected /mcp-reconnect placeholder: got %q, want %q",
			displayOrNil(res), mcpReconnectPlaceholderB22)
	}
}
