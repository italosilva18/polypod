// Package commands — batch10_env.go
//
// Slash commands for inspecting the process environment and for (eventual)
// remote session wiring:
//
//   - /env          — lists POLYPOD_*, CLAUDE_* and a short allowlist of
//     "key-ish" environment variables (API keys, tokens)
//     in a plain text table. Any secret-looking value is
//     masked by showing only the first 4 characters
//     followed by "***".
//   - /remote-env   — placeholder: "remote env sync pending wiring".
//   - /remote-setup — placeholder: "remote setup pending".
//   - /add-dir      — resolves a filesystem path via filepath.Abs and
//     echoes a "pending session wiring" notice.
//   - /files        — placeholder file listing (pending session wiring).
//
// Wiring: all five commands are registered via RegisterBatch10Env(reg).
// The file is deliberately self-contained and uses only the standard
// library plus github.com/costa/polypod/internal/command.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch10Env registers the five env/remote/files slash commands.
func RegisterBatch10Env(reg *command.Registry) {
	reg.Register(b10CmdEnv())
	reg.Register(b10CmdRemoteEnv())
	reg.Register(b10CmdRemoteSetup())
	reg.Register(b10CmdAddDir())
	reg.Register(b10CmdFiles())
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// b10OK builds a successful command result.
func b10OK(msg string) *command.CommandResult {
	return &command.CommandResult{Display: msg}
}

// b10Err builds an error command result.
func b10Err(msg string) *command.CommandResult {
	return &command.CommandResult{Display: "erro: " + msg}
}

// b10MaskValue masks a potentially sensitive value by keeping the first
// four characters and appending "***". Values shorter than 4 characters
// are fully replaced with "***" to avoid leaking short secrets.
func b10MaskValue(v string) string {
	if len(v) <= 4 {
		return "***"
	}
	return v[:4] + "***"
}

// b10secretSubstrings lists lowercased substrings that trigger masking
// when they appear anywhere in an environment variable name. The list
// is intentionally conservative and covers the common "key-ish" cases.
var b10secretSubstrings = []string{
	"key", "token", "secret", "password", "passwd",
	"api_key", "apikey", "auth", "credential",
}

// b10IsSecretName reports whether an env var name looks like it holds a
// secret and should therefore be masked in /env output.
func b10IsSecretName(name string) bool {
	lower := strings.ToLower(name)
	for _, sub := range b10secretSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// b10KeyishExplicit is the short allowlist of well-known "key-ish" env
// vars that /env surfaces even when they do not match the POLYPOD_* or
// CLAUDE_* prefixes.
var b10KeyishExplicit = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"GROQ_API_KEY",
	"GOOGLE_API_KEY",
	"GEMINI_API_KEY",
	"DEEPSEEK_API_KEY",
	"TOGETHER_API_KEY",
	"MISTRAL_API_KEY",
	"COHERE_API_KEY",
	"HF_TOKEN",
	"HUGGINGFACE_TOKEN",
	"GITHUB_TOKEN",
	"GITLAB_TOKEN",
}

// b10IsKeyishExplicit reports whether name is in the explicit allowlist.
func b10IsKeyishExplicit(name string) bool {
	for _, k := range b10KeyishExplicit {
		if name == k {
			return true
		}
	}
	return false
}

// b10CollectEnv walks os.Environ() once and returns a sorted slice of
// (name, maskedValue) pairs. A var is included when:
//
//   - its name starts with "POLYPOD_" or "CLAUDE_", or
//   - its name appears in the explicit key-ish allowlist, or
//   - its name matches b10IsSecretName (e.g. contains "API_KEY").
//
// Values are masked whenever b10IsSecretName returns true. Non-secret
// POLYPOD_/CLAUDE_ vars are shown verbatim.
func b10CollectEnv() [][2]string {
	seen := map[string]string{}
	for _, kv := range os.Environ() {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		name := kv[:idx]
		value := kv[idx+1:]

		include := false
		switch {
		case strings.HasPrefix(name, "POLYPOD_"):
			include = true
		case strings.HasPrefix(name, "CLAUDE_"):
			include = true
		case b10IsKeyishExplicit(name):
			include = true
		case b10IsSecretName(name):
			include = true
		}
		if !include {
			continue
		}

		if b10IsSecretName(name) || b10IsKeyishExplicit(name) {
			value = b10MaskValue(value)
		}
		seen[name] = value
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([][2]string, 0, len(names))
	for _, n := range names {
		out = append(out, [2]string{n, seen[n]})
	}
	return out
}

// b10FormatEnvTable renders (name, value) pairs as a left-aligned two
// column plain text table. The output always ends with a trailing
// newline so it can be concatenated with other CommandResult.Display
// strings without losing formatting.
func b10FormatEnvTable(rows [][2]string) string {
	if len(rows) == 0 {
		return "(nenhuma variavel de ambiente relevante encontrada)\n"
	}

	// Compute column widths.
	nameW := len("NOME")
	valueW := len("VALOR")
	for _, r := range rows {
		if len(r[0]) > nameW {
			nameW = len(r[0])
		}
		if len(r[1]) > valueW {
			valueW = len(r[1])
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-*s  %-*s\n", nameW, "NOME", valueW, "VALOR")
	fmt.Fprintf(&sb, "%s  %s\n", strings.Repeat("-", nameW), strings.Repeat("-", valueW))
	for _, r := range rows {
		fmt.Fprintf(&sb, "%-*s  %-*s\n", nameW, r[0], valueW, r[1])
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// /env
// ---------------------------------------------------------------------------

func b10CmdEnv() *command.Command {
	return &command.Command{
		Name:        "env",
		Description: "Listar variaveis de ambiente POLYPOD_*, CLAUDE_* e chaves (mascaradas)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			rows := b10CollectEnv()
			return b10OK(b10FormatEnvTable(rows)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /remote-env
// ---------------------------------------------------------------------------

func b10CmdRemoteEnv() *command.Command {
	return &command.Command{
		Name:        "remote-env",
		Description: "Sincronizar variaveis de ambiente com sessao remota (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b10OK("remote env sync pending wiring"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /remote-setup
// ---------------------------------------------------------------------------

func b10CmdRemoteSetup() *command.Command {
	return &command.Command{
		Name:        "remote-setup",
		Description: "Configurar sessao remota (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b10OK("remote setup pending"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /add-dir
// ---------------------------------------------------------------------------

func b10CmdAddDir() *command.Command {
	return &command.Command{
		Name:        "add-dir",
		Description: "Adicionar diretorio ao contexto da sessao (pending wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			if len(args) == 0 {
				return b10Err("uso: /add-dir [caminho]"), nil
			}
			abs, err := filepath.Abs(strings.TrimSpace(args[0]))
			if err != nil {
				return b10Err(fmt.Sprintf("falha ao resolver caminho: %v", err)), nil
			}
			return b10OK(fmt.Sprintf("added (pending session wiring): %s", abs)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// /files
// ---------------------------------------------------------------------------

func b10CmdFiles() *command.Command {
	return &command.Command{
		Name:        "files",
		Description: "Listar arquivos da sessao (placeholder, pending wiring)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return b10OK("files listing pending session wiring"), nil
		},
	}
}
