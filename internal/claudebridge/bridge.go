package claudebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Bridge integrates with Claude Code CLI installed on the system.
// Uses `claude -p` headless mode to delegate complex tasks.
type Bridge struct {
	claudePath string
	available  bool
}

// New creates a Claude Code bridge. Auto-detects if claude CLI is installed.
func New() *Bridge {
	path, err := exec.LookPath("claude")
	if err != nil {
		return &Bridge{available: false}
	}
	return &Bridge{claudePath: path, available: true}
}

// IsAvailable returns true if Claude Code CLI is installed.
func (b *Bridge) IsAvailable() bool { return b.available }

// Run executes a prompt via Claude Code headless mode.
func (b *Bridge) Run(ctx context.Context, prompt string, outputFormat string) (string, error) {
	if !b.available {
		return "", fmt.Errorf("claude CLI nao encontrado (instale: npm install -g @anthropic-ai/claude-code)")
	}

	args := []string{"-p", prompt, "--output-format", outputFormat}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(execCtx, b.claudePath, args...)
	out, err := cmd.CombinedOutput()
	if execCtx.Err() == context.DeadlineExceeded {
		return string(out) + "\n[timeout: 5m]", nil
	}
	if err != nil {
		return string(out), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// RunJSON executes a prompt and parses JSON output.
func (b *Bridge) RunJSON(ctx context.Context, prompt string) (map[string]any, error) {
	output, err := b.Run(ctx, prompt, "json")
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("parsing claude output: %w", err)
	}
	return result, nil
}

// RegisterSkills registers Claude Code integration skills.
func RegisterSkills(reg *skill.Registry) {
	bridge := New()

	reg.Register(&skill.Skill{
		Name:        "claude_code",
		Description: "Delegar tarefa complexa para o Claude Code CLI (se instalado)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"prompt": {Type: jsonschema.String, Description: "Prompt para o Claude Code"},
				"format": {Type: jsonschema.String, Description: "Formato: text (default) ou json"},
			},
			Required: []string{"prompt"},
		},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Claude Code CLI nao esta instalado. Instale com: npm install -g @anthropic-ai/claude-code", nil
			}
			format := args["format"]
			if format == "" {
				format = "text"
			}
			return bridge.Run(context.Background(), args["prompt"], format)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "claude_code_review",
		Description: "Pedir code review ao Claude Code (usa modelo Opus para analise profunda)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"target": {Type: jsonschema.String, Description: "Alvo: 'staged', 'unstaged', ou caminho de arquivo"},
			},
		},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Claude Code nao disponivel.", nil
			}
			target := args["target"]
			if target == "" {
				target = "staged"
			}
			prompt := fmt.Sprintf("Faca uma code review detalhada das mudancas %s. Analise bugs, seguranca, performance e legibilidade.", target)
			return bridge.Run(context.Background(), prompt, "text")
		},
	})

	reg.Register(&skill.Skill{
		Name:        "claude_code_available",
		Description: "Verificar se o Claude Code CLI esta instalado e acessivel",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Claude Code CLI NAO encontrado.\n\nPara instalar:\n  npm install -g @anthropic-ai/claude-code\n\nRequisitos:\n  - Node.js 18+\n  - Conta Anthropic (claude.ai)", nil
			}
			// Get version
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bridge.claudePath, "--version")
			out, _ := cmd.Output()
			version := strings.TrimSpace(string(out))
			return fmt.Sprintf("Claude Code CLI disponivel.\nPath: %s\nVersion: %s\n\nSkills disponiveis:\n- claude_code: delegar tarefas complexas\n- claude_code_review: code review profunda", bridge.claudePath, version), nil
		},
	})
}
