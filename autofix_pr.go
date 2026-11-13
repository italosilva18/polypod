package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/costa/polypod/internal/adapter"
	"github.com/costa/polypod/internal/command"
	"github.com/costa/polypod/internal/turncheckpoint"
)

// isAutofixPRPrompt reports whether an inbound prompt invokes /autofix-pr
// (either after autonomous slash-command expansion or directly).
func isAutofixPRPrompt(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "[COMANDO AUTONOMO /autofix-pr]") {
		return true
	}
	if strings.HasPrefix(trimmed, "/autofix-pr") {
		rest := strings.TrimPrefix(trimmed, "/autofix-pr")
		return rest == "" || unicode.IsSpace(rune(rest[0]))
	}
	return false
}

func sanitizeProposalFilename(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "proposal"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// finalizeAutofixPRGate enforces the Phase 5 test gate at the end of /autofix-pr:
// if configured verifiers ran and passed, working-tree edits remain applied and
// the turn is marked verified; otherwise, the unified diff is saved under
// .polypod/held-proposals/ and all checkpointed edits from turnIDs are reverted.
func finalizeAutofixPRGate(
	ctx context.Context,
	turns *turncheckpoint.Manager,
	sessionID string,
	turnIDs []string,
	projectDir string,
	verified bool,
	unverifiedReason string,
) string {
	if verified {
		return "\n\n[STATUS /autofix-pr: CORREÇÃO VERIFICADA — verificadores aprovados; alterações mantidas na worktree para revisão.]"
	}
	if strings.TrimSpace(unverifiedReason) == "" {
		unverifiedReason = "verificação não aprovada"
	}
	if turns == nil || sessionID == "" || len(turnIDs) == 0 {
		return fmt.Sprintf("\n\n[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA (%s). Nenhuma alteração verificada foi aplicada.]", unverifiedReason)
	}

	diffs, _ := turns.DiffTurns(sessionID, turnIDs)
	var savedRelPath string
	if len(diffs) > 0 {
		baseDir := projectDir
		if strings.TrimSpace(baseDir) == "" {
			baseDir = "."
		}
		proposalsDir := filepath.Join(baseDir, ".polypod", "held-proposals")
		if mkErr := os.MkdirAll(proposalsDir, 0o700); mkErr == nil {
			var patchBuf strings.Builder
			for _, d := range diffs {
				patchBuf.WriteString(d.Patch)
				if !strings.HasSuffix(d.Patch, "\n") {
					patchBuf.WriteByte('\n')
				}
			}
			fileName := sanitizeProposalFilename(turnIDs[0]) + ".patch"
			fullPath := filepath.Join(proposalsDir, fileName)
			if writeErr := os.WriteFile(fullPath, []byte(patchBuf.String()), 0o600); writeErr == nil {
				savedRelPath = filepath.ToSlash(filepath.Join(".polypod", "held-proposals", fileName))
			}
		}
	}

	restoreCtx := ctx
	if restoreCtx == nil || restoreCtx.Err() != nil {
		restoreCtx = context.Background()
	}
	restored, restoreErr := turns.RestoreTurns(restoreCtx, sessionID, turnIDs)
	if restoreErr != nil {
		return fmt.Sprintf("\n\n[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA (%s). Falha parcial ao reverter worktree: %v.]", unverifiedReason, restoreErr)
	}
	if len(restored.Restored) > 0 {
		if savedRelPath != "" {
			return fmt.Sprintf("\n\n[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA (%s). %d arquivo(s) revertido(s) da worktree; proposta salva em %s.]",
				unverifiedReason, len(restored.Restored), savedRelPath)
		}
		return fmt.Sprintf("\n\n[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA (%s). %d arquivo(s) revertido(s) da worktree.]",
			unverifiedReason, len(restored.Restored))
	}
	return fmt.Sprintf("\n\n[STATUS /autofix-pr: PROPOSTA RETIDA — NÃO VERIFICADA (%s). Nenhuma alteração verificada foi mantida na worktree.]", unverifiedReason)
}

func resolveHeadlessSlashCommand(reg *command.Registry, workingDir, sessionID, raw string) (expandedPrompt string, localDisplay string, handledLocal bool, err error) {
	trimmed := strings.TrimSpace(raw)
	if reg == nil || !strings.HasPrefix(trimmed, "/") {
		return raw, "", false, nil
	}
	withoutSlash := trimmed[1:]
	if withoutSlash == "" {
		return raw, "", false, nil
	}
	end := strings.IndexFunc(withoutSlash, unicode.IsSpace)
	name := withoutSlash
	rest := ""
	if end >= 0 {
		name = withoutSlash[:end]
		rest = strings.TrimSpace(withoutSlash[end:])
	}
	cmd, ok := reg.Get(name)
	if !ok {
		return raw, "", false, nil
	}
	var args []string
	if rest != "" {
		args = strings.Fields(rest)
	}
	cmdCtx := command.CommandContext{WorkingDir: workingDir, SessionID: sessionID}
	switch cmd.Type {
	case command.CommandPrompt:
		if cmd.GetPrompt != nil {
			expanded, promptErr := cmd.GetPrompt(args, cmdCtx)
			if promptErr != nil {
				return "", "", false, promptErr
			}
			return expanded, "", false, nil
		}
	case command.CommandLocal:
		if cmd.Execute != nil {
			res, execErr := cmd.Execute(args, cmdCtx)
			if execErr != nil {
				return "", "", false, execErr
			}
			display := ""
			if res != nil {
				display = res.Display
			}
			return raw, display, true, nil
		}
	}
	return raw, "", false, nil
}

func makeStaticLocalHandlers(display string) (adapter.MessageHandler, adapter.StreamHandler) {
	msgH := func(_ context.Context, msg adapter.InMessage) (adapter.OutMessage, error) {
		return adapter.OutMessage{
			Text:    display,
			Channel: msg.Channel,
			UserID:  msg.UserID,
			ReplyTo: msg.ID,
		}, nil
	}
	streamH := func(_ context.Context, _ adapter.InMessage, chunks chan<- adapter.StreamChunk) {
		defer close(chunks)
		if display != "" {
			chunks <- adapter.StreamChunk{Delta: display}
		}
		chunks <- adapter.StreamChunk{Done: true}
	}
	return msgH, streamH
}
