// Package commands — batch15_auth.go
//
// Batch 15 (authentication slash commands, wave 3 round 3).
//
// This batch intentionally defers to batch1 for the classic /login, /logout
// and /privacy-settings commands — those were already migrated in batch1 and
// re-registering them here would collide on the command.Registry. Batch 15
// only adds the brand-new placeholder commands that did not exist before:
//
//	/oauth-refresh — placeholder for the forthcoming OAuth refresh flow,
//	                 wired up once internal/mcp/oauth_flow.go is in place.
//
// The registration helper is kept intentionally minimal so that wave 2
// wiring agents can import it without pulling in unrelated side-effects.
package commands

import (
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch15Auth registers the batch-15 authentication commands onto
// the given registry.
//
// NOTE: /login, /logout and /privacy-settings already live in batch1, so we
// deliberately do NOT re-register them here. File/command collisions between
// batches are considered catastrophic for the migration, so this helper only
// registers the genuinely new /oauth-refresh placeholder.
func RegisterBatch15Auth(reg *command.Registry) {
	reg.Register(b15CmdOauthRefresh())
}

// ---------------------------------------------------------------------------
// /oauth-refresh — placeholder for the OAuth refresh wiring
// ---------------------------------------------------------------------------

// b15OauthRefreshMsg is exported as a package-level variable so the tests can
// assert on the user-visible placeholder text without duplicating the string
// literal in multiple places.
var b15OauthRefreshMsg = strings.Join([]string{
	"Atualização de token OAuth",
	"",
	"  Status: pendente de fiação (wiring).",
	"  Implementação prevista em: internal/mcp/oauth_flow.go",
	"",
	"  Enquanto o fluxo não estiver conectado, utilize as credenciais",
	"  estáticas via variáveis de ambiente:",
	"",
	"    export ANTHROPIC_API_KEY=<sua-chave>",
	"    export POLYPOD_API_KEY=<sua-chave>",
	"",
	"  Para acompanhar o progresso, veja o TODO em oauth_flow.go.",
}, "\n")

// b15CmdOauthRefresh returns the placeholder /oauth-refresh command.
//
// The real implementation will delegate to the MCP OAuth flow once that
// package lands; for now the command prints a stable placeholder message so
// the UI, help text and docs can already reference it.
func b15CmdOauthRefresh() *command.Command {
	return &command.Command{
		Name:        "oauth-refresh",
		Aliases:     []string{"refresh-oauth", "oauth-renew"},
		Description: "Renovar token OAuth (pendente de wiring em internal/mcp/oauth_flow.go)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(b15OauthRefreshMsg), nil
		},
	}
}
