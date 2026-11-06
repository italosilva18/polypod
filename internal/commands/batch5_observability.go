// Package commands — batch5_observability.go
// Observability slash commands: /cost, /insights, /usage.
//
// These commands surface the per-category token budget tracker created in
// internal/tokenbudget/wiring.go (round 1 output). Because
// command.CommandContext does not yet expose a reference to the active
// BudgetTracker, this file keeps the tracker in a package-level slot that
// main.go can populate at wiring time via SetObservabilityTracker.
//
// TODO(wiring): CommandContext needs to expose *tokenbudget.BudgetTracker so
// we can drop the package-level slot and plumb a per-session tracker through
// the context instead. Until then, all Executes fall back to a Portuguese
// placeholder when the slot is empty.
package commands

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/tokenbudget"
)

// ---------------------------------------------------------------------------
// Tracker injection slot
// ---------------------------------------------------------------------------

// observabilityState holds everything the observability commands need that
// is not currently plumbed through command.CommandContext. main.go populates
// these via the setter functions below during startup wiring.
var observabilityState = struct {
	mu          sync.RWMutex
	tracker     *tokenbudget.BudgetTracker
	startedAt   time.Time
	activeAgent string
	turns       int
	toolCalls   int
	errorCount  int
}{
	startedAt: time.Now(),
}

// SetObservabilityTracker injects the BudgetTracker used by /cost and /usage.
// Pass nil to clear it. Safe to call from any goroutine.
func SetObservabilityTracker(t *tokenbudget.BudgetTracker) {
	observabilityState.mu.Lock()
	observabilityState.tracker = t
	if observabilityState.startedAt.IsZero() {
		observabilityState.startedAt = time.Now()
	}
	observabilityState.mu.Unlock()
}

// SetObservabilityAgent records the currently active agent name for /insights.
func SetObservabilityAgent(name string) {
	observabilityState.mu.Lock()
	observabilityState.activeAgent = name
	observabilityState.mu.Unlock()
}

// RecordObservabilityTurn bumps the turn counter used by /insights.
func RecordObservabilityTurn() {
	observabilityState.mu.Lock()
	observabilityState.turns++
	observabilityState.mu.Unlock()
}

// RecordObservabilityToolCall bumps the tool-call counter used by /insights.
func RecordObservabilityToolCall() {
	observabilityState.mu.Lock()
	observabilityState.toolCalls++
	observabilityState.mu.Unlock()
}

// RecordObservabilityError bumps the error counter used by /insights.
func RecordObservabilityError() {
	observabilityState.mu.Lock()
	observabilityState.errorCount++
	observabilityState.mu.Unlock()
}

// observabilitySnapshot returns a copy of the mutable state for rendering.
type observabilitySnapshot struct {
	tracker     *tokenbudget.BudgetTracker
	startedAt   time.Time
	activeAgent string
	turns       int
	toolCalls   int
	errorCount  int
}

func readObservabilityState() observabilitySnapshot {
	observabilityState.mu.RLock()
	defer observabilityState.mu.RUnlock()
	return observabilitySnapshot{
		tracker:     observabilityState.tracker,
		startedAt:   observabilityState.startedAt,
		activeAgent: observabilityState.activeAgent,
		turns:       observabilityState.turns,
		toolCalls:   observabilityState.toolCalls,
		errorCount:  observabilityState.errorCount,
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch5Observability registers /cost, /insights and /usage.
// These commands intentionally overwrite any earlier registrations (for
// example the placeholder /insights in batch1.go and the legacy /cost in
// the builtin registry) because this batch wires them to the live
// BudgetTracker.
func RegisterBatch5Observability(reg *command.Registry) {
	reg.Register(cmdCost())
	reg.Register(cmdInsightsObservability())
	reg.Register(cmdUsageObservability())
}

// ---------------------------------------------------------------------------
// /cost
// ---------------------------------------------------------------------------

// cmdCost renders the BudgetTracker's human-readable report, or a Portuguese
// placeholder if no tracker has been wired yet.
func cmdCost() *command.Command {
	return &command.Command{
		Name:        "cost",
		Description: "Mostrar custo da sessao atual (tokens por categoria)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			snap := readObservabilityState()
			if snap.tracker == nil {
				return okResult("Nenhum rastreamento de orcamento ativo nesta sessao."), nil
			}
			return okResult(snap.tracker.BudgetReport()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /insights
// ---------------------------------------------------------------------------

// cmdInsightsObservability prints a short session summary: turn count, total
// tokens (if a tracker is wired), active agent name and uptime.
func cmdInsightsObservability() *command.Command {
	return &command.Command{
		Name:        "insights",
		Description: "Resumo rapido da sessao (turnos, tokens, tool calls, erros)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			snap := readObservabilityState()

			var b strings.Builder
			b.WriteString("Resumo da sessao\n")
			b.WriteString("----------------\n")

			// Uptime
			uptime := time.Duration(0)
			if !snap.startedAt.IsZero() {
				uptime = time.Since(snap.startedAt).Round(time.Second)
			}
			fmt.Fprintf(&b, "Tempo ativo     : %s\n", uptime)

			// Agent
			agent := snap.activeAgent
			if agent == "" {
				agent = "(desconhecido)"
			}
			fmt.Fprintf(&b, "Agente ativo    : %s\n", agent)

			// Turns — prefer tracker turns if available, otherwise local counter.
			turns := snap.turns
			if snap.tracker != nil {
				if tt := snap.tracker.Turns(); tt > turns {
					turns = tt
				}
			}
			fmt.Fprintf(&b, "Turnos          : %d\n", turns)

			// Tool calls / errors from local counters.
			fmt.Fprintf(&b, "Chamadas tool   : %d\n", snap.toolCalls)
			fmt.Fprintf(&b, "Erros           : %d\n", snap.errorCount)

			// Tokens from tracker, if wired.
			if snap.tracker != nil {
				total := snap.tracker.TotalUsed()
				fmt.Fprintf(&b, "Tokens totais   : %s\n", formatObsInt(total))
				if limit := snap.tracker.Limit(); limit > 0 {
					ratio := snap.tracker.UsageRatio() * 100
					fmt.Fprintf(&b, "Limite          : %s (%.1f%% usado)\n",
						formatObsInt(limit), ratio)
				}
			} else {
				b.WriteString("Tokens totais   : (rastreamento nao ativo)\n")
			}

			return okResult(b.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /usage
// ---------------------------------------------------------------------------

// cmdUsageObservability is a developer-focused breakdown of /cost: it renders
// a per-category table using lipgloss when possible, with a plain-text
// fallback if lipgloss width computations return something unusable.
func cmdUsageObservability() *command.Command {
	return &command.Command{
		Name:        "usage",
		Aliases:     []string{"breakdown"},
		Description: "Detalhamento por categoria (tabela de tokens)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			snap := readObservabilityState()
			if snap.tracker == nil {
				return okResult("Nenhum rastreamento de orcamento ativo nesta sessao."), nil
			}
			return okResult(renderUsageTable(snap.tracker)), nil
		},
	}
}

// renderUsageTable builds a per-category table for /usage. It tries lipgloss
// first and falls back to a simple padded plain-text layout on any failure.
func renderUsageTable(tr *tokenbudget.BudgetTracker) string {
	cats := tr.Categories()
	total := tr.TotalUsed()

	// Collect rows in a stable order: known categories first, extras alpha.
	type row struct {
		Category string
		Tokens   int
		Pct      float64
	}
	var rows []row

	known := tokenbudget.DefaultCategories()
	seen := make(map[tokenbudget.Category]bool, len(known))
	for _, c := range known {
		seen[c] = true
		v := cats[c]
		pct := 0.0
		if total > 0 {
			pct = float64(v) / float64(total) * 100
		}
		rows = append(rows, row{string(c), v, pct})
	}
	var extras []tokenbudget.Category
	for c := range cats {
		if !seen[c] {
			extras = append(extras, c)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })
	for _, c := range extras {
		v := cats[c]
		pct := 0.0
		if total > 0 {
			pct = float64(v) / float64(total) * 100
		}
		rows = append(rows, row{string(c), v, pct})
	}

	// Build plain-text fallback first — it's always the source of truth for
	// tests. The lipgloss-styled version wraps around this output.
	var b strings.Builder
	b.WriteString("Detalhamento de uso (/usage)\n")
	b.WriteString("============================\n")
	fmt.Fprintf(&b, "Turnos : %d\n", tr.Turns())
	fmt.Fprintf(&b, "Total  : %s tokens\n", formatObsInt(total))
	if limit := tr.Limit(); limit > 0 {
		fmt.Fprintf(&b, "Limite : %s (%.1f%%)\n", formatObsInt(limit), tr.UsageRatio()*100)
	}
	b.WriteString("\n")

	// Header
	const (
		catW = 14
		tokW = 12
		pctW = 8
	)
	header := fmt.Sprintf("%-*s %*s %*s", catW, "Categoria", tokW, "Tokens", pctW, "Percent")
	sep := strings.Repeat("-", catW+1+tokW+1+pctW)
	b.WriteString(header + "\n")
	b.WriteString(sep + "\n")
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-*s %*s %*.1f%%\n",
			catW, r.Category,
			tokW, formatObsInt(r.Tokens),
			pctW-1, r.Pct))
	}

	plain := b.String()

	// Wrap in a lipgloss border for the TUI. If lipgloss explodes for any
	// reason (width computation, terminal detection), fall back to plain.
	styled := func() (out string) {
		defer func() {
			if r := recover(); r != nil {
				out = plain
			}
		}()
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			Render(plain)
		if box == "" || !strings.Contains(box, "Categoria") {
			return plain
		}
		return box
	}()
	return styled
}

// formatObsInt formats integers with underscore thousands separators so the
// /cost and /usage outputs stay consistent with tokenbudget.BudgetReport.
func formatObsInt(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return sign + s
	}
	var out strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		out.WriteString(s[:pre])
		if len(s) > pre {
			out.WriteByte('_')
		}
	}
	for i := pre; i < len(s); i += 3 {
		out.WriteString(s[i : i+3])
		if i+3 < len(s) {
			out.WriteByte('_')
		}
	}
	return sign + out.String()
}
