// Package commands — batch17_trace.go
//
// Slash commands for remote triggering, runtime tracing, and live context
// window visualization:
//
//   - /remote-trigger [name] — placeholder, awaiting wiring into internal/remote/
//   - /ant-trace             — placeholder, awaiting wiring into runtime/trace
//   - /ctx_viz               — ASCII visualization of the current context
//     window usage, sourced from the batch5
//     observability tracker slot when present.
//
// Wiring: registered via RegisterBatch17Trace(reg). The /ctx_viz command
// reads the package-level observabilityState slot that batch5 defines
// (see batch5_observability.go). When the tracker has not been injected
// or has no limit, /ctx_viz falls back to a Portuguese placeholder.
package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tokenbudget"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch17Trace registers /remote-trigger, /ant-trace and /ctx_viz.
func RegisterBatch17Trace(reg *command.Registry) {
	reg.Register(b17CmdRemoteTrigger())
	reg.Register(b17CmdAntTrace())
	reg.Register(b17CmdCtxViz())
}

// ---------------------------------------------------------------------------
// /remote-trigger
// ---------------------------------------------------------------------------

// b17CmdRemoteTrigger is a placeholder pending wiring into internal/remote/.
// It echoes the optional [name] argument back so users can verify the parser
// plumbed arguments correctly while the real implementation is pending.
func b17CmdRemoteTrigger() *command.Command {
	return &command.Command{
		Name:        "remote-trigger",
		Description: "Disparar acao remota (placeholder, aguardando wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			var b strings.Builder
			b.WriteString("remote trigger pending wiring (see internal/remote/)")
			if len(args) > 0 {
				name := strings.TrimSpace(args[0])
				if name != "" {
					fmt.Fprintf(&b, "\nalvo solicitado: %s", name)
				}
			}
			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /ant-trace
// ---------------------------------------------------------------------------

// b17CmdAntTrace is a placeholder pending wiring into runtime/trace.
func b17CmdAntTrace() *command.Command {
	return &command.Command{
		Name:        "ant-trace",
		Description: "Profiling com runtime/trace (placeholder, aguardando wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("ant trace profiling pending wiring (runtime/trace)"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /ctx_viz
// ---------------------------------------------------------------------------

// b17CmdCtxViz renders ASCII bars describing the current context-window
// usage. It pulls data from the batch5 observability slot
// (observabilityState.tracker) so that whatever populates /cost also drives
// /ctx_viz. When no tracker is wired, it emits a Portuguese placeholder.
func b17CmdCtxViz() *command.Command {
	return &command.Command{
		Name:        "ctx_viz",
		Description: "Visualizar uso da janela de contexto (barras ASCII)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			snap := readObservabilityState()
			if snap.tracker == nil {
				return okResult("Nenhum rastreamento de orcamento ativo nesta sessao."), nil
			}
			return okResult(b17RenderCtxViz(snap.tracker)), nil
		},
	}
}

// b17RenderCtxViz builds the human-readable ASCII visualization. The header
// shows the overall budget bar (if a limit is configured) and is followed by
// a per-category bar table normalized against the total consumption.
func b17RenderCtxViz(tr *tokenbudget.BudgetTracker) string {
	total := tr.TotalUsed()
	limit := tr.Limit()

	var b strings.Builder
	b.WriteString("Visualizacao da janela de contexto\n")
	b.WriteString("==================================\n")

	// Overall bar (only when a limit is configured).
	if limit > 0 {
		ratio := tr.UsageRatio()
		if ratio < 0 {
			ratio = 0
		}
		pct := ratio * 100
		fmt.Fprintf(&b, "Total  : %s %5.1f%%  (%s / %s tokens)\n",
			b17Bar(ratio, 20), pct, formatObsInt(total), formatObsInt(limit))
	} else {
		fmt.Fprintf(&b, "Total  : (sem limite)  %s tokens\n", formatObsInt(total))
	}
	b.WriteString("\n")

	// Per-category bars normalized against TotalUsed so the denominator is
	// always meaningful even when the tracker has no configured limit.
	cats := tr.Categories()
	type row struct {
		Name   string
		Tokens int
	}
	var rows []row

	known := tokenbudget.DefaultCategories()
	seen := map[tokenbudget.Category]bool{}
	for _, c := range known {
		seen[c] = true
		rows = append(rows, row{string(c), cats[c]})
	}
	var extras []tokenbudget.Category
	for c := range cats {
		if !seen[c] {
			extras = append(extras, c)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })
	for _, c := range extras {
		rows = append(rows, row{string(c), cats[c]})
	}

	b.WriteString("Por categoria:\n")
	const nameW = 14
	for _, r := range rows {
		ratio := 0.0
		if total > 0 {
			ratio = float64(r.Tokens) / float64(total)
		}
		pct := ratio * 100
		fmt.Fprintf(&b, "%-*s %s %5.1f%%\n",
			nameW, r.Name, b17Bar(ratio, 10), pct)
	}

	return b.String()
}

// b17Bar returns an ASCII progress bar of the form "[████░░░░░░]" with the
// requested total width. The ratio is clamped into [0, 1] — values greater
// than 1 fill the entire bar so exceeded budgets visually max out.
func b17Bar(ratio float64, width int) string {
	if width <= 0 {
		width = 10
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < filled; i++ {
		b.WriteString("\u2588") // █
	}
	for i := filled; i < width; i++ {
		b.WriteString("\u2591") // ░
	}
	b.WriteByte(']')
	return b.String()
}
