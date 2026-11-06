// Package commands — batch18_sandbox.go
//
// Sandbox toggle plus four UI / desktop / mobile handoff placeholders.
//
// Commands registered by RegisterBatch18Sandbox:
//
//   - /sandbox-toggle  — flips a "sandbox" boolean inside the per-session
//     JSON blob (writeSessionJSON / readSessionJSON
//     helpers from batch1.go). Persists across the
//     session lifetime; the surrounding harness reads
//     the same key when deciding whether to wrap shell
//     execution in a sandbox.
//   - /ide             — placeholder ("IDE handoff pending wiring") that
//     will eventually hand the active project off to a
//     running IDE companion process.
//   - /chrome          — placeholder ("chrome integration pending wiring")
//     for the upcoming browser-side bridge.
//   - /desktop         — placeholder ("desktop app handoff pending wiring")
//     for handing the conversation off to the native
//     desktop client.
//   - /mobile          — placeholder ("mobile handoff pending wiring") for
//     the QR-code mobile pairing flow.
//
// All user-facing strings are Brazilian Portuguese (or the exact English
// placeholder strings spelled out in the task), per project convention.
//
// Function and type identifiers are suffixed with "B18" to avoid colliding
// with the legacy cmdIDE/cmdMobile in batch1.go and the
// cmdSandboxToggle/cmdChrome/cmdDesktop helpers in batch2.go (which use a
// different SlashCommand type entirely).
package commands

import (
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch18Sandbox registers /sandbox-toggle, /ide, /chrome, /desktop
// and /mobile on the supplied registry. Re-registering names that already
// exist (e.g. /ide and /mobile from batch1) is intentional: the batch18
// variants are the new sandbox-aware handlers, and command.Registry.Register
// last-write-wins, so callers should invoke RegisterBatch18Sandbox after
// RegisterBatch1 if they want batch18 to take effect.
func RegisterBatch18Sandbox(reg *command.Registry) {
	reg.Register(cmdSandboxToggleB18())
	reg.Register(cmdIDEB18())
	reg.Register(cmdChromeB18())
	reg.Register(cmdDesktopB18())
	reg.Register(cmdMobileB18())
}

// ---------------------------------------------------------------------------
// /sandbox-toggle — flip the sandbox flag in session JSON
// ---------------------------------------------------------------------------

// sandboxSessionKey is the JSON field used to persist the toggle. Kept as a
// package constant so tests can reach for it without hardcoding the literal.
const sandboxSessionKey = "sandbox"

// readSandboxFlag returns the current sandbox flag for the session, defaulting
// to false when the session JSON is missing or the key is unset. Any decode
// error is treated as "off" so the toggle is always safe to call on a fresh
// session.
func readSandboxFlag(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	m, err := readSessionJSON(sessionID)
	if err != nil || m == nil {
		return false
	}
	v, ok := m[sandboxSessionKey]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "on", "yes", "sim":
			return true
		}
	}
	return false
}

// writeSandboxFlag persists the new sandbox flag, preserving any other keys
// already present in the session JSON.
func writeSandboxFlag(sessionID string, enabled bool) error {
	if sessionID == "" {
		return fmt.Errorf("sessao vazia")
	}
	m, err := readSessionJSON(sessionID)
	if err != nil || m == nil {
		m = map[string]any{}
	}
	m[sandboxSessionKey] = enabled
	return writeSessionJSON(sessionID, m)
}

func cmdSandboxToggleB18() *command.Command {
	return &command.Command{
		Name:        "sandbox-toggle",
		Description: "Alternar modo sandbox para execucao de comandos",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if ctx.SessionID == "" {
				return errResult("sessao indisponivel para persistir flag de sandbox"), nil
			}

			// Allow explicit on/off via argument; otherwise toggle the
			// current value. Mirrors the /debug-tool-call ergonomics.
			current := readSandboxFlag(ctx.SessionID)
			var newState bool
			if len(args) > 0 {
				switch strings.ToLower(strings.TrimSpace(args[0])) {
				case "on", "true", "1", "yes", "sim":
					newState = true
				case "off", "false", "0", "no", "nao":
					newState = false
				default:
					return errResult(fmt.Sprintf(
						"valor invalido: %q (use on/off)", args[0])), nil
				}
			} else {
				newState = !current
			}

			if err := writeSandboxFlag(ctx.SessionID, newState); err != nil {
				return errResult(fmt.Sprintf(
					"falha ao gravar estado de sandbox: %v", err)), nil
			}

			if newState {
				return okResult("Modo sandbox: LIGADO"), nil
			}
			return okResult("Modo sandbox: DESLIGADO"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /ide — placeholder
// ---------------------------------------------------------------------------

func cmdIDEB18() *command.Command {
	return &command.Command{
		Name:        "ide",
		Description: "Handoff para IDE companion (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("IDE handoff pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /chrome — placeholder
// ---------------------------------------------------------------------------

func cmdChromeB18() *command.Command {
	return &command.Command{
		Name:        "chrome",
		Description: "Integracao com Chrome (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("chrome integration pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /desktop — placeholder
// ---------------------------------------------------------------------------

func cmdDesktopB18() *command.Command {
	return &command.Command{
		Name:        "desktop",
		Description: "Handoff para aplicativo desktop (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("desktop app handoff pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /mobile — placeholder
// ---------------------------------------------------------------------------

func cmdMobileB18() *command.Command {
	return &command.Command{
		Name:        "mobile",
		Description: "Handoff para aplicativo mobile (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("mobile handoff pending wiring"), nil
		},
	}
}
