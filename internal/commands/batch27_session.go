// Package commands — batch27_session.go
//
// Session management slash commands for batch 27:
//
//   - /session   — exibe informações da sessão atual (ID, início, mensagens,
//     tokens).
//   - /share     — exporta a sessão atual para JSON compartilhável.
//     Aceita caminho de saída opcional.
//   - /teleport  — pula para um turno específico no histórico da conversa.
//     Parâmetro: número do turno.
//   - /resume    — retoma uma sessão anterior pelo ID.
//     Parâmetro: session_id.
//
// Identifiers carry the "B27" suffix to stay isolated from the rest of the
// package.
package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/costa/polypod/internal/command"
)

// startTimeB27 records the process start time used for session reporting.
var startTimeB27 = time.Now()

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch27Session registers the batch27 session management commands on
// the supplied registry.
func RegisterBatch27Session(reg *command.Registry) {
	reg.Register(cmdSessionB27())
	reg.Register(cmdShareB27())
	reg.Register(cmdTeleportB27())
	reg.Register(cmdResumeB27())
}

// ---------------------------------------------------------------------------
// /session — show current session info
// ---------------------------------------------------------------------------

func cmdSessionB27() *command.Command {
	return &command.Command{
		Name:        "session",
		Description: "Mostrar informações da sessão atual (ID, início, mensagens, tokens)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			uptime := time.Since(startTimeB27).Round(time.Second)
			startStr := startTimeB27.Format("2006-01-02 15:04:05")

			sid := ctx.SessionID
			if sid == "" {
				sid = "(não disponível)"
			}

			// Try to obtain token budget info if the wiring is available.
			tokenInfo := "(não disponível)"
			msgCount := "(não disponível)"
			if ctx.BudgetReportFn != nil {
				snap, err := ctx.BudgetReportFn()
				if err == nil && snap != nil {
					tokenInfo = fmt.Sprintf("%d / %d", snap.Used, snap.Limit)
					msgCount = fmt.Sprintf("%d", snap.Turns)
				}
			}

			msg := fmt.Sprintf(
				"sessão: %s\ninício: %s\nuptime: %s\nmensagens: %s\ntokens: %s",
				sid, startStr, uptime, msgCount, tokenInfo,
			)
			return okResult(msg), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /share — export session to shareable JSON
// ---------------------------------------------------------------------------

// sessionSummaryB27 is the JSON structure exported by /share.
type sessionSummaryB27 struct {
	SessionID string `json:"session_id"`
	StartTime string `json:"start_time"`
	Uptime    string `json:"uptime"`
	Tokens    string `json:"tokens,omitempty"`
	Messages  string `json:"messages,omitempty"`
}

func cmdShareB27() *command.Command {
	return &command.Command{
		Name:        "share",
		Description: "Exportar a sessão atual para um formato compartilhável (JSON)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			summary := sessionSummaryB27{
				SessionID: ctx.SessionID,
				StartTime: startTimeB27.Format(time.RFC3339),
				Uptime:    time.Since(startTimeB27).Round(time.Second).String(),
			}

			if ctx.BudgetReportFn != nil {
				snap, err := ctx.BudgetReportFn()
				if err == nil && snap != nil {
					summary.Tokens = fmt.Sprintf("%d/%d", snap.Used, snap.Limit)
					summary.Messages = fmt.Sprintf("%d", snap.Turns)
				}
			}

			data, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return errResult(fmt.Sprintf("falha ao serializar sessão: %v", err)), nil
			}

			// If an output path was provided, write to file.
			if len(args) > 0 && args[0] != "" {
				outPath := args[0]
				if writeErr := os.WriteFile(outPath, data, 0o644); writeErr != nil {
					return errResult(fmt.Sprintf("falha ao escrever %s: %v", outPath, writeErr)), nil
				}
				return okResult(fmt.Sprintf("sessão exportada para %s", outPath)), nil
			}

			return okResult(string(data)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /teleport — jump to a specific turn in conversation history
// ---------------------------------------------------------------------------

func cmdTeleportB27() *command.Command {
	return &command.Command{
		Name:        "teleport",
		Description: "Pular para um turno específico no histórico da conversa",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 || args[0] == "" {
				return errResult("uso: /teleport <número_do_turno>"), nil
			}

			turnNum, err := strconv.Atoi(args[0])
			if err != nil || turnNum < 1 {
				return errResult(fmt.Sprintf("número de turno inválido: %s", args[0])), nil
			}

			// Placeholder — actual history navigation requires conversation
			// manager wiring which will be connected in a later integration.
			return okResult(fmt.Sprintf("teleporte para turno %d pendente wiring (precisa acessar histórico de conversação)", turnNum)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /resume — resume a previous session by ID
// ---------------------------------------------------------------------------

func cmdResumeB27() *command.Command {
	return &command.Command{
		Name:        "resume",
		Description: "Retomar uma sessão anterior pelo ID",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 || args[0] == "" {
				return errResult("uso: /resume <session_id>"), nil
			}

			sessionID := args[0]

			// Placeholder — actual session resume requires session persistence
			// wiring which will be connected in a later integration.
			return okResult(fmt.Sprintf("retomada da sessão %q pendente wiring (precisa acessar gerenciador de sessões)", sessionID)), nil
		},
	}
}
