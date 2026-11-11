package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/types"
)

const (
	historySearchPreviewChars = 300
	historyRecallDefaultChars = 6000
	historyRecallMaxChars     = 12000
)

// RegisterHistoryRecallTools exposes only the request's own durable journal.
// The model cannot choose a channel, user, branch, or agent through arguments.
func RegisterHistoryRecallTools(reg *Registry, store *conversation.Store) {
	if reg == nil || store == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "history_search" },
		DescriptionFn: func() string {
			return "Buscar mensagens originais antigas desta conversa por texto. Retorna IDs para history_recall; " +
				"use before_sequence para continuar em mensagens mais antigas após a compactação."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"query":           map[string]any{"type": "string", "description": "Texto a localizar nas mensagens originais"},
				"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"before_sequence": map[string]any{"type": "integer", "minimum": 1, "description": "Cursor next_before da busca anterior"},
			}, "required": []string{"query"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateHistorySearch(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validateHistorySearch(args); err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			scope, ok := conversation.JournalScopeFromContext(ctx)
			if !ok {
				return types.ToolResult{IsError: true, Content: "Histórico indisponível fora de uma conversa identificada."}, nil
			}
			limit, _ := integerArg(args, "limit", 10, 20)
			before, _ := integerArg(args, "before_sequence", 0, math.MaxInt32)
			page, err := store.SearchJournal(ctx, scope, args["query"].(string), uint64(before), limit)
			if err != nil {
				return types.ToolResult{IsError: true, Content: "Busca no histórico: " + err.Error()}, nil
			}
			matches := make([]map[string]any, 0, len(page.Entries))
			for _, entry := range page.Entries {
				preview := []rune(entry.Message.Content)
				truncated := len(preview) > historySearchPreviewChars
				if truncated {
					preview = preview[:historySearchPreviewChars]
				}
				matches = append(matches, map[string]any{
					"sequence": entry.Sequence, "message_id": entry.Message.ID,
					"role": entry.Message.Role, "timestamp": entry.Message.Timestamp,
					"preview": string(preview), "preview_truncated": truncated,
				})
			}
			return historyJSON(map[string]any{
				"matches": matches, "scanned": page.Scanned, "next_before": page.NextBefore,
			})
		},
	})

	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "history_recall" },
		DescriptionFn: func() string {
			return "Ler o texto exato de uma mensagem ou resumo salvo desta conversa pelo message_id retornado por history_search ou citado no contexto. " +
				"Use start e max_chars para ler mensagens longas em partes."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"message_id": map[string]any{"type": "string", "description": "ID retornado por history_search"},
				"start":      map[string]any{"type": "integer", "minimum": 0, "description": "Deslocamento em caracteres Unicode"},
				"max_chars":  map[string]any{"type": "integer", "minimum": 1, "maximum": historyRecallMaxChars},
			}, "required": []string{"message_id"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateHistoryRecall(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn:    func(map[string]any) bool { return true },
		IsConcurrencyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validateHistoryRecall(args); err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			scope, ok := conversation.JournalScopeFromContext(ctx)
			if !ok {
				return types.ToolResult{IsError: true, Content: "Histórico indisponível fora de uma conversa identificada."}, nil
			}
			entry, found, err := store.ReadJournalScoped(ctx, scope, args["message_id"].(string))
			if err != nil {
				return types.ToolResult{IsError: true, Content: "Leitura do histórico: " + err.Error()}, nil
			}
			if !found {
				return types.ToolResult{IsError: true, Content: "Mensagem não encontrada neste histórico."}, nil
			}
			start, _ := integerArg(args, "start", 0, math.MaxInt32)
			maxChars, _ := integerArg(args, "max_chars", historyRecallDefaultChars, historyRecallMaxChars)
			content := []rune(entry.Message.Content)
			if start > len(content) {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("start excede %d caracteres", len(content))}, nil
			}
			end := min(start+maxChars, len(content))
			return historyJSON(map[string]any{
				"sequence": entry.Sequence, "message_id": entry.Message.ID,
				"role": entry.Message.Role, "timestamp": entry.Message.Timestamp,
				"content": string(content[start:end]), "start": start, "end": end,
				"total_chars": len(content), "next_start": nextHistoryStart(end, len(content)),
			})
		},
	})
}

func validateHistorySearch(args map[string]any) error {
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return fmt.Errorf("query é obrigatória")
	}
	limit, err := integerArg(args, "limit", 10, 20)
	if err != nil {
		return err
	}
	if limit < 1 {
		return fmt.Errorf("limit deve estar entre 1 e 20")
	}
	if _, err := integerArg(args, "before_sequence", 0, math.MaxInt32); err != nil {
		return err
	}
	return nil
}

func validateHistoryRecall(args map[string]any) error {
	id, ok := args["message_id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("message_id é obrigatório")
	}
	if _, err := integerArg(args, "start", 0, math.MaxInt32); err != nil {
		return err
	}
	if _, err := integerArg(args, "max_chars", historyRecallDefaultChars, historyRecallMaxChars); err != nil {
		return err
	}
	return nil
}

func nextHistoryStart(end, total int) int {
	if end >= total {
		return 0
	}
	return end
}

func historyJSON(value any) (types.ToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return types.ToolResult{IsError: true, Content: "Não foi possível formatar o histórico: " + err.Error()}, nil
	}
	return types.ToolResult{Content: string(raw)}, nil
}
