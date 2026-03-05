package ai

import (
	"fmt"
	"strings"
)

// BuildSystemPrompt creates the system prompt with persona, optional knowledge context, and optional memory context.
func BuildSystemPrompt(persona string, knowledgeContext string, memoryContext string) string {
	if persona == "" {
		persona = "Voce e um assistente inteligente. Seja conciso e direto."
	}

	var b strings.Builder
	b.WriteString(persona)

	if memoryContext != "" {
		b.WriteString(fmt.Sprintf("\n\n%s", memoryContext))
	}
	if knowledgeContext != "" {
		b.WriteString(fmt.Sprintf("\n\nCONTEXTO DISPONIVEL:\n---\n%s\n---", knowledgeContext))
	}
	return b.String()
}

// FormatKnowledgeContext formats knowledge fragments into a single context string.
func FormatKnowledgeContext(fragments []string) string {
	if len(fragments) == 0 {
		return ""
	}
	return strings.Join(fragments, "\n\n")
}
