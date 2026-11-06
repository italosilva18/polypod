// Package commands — batch22_mcp_cmds.go
//
// MCP slash-command placeholders for rodada 3 wave 1 batch 22.
//
// Territory asked for /mcp-refresh, /mcp-token, /mcp-debug and /mcp-reconnect.
// A grep across internal/commands/ confirmed there are no collisions with
// those exact hyphenated names. batch4_security.go already owns /mcp
// (singular), which does not collide with these /mcp-* commands.
//
// The internal/mcp package does expose a live client manager, but this batch
// does not own the wiring that would plumb that client into the slash-command
// layer. The four commands are therefore placeholders that return the agreed
// "pendente wiring" strings described in the territory brief. A later wiring
// agent can replace the Execute bodies with calls against the real MCP
// manager without having to touch these command definitions.
//
// Commands registered by RegisterBatch22MCPCmds:
//
//   - /mcp-refresh             — "atualização MCP pendente wiring"
//   - /mcp-token    [server]   — "rotação de token MCP pendente wiring"
//   - /mcp-debug               — "debug MCP: 0 chamadas (pendente wiring)"
//   - /mcp-reconnect [server]  — "reconexão pendente wiring"
//
// Identifiers are suffixed with "B22" to keep them isolated from the rest of
// the package.
package commands

import (
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Placeholder display strings
// ---------------------------------------------------------------------------
//
// These are kept as package-level constants so the tests can reference them
// without duplicating the literal strings. When the wiring agent lands, the
// constants themselves can be removed (or repurposed as fallback messages).

const (
	mcpRefreshPlaceholderB22   = "atualização MCP pendente wiring"
	mcpTokenPlaceholderB22     = "rotação de token MCP pendente wiring"
	mcpDebugPlaceholderB22     = "debug MCP: 0 chamadas (pendente wiring)"
	mcpReconnectPlaceholderB22 = "reconexão pendente wiring"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch22MCPCmds registers the batch22 MCP management commands on the
// supplied registry. All four commands are CommandLocal / SourceBuiltin and
// return a fixed placeholder message until the real wiring against
// internal/mcp lands.
func RegisterBatch22MCPCmds(reg *command.Registry) {
	reg.Register(cmdMCPRefreshB22())
	reg.Register(cmdMCPTokenB22())
	reg.Register(cmdMCPDebugB22())
	reg.Register(cmdMCPReconnectB22())
}

// ---------------------------------------------------------------------------
// /mcp-refresh — placeholder
// ---------------------------------------------------------------------------

func cmdMCPRefreshB22() *command.Command {
	return &command.Command{
		Name:        "mcp-refresh",
		Description: "Recarregar servidores MCP conectados (em fila até a integração ser concluída)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(mcpRefreshPlaceholderB22), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /mcp-token [server] — placeholder
// ---------------------------------------------------------------------------

func cmdMCPTokenB22() *command.Command {
	return &command.Command{
		Name:        "mcp-token",
		Description: "Rotacionar token de autenticação de um servidor MCP (em fila até a integração ser concluída)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(mcpTokenPlaceholderB22), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /mcp-debug — placeholder
// ---------------------------------------------------------------------------

func cmdMCPDebugB22() *command.Command {
	return &command.Command{
		Name:        "mcp-debug",
		Description: "Mostrar estatísticas de debug de chamadas MCP (em fila até a integração ser concluída)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(mcpDebugPlaceholderB22), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /mcp-reconnect [server] — placeholder
// ---------------------------------------------------------------------------

func cmdMCPReconnectB22() *command.Command {
	return &command.Command{
		Name:        "mcp-reconnect",
		Description: "Reconectar a um servidor MCP específico (em fila até a integração ser concluída)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(mcpReconnectPlaceholderB22), nil
		},
	}
}
