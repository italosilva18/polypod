// Package commands — batch29_voice.go
//
// Voice, status-line, combo-git and thinking-replay slash commands for
// the batch 29 surface:
//
//   - /voice            — toggle voice mode on/off.
//   - /statusline       — configure the status line display format.
//   - /commit-push-pr   — combo: commit + push + create PR in one step.
//   - /thinkback-play   — replay extended thinking blocks from the
//     conversation.
//
// All user-facing strings are in Brazilian Portuguese, per project
// convention. Identifiers carry the "B29" suffix to stay isolated from
// other batches.
package commands

import (
	"fmt"
	"strings"
	"sync"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Package-level callback wiring
// ---------------------------------------------------------------------------

// voiceToggleFnB29 is the package-level hook called by /voice to actually
// toggle voice mode. When nil the command falls back to the built-in
// in-memory toggle. Set it via SetVoiceToggleFn before registering
// commands if external voice subsystem integration is needed.
var (
	voiceToggleMu    sync.Mutex
	voiceToggleFnB29 func(enabled bool) error
)

// SetVoiceToggleFn sets the external callback invoked by /voice when
// toggling. Pass nil to revert to the built-in in-memory toggle.
func SetVoiceToggleFn(fn func(enabled bool) error) {
	voiceToggleMu.Lock()
	defer voiceToggleMu.Unlock()
	voiceToggleFnB29 = fn
}

// gitComboFnB29 is the package-level hook called by /commit-push-pr to
// perform the combo git workflow. When nil the command returns a
// "pending wiring" message. Set it via SetGitComboFn.
var (
	gitComboMu    sync.Mutex
	gitComboFnB29 func(cwd string, message string) (string, error)
)

// SetGitComboFn sets the external callback invoked by /commit-push-pr.
// Pass nil to revert to the default placeholder behaviour.
func SetGitComboFn(fn func(cwd string, message string) (string, error)) {
	gitComboMu.Lock()
	defer gitComboMu.Unlock()
	gitComboFnB29 = fn
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch29Voice registers the batch29 voice/status/git-combo/thinking
// commands on the supplied registry.
func RegisterBatch29Voice(reg *command.Registry) {
	reg.Register(cmdVoiceB29())
	reg.Register(cmdStatusLineB29())
	reg.Register(cmdCommitPushPRB29())
	reg.Register(cmdThinkbackPlayB29())
}

// ---------------------------------------------------------------------------
// /voice — toggle voice mode
// ---------------------------------------------------------------------------

// voiceEnabledB29 tracks the current voice-mode state when no external
// toggle callback is configured.
var voiceEnabledB29 bool

// voiceActivatedMessageB29 is the display string shown when voice mode is
// turned on.
const voiceActivatedMessageB29 = "Modo voz ativado"

// voiceDeactivatedMessageB29 is the display string shown when voice mode is
// turned off.
const voiceDeactivatedMessageB29 = "Modo voz desativado"

func cmdVoiceB29() *command.Command {
	return &command.Command{
		Name:        "voice",
		Description: "Alternar modo voz (ativado/desativado)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			voiceToggleMu.Lock()
			fn := voiceToggleFnB29
			voiceToggleMu.Unlock()

			// Flip state.
			voiceEnabledB29 = !voiceEnabledB29

			// Notify external subsystem if wired.
			if fn != nil {
				if err := fn(voiceEnabledB29); err != nil {
					// Revert on failure.
					voiceEnabledB29 = !voiceEnabledB29
					return errResult(fmt.Sprintf("falha ao alternar modo voz: %v", err)), nil
				}
			}

			if voiceEnabledB29 {
				return okResult(voiceActivatedMessageB29), nil
			}
			return okResult(voiceDeactivatedMessageB29), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /statusline — configure status line display
// ---------------------------------------------------------------------------

// statusLineFormatB29 holds the current status line format string.
var statusLineFormatB29 = defaultStatusLineFormatB29

// defaultStatusLineFormatB29 is the built-in status line format applied when
// no custom format has been set.
const defaultStatusLineFormatB29 = "{session} | {model} | {tokens}"

// statusLineCurrentMessageB29 formats the current status line format for
// display.
func statusLineCurrentMessageB29(format string) string {
	return fmt.Sprintf("formato da linha de status: %s", format)
}

// statusLineUpdatedMessageB29 formats the confirmation after updating the
// status line format.
func statusLineUpdatedMessageB29(format string) string {
	return fmt.Sprintf("linha de status atualizada: %s", format)
}

func cmdStatusLineB29() *command.Command {
	return &command.Command{
		Name:        "statusline",
		Description: "Configurar o formato da linha de status",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return okResult(statusLineCurrentMessageB29(statusLineFormatB29)), nil
			}
			statusLineFormatB29 = strings.Join(args, " ")
			return okResult(statusLineUpdatedMessageB29(statusLineFormatB29)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /commit-push-pr — combo git workflow
// ---------------------------------------------------------------------------

// commitPushPRPendingMessageB29 is the display string shown when the git
// combo callback has not been wired yet.
const commitPushPRPendingMessageB29 = "commit-push-pr pendente de wiring (configure via SetGitComboFn)"

// commitPushPRUsageMessageB29 is the display string shown when no message is
// provided and the callback is wired.
const commitPushPRUsageMessageB29 = "uso: /commit-push-pr <mensagem do commit>"

func cmdCommitPushPRB29() *command.Command {
	return &command.Command{
		Name:        "commit-push-pr",
		Description: "Combo: commit + push + criar PR em um passo",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			gitComboMu.Lock()
			fn := gitComboFnB29
			gitComboMu.Unlock()

			if fn == nil {
				return okResult(commitPushPRPendingMessageB29), nil
			}

			msg := strings.Join(args, " ")
			if msg == "" {
				return okResult(commitPushPRUsageMessageB29), nil
			}

			result, err := fn(ctx.WorkingDir, msg)
			if err != nil {
				return errResult(fmt.Sprintf("falha no combo git: %v", err)), nil
			}
			return okResult(result), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /thinkback-play — replay extended thinking blocks
// ---------------------------------------------------------------------------

// thinkbackPlayEmptyMessageB29 is the display string shown when there are no
// thinking blocks to replay.
const thinkbackPlayEmptyMessageB29 = "nenhum bloco de pensamento encontrado na conversa atual"

// thinkbackPlayHeaderB29 is the header line prepended to the replayed
// thinking content.
const thinkbackPlayHeaderB29 = "=== Replay de blocos de pensamento ===\n"

// thinkbackPlayPlaceholderB29 is the full display string returned while the
// conversation-aware wiring for retrieving thinking blocks is pending.
const thinkbackPlayPlaceholderB29 = "replay de pensamento pendente wiring (precisa acessar blocos de thinking da conversa)"

func cmdThinkbackPlayB29() *command.Command {
	return &command.Command{
		Name:        "thinkback-play",
		Description: "Reproduzir blocos de pensamento estendido da conversa",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// The conversation thinking-block retrieval requires wiring
			// that is not yet available. Return the placeholder message.
			return okResult(thinkbackPlayPlaceholderB29), nil
		},
	}
}
