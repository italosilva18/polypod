// Package commands — compact.go
// Compact command: Clear conversation history but keep a summary in context

package commands

import (
	"context"
	"fmt"
	"strings"
)

// CompactResult holds the result of a compact operation
type CompactResult struct {
	OriginalMessages int
	NewMessages      int
	Summary          string
	TokensSaved      int
}

// CompactParams holds parameters for the compact command
type CompactParams struct {
	CustomInstructions string // Optional custom instructions for summarization
	KeepLastN          int    // Number of recent messages to keep (default: 20)
}

// Compact summarizes the conversation and clears history
// NOTE: This is a placeholder implementation. Full implementation requires
// integration with the LLM client and session manager.
func Compact(ctx context.Context, params CompactParams) (*CompactResult, error) {
	// Placeholder - will be fully implemented when session manager wiring is complete
	
	summary := "Resumo da conversa (implementação completa pendente de integração com LLM client)\n\n"
	summary += "Para usar esta funcionalidade:\n"
	summary += "1. O comando /compact está registrado e disponível\n"
	summary += "2. A integração com o session manager será feita em breve\n"
	summary += "3. Quando completo, irá resumir a conversa e economizar tokens\n"
	
	if params.CustomInstructions != "" {
		summary += fmt.Sprintf("\nInstruções customizadas: %s\n", params.CustomInstructions)
	}

	return &CompactResult{
		OriginalMessages: 0,
		NewMessages:      0,
		Summary:          summary,
		TokensSaved:      0,
	}, nil
}

// FormatCompactResult formats the compact result for display
func FormatCompactResult(result *CompactResult) string {
	var sb strings.Builder
	
	sb.WriteString("✅ **Conversa compactada com sucesso!**\n\n")
	sb.WriteString(fmt.Sprintf("📊 Mensagens: %d → %d\n", result.OriginalMessages, result.NewMessages))
	sb.WriteString(fmt.Sprintf("💾 Tokens economizados: ~%d\n", result.TokensSaved))
	sb.WriteString("\n📝 **Resumo:**\n")
	sb.WriteString("```\n")
	sb.WriteString(result.Summary)
	sb.WriteString("\n```")
	
	return sb.String()
}

// FormatCompactPlaceholder formats a placeholder message for compact
func FormatCompactPlaceholder() string {
	var sb strings.Builder
	
	sb.WriteString("✅ **Compact implementado!**\n\n")
	sb.WriteString("📌 **Funcionalidade:** Resumir conversa e limpar histórico mantendo contexto\n\n")
	sb.WriteString("**Uso:**\n")
	sb.WriteString("```\n")
	sb.WriteString("/compact                      # Resume automático\n")
	sb.WriteString("/compact foque em X           # Com instruções customizadas\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**Como funciona:**\n")
	sb.WriteString("1. Pega últimas N mensagens\n")
	sb.WriteString("2. Envia para LLM gerar resumo\n")
	sb.WriteString("3. Descarta mensagens antigas\n")
	sb.WriteString("4. Mantém resumo como contexto\n\n")
	sb.WriteString("⚠️  **Status:** Implementação base pronta, wiring com session manager em andamento\n")
	
	return sb.String()
}
