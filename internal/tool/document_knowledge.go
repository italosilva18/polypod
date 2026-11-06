package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/document"
	"github.com/costa/polypod/internal/types"
	"github.com/costa/polypod/internal/whatsappgateway"
)

// RegisterDocumentKnowledgeTool exposes a persistent, chat-scoped document
// index. Local files are visible from the local CLI and the exact owner chat;
// other remote chats can search only attachments from their own chat.
func RegisterDocumentKnowledgeTool(reg *Registry, index *document.Index, ownerChat string) {
	if index == nil {
		return
	}
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "document_knowledge" },
		DescriptionFn: func() string {
			return "Indexar, pesquisar, ler trechos, remover e inspecionar documentos persistentes. " +
				"A busca retorna citações [doc:...] e respeita o escopo da conversa."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"action":    map[string]any{"type": "string", "enum": []string{"index_file", "search", "read", "delete", "status"}},
				"path":      map[string]any{"type": "string", "description": "Arquivo local a indexar"},
				"query":     map[string]any{"type": "string", "description": "Consulta de busca"},
				"source_id": map[string]any{"type": "string", "description": "ID da fonte retornado pela busca"},
				"scope":     map[string]any{"type": "string", "enum": []string{"local", "chat"}},
				"ordinal":   map[string]any{"type": "integer", "minimum": 1},
				"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
				"ocr":       map[string]any{"type": "boolean"},
			}, "required": []string{"action"}}
		},
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if err := validateKnowledgeArgs(args); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
			return types.ValidationResult{Valid: true}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "search" || action == "read" || action == "status"
		},
		IsConcurrencyFn: func(args map[string]any) bool {
			action, _ := args["action"].(string)
			return action == "search" || action == "read" || action == "status"
		},
		ExecuteFn: func(tc types.ToolContext, args map[string]any) (types.ToolResult, error) {
			if err := validateKnowledgeArgs(args); err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			ctx := tc.Context
			if ctx == nil {
				ctx = context.Background()
			}
			allowed := knowledgeScopes(ctx, ownerChat)
			scopes, err := selectKnowledgeScopes(allowed, args)
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			action := args["action"].(string)
			var value any
			switch action {
			case "index_file":
				if !containsScope(scopes, document.LocalScope) {
					return types.ToolResult{IsError: true, Content: "Indexação local indisponível nesta conversa."}, nil
				}
				path := strings.TrimSpace(args["path"].(string))
				if !filepath.IsAbs(path) {
					path = filepath.Join(tc.WorkingDir, path)
				}
				ocr, _ := args["ocr"].(bool)
				value, _, err = index.IndexFile(ctx, path, document.Options{
					OCR: ocr, OCRLanguage: os.Getenv("POLYPOD_OCR_LANG")})
			case "search":
				limit, _ := integerArg(args, "limit", 5, 10)
				var hits []document.SearchHit
				hits, err = index.Search(ctx, strings.TrimSpace(args["query"].(string)), scopes, limit)
				if err == nil {
					items := make([]map[string]any, 0, len(hits))
					for _, hit := range hits {
						items = append(items, map[string]any{"scope": knowledgeScopeLabel(hit.Scope),
							"source_id": hit.SourceID, "ordinal": hit.Ordinal, "citation": hit.Citation,
							"text": hit.Text, "score": hit.Score})
					}
					value = items
				}
			case "read":
				ordinal, _ := integerArg(args, "ordinal", 1, 1_000_000)
				for _, scope := range scopes {
					var hit document.SearchHit
					hit, err = index.ReadChunk(ctx, scope, args["source_id"].(string), ordinal)
					if err == nil {
						value = map[string]any{"scope": knowledgeScopeLabel(scope), "source_id": hit.SourceID,
							"ordinal": hit.Ordinal, "citation": hit.Citation, "text": hit.Text}
						break
					}
					if !errors.Is(err, document.ErrSourceNotFound) {
						break
					}
				}
			case "delete":
				removed := false
				for _, scope := range scopes {
					var done bool
					done, err = index.DeleteSource(ctx, scope, args["source_id"].(string))
					if err != nil || done {
						removed = done
						break
					}
				}
				value = map[string]any{"removed": removed}
			case "status":
				items := make(map[string]document.IndexStatus, len(scopes))
				for _, scope := range scopes {
					items[knowledgeScopeLabel(scope)], err = index.Status(ctx, scope)
					if err != nil {
						break
					}
				}
				value = items
			}
			if err != nil {
				return types.ToolResult{IsError: true, Content: fmt.Sprintf("Índice de documentos: %v", err)}, nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return types.ToolResult{IsError: true, Content: "Não foi possível formatar resultado do índice."}, nil
			}
			return types.ToolResult{Content: string(raw)}, nil
		},
	})
}

func validateKnowledgeArgs(args map[string]any) error {
	action, _ := args["action"].(string)
	needed := ""
	switch action {
	case "index_file":
		needed = "path"
	case "search":
		needed = "query"
	case "read", "delete":
		needed = "source_id"
	case "status":
	default:
		return errors.New("action deve ser index_file, search, read, delete ou status")
	}
	if needed != "" {
		value, _ := args[needed].(string)
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s é obrigatório", needed)
		}
	}
	if scope, ok := args["scope"].(string); ok && scope != "local" && scope != "chat" {
		return errors.New("scope deve ser local ou chat")
	}
	if _, err := integerArg(args, "limit", 5, 10); err != nil {
		return err
	}
	if _, err := integerArg(args, "ordinal", 1, 1_000_000); err != nil {
		return err
	}
	return nil
}

func knowledgeScopes(ctx context.Context, ownerChat string) []string {
	source, remote := whatsappgateway.InboundFromContext(ctx)
	if remote {
		if source.ChatID == ownerChat {
			return []string{source.ChatID, document.LocalScope}
		}
		return []string{source.ChatID}
	}
	if scope, ok := ctx.Value(remoteDocumentScopeKey{}).(string); ok && scope != "" {
		return []string{scope}
	}
	return []string{document.LocalScope}
}

type remoteDocumentScopeKey struct{}

// WithRemoteDocumentScope prevents a non-WhatsApp humanloop channel from
// inheriting the local CLI's document scope. The identity remains in memory.
func WithRemoteDocumentScope(ctx context.Context, channel, userID string) context.Context {
	if ctx == nil || channel == "" || userID == "" {
		return ctx
	}
	return context.WithValue(ctx, remoteDocumentScopeKey{}, channel+":"+userID)
}

func selectKnowledgeScopes(allowed []string, args map[string]any) ([]string, error) {
	requested, _ := args["scope"].(string)
	if requested == "" {
		return allowed, nil
	}
	for _, scope := range allowed {
		if knowledgeScopeLabel(scope) == requested {
			return []string{scope}, nil
		}
	}
	return nil, errors.New("escopo de documento não disponível nesta conversa")
}

func knowledgeScopeLabel(scope string) string {
	if scope == document.LocalScope {
		return "local"
	}
	return "chat"
}

func containsScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}
