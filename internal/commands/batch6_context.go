// Package commands — batch6_context.go
//
// Slash commands exposing the context-management subsystem:
//
//   - /compact [instrucoes]  — full compaction of the current conversation,
//     optionally guided by custom instructions.
//   - /snip                  — lossy tool-result snipping (structural,
//     no LLM call).
//   - /context               — shows token budget usage for the session.
//
// Wiring: command.CommandContext (in internal/command/registry.go) carries
// optional function fields (CompactFn, SnipFn, BudgetReportFn) that callers
// populate when the subsystems are available. When these fields are nil the
// commands fall back to a Portuguese placeholder message — this keeps them
// idempotent and safe to invoke even when no conversation is loaded.
package commands

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch6Context registers /compact, /snip, and /context.
//
// Note: internal/command/registry.go's RegisterBuiltinCommands already
// registers a stub /compact entry with no Execute handler. Re-registering
// here overwrites the stub with our real (placeholder-for-now) handler, so
// the dispatcher will route invocations to us instead of the empty stub.
func RegisterBatch6Context(reg *command.Registry) {
	reg.Register(cmdCompact())
	reg.Register(cmdSnip())
	reg.Register(cmdContext())
}

// ---------------------------------------------------------------------------
// /compact — trigger conversation compaction
// ---------------------------------------------------------------------------

func cmdCompact() *command.Command {
	return &command.Command{
		Name:        "compact",
		Description: "Compactar contexto da conversa (opcional: instrucoes)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Optional custom instructions: join all args so
			//   /compact focar no historico do git
			// and
			//   /compact "focus on the git history"
			// both work the same way.
			instructions := strings.TrimSpace(strings.Join(args, " "))
			instructions = strings.Trim(instructions, `"'`)

			// If the compact service is wired, perform real compaction.
			if ctx.CompactFn != nil {
				result, err := ctx.CompactFn(instructions)
				if err != nil {
					// Surface the error as a user-friendly message rather
					// than propagating it as a Go error, so the command
					// pipeline treats it as a normal output.
					return okResult(fmt.Sprintf("Erro na compactacao: %v", err)), nil
				}
				return okResult(fmt.Sprintf(
					"Compactacao concluida: %d -> %d tokens (%d mensagens resumidas).",
					result.PreCompactTokens, result.PostCompactTokens, result.MessagesRemoved,
				)), nil
			}

			// Fallback: service not yet wired.
			var sb strings.Builder
			sb.WriteString("Compactacao solicitada, mas o servico de compactacao ainda nao esta ligado a sessao.\n")
			sb.WriteString("\n")
			sb.WriteString("Status: fila (aguardando wiring de command.CommandContext.CompactService).\n")
			if instructions != "" {
				sb.WriteString(fmt.Sprintf("Instrucoes personalizadas capturadas: %q\n", instructions))
			}
			if ctx.SessionID != "" {
				sb.WriteString(fmt.Sprintf("Sessao: %s\n", ctx.SessionID))
			}
			sb.WriteString("\n")
			sb.WriteString("Quando o wiring estiver pronto, esta chamada acionara compact.CompactService.Compact\n")
			sb.WriteString("sobre a conversa atual, resumindo os grupos antigos e preservando os recentes.")
			return okResult(sb.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /snip — trigger structural snip of old tool results
// ---------------------------------------------------------------------------

func cmdSnip() *command.Command {
	return &command.Command{
		Name:        "snip",
		Description: "Reduzir resultados de ferramentas antigos (sem chamar LLM)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// If the snip function is wired, perform real snipping.
			if ctx.SnipFn != nil {
				saved, err := ctx.SnipFn()
				if err != nil {
					return okResult(fmt.Sprintf("Erro no snip: %v", err)), nil
				}
				return okResult(fmt.Sprintf(
					"Snip concluido: ~%d tokens liberados.", saved,
				)), nil
			}

			// Fallback: service not yet wired.
			var sb strings.Builder
			sb.WriteString("Snip solicitado, mas o servico de compactacao ainda nao esta ligado a sessao.\n")
			sb.WriteString("\n")
			sb.WriteString("Status: fila (aguardando wiring de command.CommandContext.Snipper).\n")
			sb.WriteString("Politica padrao: manter ultimos 5 resultados, reter 200 bytes dos antigos.\n")
			if ctx.SessionID != "" {
				sb.WriteString(fmt.Sprintf("Sessao: %s\n", ctx.SessionID))
			}
			sb.WriteString("\n")
			sb.WriteString("Quando o wiring estiver pronto, esta chamada acionara compact.SnipToolResults\n")
			sb.WriteString("com compact.DefaultSnipPolicy() sobre a conversa atual.")
			return okResult(sb.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /context — show context window usage
// ---------------------------------------------------------------------------

func cmdContext() *command.Command {
	return &command.Command{
		Name:        "context",
		Aliases:     []string{"ctx"},
		Description: "Mostrar uso da janela de contexto",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// If the budget tracker is wired, render a real report.
			if ctx.BudgetReportFn != nil {
				snap, err := ctx.BudgetReportFn()
				if err != nil {
					return okResult(fmt.Sprintf("Erro ao consultar orcamento: %v", err)), nil
				}
				var sb strings.Builder
				sb.WriteString("Uso da janela de contexto:\n\n")
				sb.WriteString("  " + renderBudgetBar(snap.Used, snap.Limit) + "\n")
				sb.WriteString(fmt.Sprintf("  Tokens usados: %d / %d\n", snap.Used, snap.Limit))
				sb.WriteString(fmt.Sprintf("  Turnos: %d\n", snap.Turns))
				if snap.Report != "" {
					sb.WriteString("\n")
					sb.WriteString(snap.Report)
				}
				if ctx.SessionID != "" {
					sb.WriteString(fmt.Sprintf("\nSessao: %s\n", ctx.SessionID))
				}
				return okResult(sb.String()), nil
			}

			// Fallback: tracker not yet wired.
			var sb strings.Builder
			sb.WriteString("Sem tracker de orcamento ativo na sessao atual.\n")
			sb.WriteString("\n")
			sb.WriteString("Status: fila (aguardando wiring de command.CommandContext.BudgetTracker).\n")
			sb.WriteString("\n")
			// Render a placeholder visual so users see the shape of the
			// eventual report.
			sb.WriteString("Estado esperado (placeholder):\n")
			sb.WriteString("  " + renderBudgetBar(0, 0) + "\n")
			sb.WriteString("  Tokens usados:          --- / ---\n")
			sb.WriteString("  Limite de auto-compact: ---\n")
			sb.WriteString("  Compactacoes: 0\n")
			if ctx.SessionID != "" {
				sb.WriteString(fmt.Sprintf("\nSessao: %s\n", ctx.SessionID))
			}
			return okResult(sb.String()), nil
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// renderBudgetBar returns a 20-cell visual progress bar using the markers
// requested in the task (● full, ◐ partial, ○ empty). A zero limit renders an
// all-empty bar, which matches the placeholder state before wiring lands.
func renderBudgetBar(used, limit int) string {
	const width = 20
	if limit <= 0 {
		return "[" + strings.Repeat("\u25CB", width) + "]"
	}
	ratio := float64(used) / float64(limit)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	fullCells := int(ratio * float64(width))
	// Partial cell: one half-filled marker if there's leftover fraction.
	partial := 0
	if fullCells < width {
		frac := ratio*float64(width) - float64(fullCells)
		if frac >= 0.5 {
			partial = 1
		}
	}
	empty := width - fullCells - partial
	if empty < 0 {
		empty = 0
	}
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(strings.Repeat("\u25CF", fullCells))
	if partial == 1 {
		b.WriteString("\u25D0")
	}
	b.WriteString(strings.Repeat("\u25CB", empty))
	b.WriteByte(']')
	return b.String()
}
