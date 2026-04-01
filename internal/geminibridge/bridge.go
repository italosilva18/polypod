package geminibridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// Bridge integrates with Gemini CLI (google-gemini/gemini-cli) installed on the system.
// Apache-2.0 licensed, installed via: npm install -g @anthropic-ai/gemini-cli
type Bridge struct {
	geminiPath string
	available  bool
}

// New creates a Gemini CLI bridge. Auto-detects if gemini CLI is installed.
func New() *Bridge {
	path, err := exec.LookPath("gemini")
	if err != nil {
		return &Bridge{available: false}
	}
	return &Bridge{geminiPath: path, available: true}
}

// IsAvailable returns true if Gemini CLI is installed.
func (b *Bridge) IsAvailable() bool { return b.available }

// Run executes a prompt via Gemini CLI headless mode.
func (b *Bridge) Run(ctx context.Context, prompt string) (string, error) {
	if !b.available {
		return "", fmt.Errorf("gemini CLI nao encontrado (instale: npm install -g @google/gemini-cli)")
	}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(execCtx, b.geminiPath, "-p", prompt)
	out, err := cmd.CombinedOutput()
	if execCtx.Err() == context.DeadlineExceeded {
		return string(out) + "\n[timeout: 5m]", nil
	}
	if err != nil {
		return string(out), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// RegisterSkills registers Gemini CLI integration skills.
func RegisterSkills(reg *skill.Registry) {
	bridge := New()

	reg.Register(&skill.Skill{
		Name:        "gemini_cli",
		Description: "Delegar tarefa para o Gemini CLI (Google, se instalado)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"prompt": {Type: jsonschema.String, Description: "Prompt para o Gemini CLI"},
			},
			Required: []string{"prompt"},
		},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Gemini CLI nao instalado. Instale: npm install -g @google/gemini-cli", nil
			}
			return bridge.Run(context.Background(), args["prompt"])
		},
	})

	reg.Register(&skill.Skill{
		Name:        "gemini_search",
		Description: "Usar Gemini CLI com Google Search grounding (pesquisa web nativa)",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"query": {Type: jsonschema.String, Description: "Pergunta para pesquisar com Google Search"},
			},
			Required: []string{"query"},
		},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Gemini CLI nao disponivel.", nil
			}
			prompt := fmt.Sprintf("Pesquise na web e responda com fontes: %s", args["query"])
			return bridge.Run(context.Background(), prompt)
		},
	})

	reg.Register(&skill.Skill{
		Name:        "gemini_available",
		Description: "Verificar se o Gemini CLI esta instalado",
		Parameters:  jsonschema.Definition{Type: jsonschema.Object, Properties: map[string]jsonschema.Definition{}},
		Execute: func(args map[string]string) (string, error) {
			if !bridge.IsAvailable() {
				return "Gemini CLI NAO encontrado.\n\nPara instalar:\n  npm install -g @google/gemini-cli\n\nRequisitos:\n  - Node.js 18+\n  - Google API key ou conta Google", nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bridge.geminiPath, "--version")
			out, _ := cmd.Output()
			return fmt.Sprintf("Gemini CLI disponivel.\nPath: %s\nVersion: %s", bridge.geminiPath, strings.TrimSpace(string(out))), nil
		},
	})
}
