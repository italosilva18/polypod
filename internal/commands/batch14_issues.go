// Package commands — batch14_issues.go
//
// Slash commands for issue triage / review helpers (placeholders that will
// be wired to real backends in later rounds):
//
//   - /issue [search|new]  — prints the github.com/anthropics/claude-code/issues
//     URL for searching, or the "new issue" URL.
//   - /advisor             — placeholder "advisor pending wiring".
//   - /bughunter           — placeholder "bughunter pending wiring".
//   - /insights-adv        — advanced insights view backed by the
//     internal/insights package. Registered under
//     /insights-adv (NOT /insights) because the
//     plain /insights name is already claimed by
//     batch1 and batch5_observability.
//
// Wiring: RegisterBatch14Issues(reg).
package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/insights"
)

// issuesBaseURL is the canonical upstream issue tracker. Placeholders point
// here so users at least get a useful link until real backends are wired.
const issuesBaseURL = "https://github.com/anthropics/claude-code/issues"

// RegisterBatch14Issues registers the batch14 issue/review slash commands
// on the provided command registry.
//
// Collision handling:
//   - /insights already exists in batch1 and batch5_observability. To avoid
//     clobbering those when batch14 is registered later in the wiring order,
//     the insights-backed command here is exposed as /insights-adv.
func RegisterBatch14Issues(reg *command.Registry) {
	reg.Register(b14CmdIssue())
	reg.Register(b14CmdAdvisor())
	reg.Register(b14CmdBughunter())
	reg.Register(b14CmdInsightsAdv())
}

// b14OK builds a successful command result.
func b14OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// b14Err builds an error command result.
func b14Err(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "erro: " + msg}
}

// ---------------------------------------------------------------------------
// /issue [search|new]
// ---------------------------------------------------------------------------

// b14CmdIssue returns the /issue placeholder command. It supports two
// subcommands:
//
//	/issue search [query...]   → prints the search URL on the upstream tracker
//	/issue new                 → prints the "new issue" URL
//
// With no subcommand (or "help"), it prints usage.
func b14CmdIssue() *command.Command {
	return &command.Command{
		Name:        "issue",
		Description: "Abrir ou buscar issues no repositorio upstream (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b14OK(strings.Join([]string{
					"uso: /issue [search|new] [termos...]",
					"  search  — abrir busca em " + issuesBaseURL,
					"  new     — abrir pagina de criacao de issue",
				}, "\n")), nil
			}
			sub := strings.ToLower(strings.TrimSpace(args[0]))
			switch sub {
			case "search", "s", "find":
				query := strings.TrimSpace(strings.Join(args[1:], " "))
				if query == "" {
					return b14OK(fmt.Sprintf("Issue search URL: %s", issuesBaseURL)), nil
				}
				// GitHub accepts `?q=is:issue+<terms>` — we keep it simple and
				// URL-escape only the spaces, which is all GitHub really needs.
				escaped := strings.ReplaceAll(query, " ", "+")
				return b14OK(fmt.Sprintf(
					"Issue search URL: %s?q=is%%3Aissue+%s",
					issuesBaseURL, escaped,
				)), nil
			case "new", "create", "open":
				return b14OK(fmt.Sprintf("New issue URL: %s/new", issuesBaseURL)), nil
			case "help", "-h", "--help":
				return b14OK(strings.Join([]string{
					"uso: /issue [search|new] [termos...]",
					"  search  — abrir busca em " + issuesBaseURL,
					"  new     — abrir pagina de criacao de issue",
				}, "\n")), nil
			default:
				return b14Err(fmt.Sprintf(
					"subcomando invalido %q. Opcoes: search, new",
					args[0],
				)), nil
			}
		},
	}
}

// ---------------------------------------------------------------------------
// /advisor — placeholder
// ---------------------------------------------------------------------------

// b14CmdAdvisor returns the /advisor placeholder. Note: batch2 defines a
// different, fully-featured /advisor command, but that one is wired through
// the unrelated SlashCommand framework, not through *command.Command, so
// there is no registry collision on the command.Registry used here.
func b14CmdAdvisor() *command.Command {
	return &command.Command{
		Name:        "advisor",
		Description: "Configurar o modelo consultor (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b14OK("advisor pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /bughunter — placeholder
// ---------------------------------------------------------------------------

// b14CmdBughunter returns the /bughunter placeholder.
func b14CmdBughunter() *command.Command {
	return &command.Command{
		Name:        "bughunter",
		Description: "Caca-bugs automatico (pendente de wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b14OK("bughunter pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /insights-adv — uses internal/insights package
// ---------------------------------------------------------------------------

// b14CmdInsightsAdv exposes the internal/insights package through a slash
// command. We register under /insights-adv because the plain /insights name
// is already taken by batch1 and batch5_observability, and the task spec
// explicitly asked to skip/rename on collision and note it in the output.
func b14CmdInsightsAdv() *command.Command {
	return &command.Command{
		Name:        "insights-adv",
		Description: "Insights avancados da sessao (via internal/insights)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// We do not have real session data plumbed in yet, so we build
			// a synthetic SessionData payload just to exercise the formatter.
			// Once the session/usage pipeline is wired, this handler should
			// read live numbers from ctx or a session store.
			data := insights.SessionData{
				Duration:   15 * time.Minute,
				Requests:   0,
				Tokens:     0,
				Cost:       0,
				ToolCalls:  map[string]int{},
				ErrorCount: 0,
				TopFiles:   map[string]int{},
			}
			body := insights.FormatQuickInsights(data)
			header := "Nota: /insights ja existe (batch1/batch5); este comando esta registrado como /insights-adv.\n\n"
			return b14OK(header + body), nil
		},
	}
}
