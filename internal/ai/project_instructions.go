package ai

import (
	"context"
	"log/slog"

	"github.com/costa/polypod/internal/project"
)

func appendProjectInstructions(ctx context.Context, toolName string, args map[string]any, content string, logger *slog.Logger) string {
	instructions, err := project.InstructionsAfterTool(ctx, toolName, args)
	if err != nil && logger != nil {
		logger.Warn("project instructions after tool unavailable", "tool", toolName, "error", err)
	}
	if instructions == "" {
		return content
	}
	return content + "\n\n[Instruções AGENTS.md aplicáveis ao caminho usado]\n" + instructions
}
