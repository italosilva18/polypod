// Package commands — batch19_misc.go
//
// Miscellaneous placeholders for the rodada 3 wave 1 batch 19.
//
// Task territory asked for /btw, /feedback, /fast and /extra-usage, with a
// "skip duplicates" rule against batch1.go and batch2.go. A grep of batch1.go
// confirmed that /btw (line 301), /fast (line 580) and /feedback (line 623)
// already exist in batch1.go — so per the skip-duplicates rule only
// /extra-usage is registered by this file.
//
// Commands registered by RegisterBatch19Misc:
//
//   - /extra-usage — placeholder that shows the extra usage indicators panel
//     ("extra usage indicators pending wiring") until the
//     surrounding telemetry / billing surface is wired up.
//
// Identifiers are suffixed with "B19" to keep them isolated from the rest of
// the package even though there is currently no collision risk.
package commands

import (
	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch19Misc registers the batch19 miscellaneous commands on the
// supplied registry.
//
// /btw, /feedback and /fast are intentionally NOT registered here because
// batch1.go already owns those names; re-registering them would shadow the
// real implementations with placeholders, which would be a regression.
func RegisterBatch19Misc(reg *command.Registry) {
	reg.Register(cmdExtraUsageB19())
}

// ---------------------------------------------------------------------------
// /extra-usage — placeholder
// ---------------------------------------------------------------------------

// extraUsagePlaceholderMessage is the exact display string returned by the
// /extra-usage command while the real telemetry wiring is pending. Kept as a
// package constant so tests can reference it without duplicating the literal.
const extraUsagePlaceholderMessage = "extra usage indicators pending wiring"

func cmdExtraUsageB19() *command.Command {
	return &command.Command{
		Name:        "extra-usage",
		Description: "Mostrar indicadores extras de uso (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult(extraUsagePlaceholderMessage), nil
		},
	}
}
