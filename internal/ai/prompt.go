package ai

import (
	"fmt"
	"strings"
)

// BuildSystemPrompt creates the system prompt with persona and optional knowledge context.
func BuildSystemPrompt(persona string, knowledgeContext string) string {
	if persona == "" {
		persona = "Voce e um assistente inteligente. Seja conciso e direto."
	}
	if knowledgeContext != "" {
		return persona + fmt.Sprintf("\n\nCONTEXTO DISPONIVEL:\n---\n%s\n---", knowledgeContext)
	}
	return persona
}

// FormatKnowledgeContext formats knowledge fragments into a single context string.
func FormatKnowledgeContext(fragments []string) string {
	if len(fragments) == 0 {
		return ""
	}
	return strings.Join(fragments, "\n\n")
}
