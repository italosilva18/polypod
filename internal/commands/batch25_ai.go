// Package commands — batch25_ai.go
//
// Placeholders (plus one real helper) for the rodada 3 wave 1 batch 25 AI
// helper surface. The commands here cover the "talk about models" menu that
// will eventually hang off the provider/model plumbing:
//
//   - /model-compare   — side-by-side model comparison (pending wiring).
//   - /provider-switch — swap the active provider at runtime (pending wiring).
//   - /token-count     — estimate the token cost of a string using a rough
//     len(text) / 4 heuristic, matching the TypeScript
//     reference implementation. This one is real (not a
//     placeholder) because it needs no provider access.
//
// Identifiers are suffixed with "B25" so they stay isolated from the rest of
// the package even though there is currently no collision risk with the other
// batches under internal/commands.
package commands

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch25AI registers the batch25 AI helper commands on the supplied
// registry.
func RegisterBatch25AI(reg *command.Registry) {
	reg.Register(cmdModelCompareB25())
	reg.Register(cmdProviderSwitchB25())
	reg.Register(cmdTokenCountB25())
}

// ---------------------------------------------------------------------------
// /model-compare — placeholder
// ---------------------------------------------------------------------------

// modelComparePlaceholderMessage is the exact display string returned by the
// /model-compare command while the real wiring is pending. Kept as a package
// constant so tests can reference it without duplicating the literal.
const modelComparePlaceholderMessage = "comparar modelos pendente wiring (use /model para trocar)"

func cmdModelCompareB25() *command.Command {
	return &command.Command{
		Name:        "model-compare",
		Description: "Comparar modelos lado a lado (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(modelComparePlaceholderMessage), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /provider-switch — placeholder
// ---------------------------------------------------------------------------

// providerSwitchPlaceholderMessage is the exact display string returned by the
// /provider-switch command while the real wiring is pending.
const providerSwitchPlaceholderMessage = "troca de provider pendente wiring (edite config.yaml)"

func cmdProviderSwitchB25() *command.Command {
	return &command.Command{
		Name:        "provider-switch",
		Description: "Trocar provider ativo em tempo de execucao (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(providerSwitchPlaceholderMessage), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /token-count — rough heuristic estimator
// ---------------------------------------------------------------------------

// tokenCountHelpMessage is the exact display string returned by /token-count
// when called without arguments.
const tokenCountHelpMessage = "uso: /token-count <texto>  (estimativa aproximada: len(texto)/4)"

// estimateTokensB25 replicates the TypeScript reference heuristic used by the
// front-end: one token is roughly four characters. It never returns a negative
// value and returns zero for empty strings.
func estimateTokensB25(text string) int {
	if text == "" {
		return 0
	}
	return len(text) / 4
}

// formatTokenCountB25 builds the display line for a given input text and its
// estimated token count. Kept as a helper so tests can assert the exact
// wording without depending on fmt.Sprintf call sites scattered across the
// file.
func formatTokenCountB25(text string, tokens int) string {
	return fmt.Sprintf("estimativa: ~%d tokens (%d caracteres, heuristica len/4)",
		tokens, len(text))
}

func cmdTokenCountB25() *command.Command {
	return &command.Command{
		Name:        "token-count",
		Description: "Estimar a contagem aproximada de tokens de um texto (heuristica len/4)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return okResult(tokenCountHelpMessage), nil
			}
			text := strings.Join(args, " ")
			tokens := estimateTokensB25(text)
			return okResult(formatTokenCountB25(text, tokens)), nil
		},
	}
}
