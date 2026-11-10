package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/conversation"
	"github.com/costa/polypod/internal/types"
)

// Remote memory writes are local to the authenticated conversation. The
// underlying stores use topic as a unique key, so a stable prefix is needed
// to allow two chats to save the same user-facing topic independently.
func scopedUserTopic(ctx context.Context, topic string) (string, string) {
	scope, ok := conversation.JournalScopeFromContext(ctx)
	if !ok || scope.Channel == "cli" {
		return topic, ""
	}
	sum := sha256.Sum256([]byte(scope.SessionID))
	return fmt.Sprintf("remote_%x_%s", sum[:12], topic), scope.SessionID
}

func displayMemoryTopic(m Memory) string {
	if m.Source == "user" && m.SessionID != "" {
		sum := sha256.Sum256([]byte(m.SessionID))
		prefix := fmt.Sprintf("remote_%x_", sum[:12])
		if strings.HasPrefix(m.Topic, prefix) {
			return strings.TrimPrefix(m.Topic, prefix)
		}
	}
	return m.Topic
}

func memoryScopeLabel(m Memory) string {
	if m.SessionID == "" {
		return "global"
	}
	return "conversa"
}

// scopedMemorySkillContext derives memory access from the trusted request.
// A model cannot select another chat or branch through tool arguments.
func scopedMemorySkillContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	scope, ok := conversation.JournalScopeFromContext(ctx)
	if !ok {
		return ctx
	}
	if scope.Channel == "cli" {
		ctx = WithLegacyEpisodeScope(ctx, scope.SessionID)
		if scope.BranchID == scope.SessionID {
			return WithSessionScope(ctx, scope.SessionID)
		}
		return WithSessionScope(ctx, "cli-branch:"+scope.BranchID)
	}
	return WithSessionScope(ctx, scope.SessionID)
}

func memorySkillString(args map[string]any, name string) (string, error) {
	value, exists := args[name]
	if !exists {
		return "", nil
	}
	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s deve ser texto", name)
	}
	return strings.TrimSpace(str), nil
}

func executeScopedMemorySkill(ctx context.Context, store Store, action string, args map[string]any) (types.ToolResult, error) {
	ctx = scopedMemorySkillContext(ctx)
	var text string
	var err error
	switch action {
	case "review_memories":
		var topic string
		topic, err = memorySkillString(args, "topic")
		if err == nil {
			var memories []Memory
			if reviewer, ok := store.(interface {
				Review(context.Context, string) ([]Memory, error)
			}); ok {
				memories, err = reviewer.Review(ctx, "")
			} else {
				memories, err = store.List(ctx)
			}
			if err == nil {
				var out strings.Builder
				for _, m := range memories {
					if topic != "" && m.Topic != topic && displayMemoryTopic(m) != topic &&
						!hasTag(m, "memory:parent="+topic) {
						localTopic, _ := scopedUserTopic(ctx, topic)
						if !hasTag(m, "memory:parent="+localTopic) {
							continue
						}
					}
					state := "ativa"
					if hasTag(m, forgottenTag) {
						state = "esquecida"
					} else if hasTag(m, supersededTag) {
						state = "substituida"
					}
					fmt.Fprintf(&out, "[%s] %s | %s | escopo=%s | fonte=%s | evidencias=%s | atualizada=%s\n",
						displayMemoryTopic(m), state, m.Content, memoryScopeLabel(m), m.Source,
						strings.Join(m.Tags, ","), m.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"))
				}
				text = out.String()
				if text == "" {
					text = "Nenhuma memoria para revisar."
				}
			}
		}
	case "save_memory":
		var topic, content string
		topic, err = memorySkillString(args, "topic")
		if err == nil {
			content, err = memorySkillString(args, "content")
		}
		if err == nil {
			if topic == "" || content == "" {
				err = fmt.Errorf("topic e content sao obrigatorios")
			} else {
				storageTopic, remoteScope := scopedUserTopic(ctx, topic)
				if remoteScope == "" {
					err = store.Save(ctx, storageTopic, content)
				} else {
					err = store.SaveWithMeta(ctx, Memory{Topic: storageTopic, Content: content,
						SessionID: remoteScope, Source: "user", Confidence: 1})
				}
				text = fmt.Sprintf("Memoria salva com sucesso: [%s]", topic)
			}
		}
	case "recall_memory":
		var query string
		query, err = memorySkillString(args, "query")
		if err == nil {
			if query == "" {
				err = fmt.Errorf("query e obrigatoria")
			} else {
				var memories []Memory
				memories, err = store.SearchRelevant(ctx, query, 20, 0.2)
				if err == nil {
					var out strings.Builder
					for _, m := range memories {
						fmt.Fprintf(&out, "- [%s] (%s): %s\n", displayMemoryTopic(m), memoryScopeLabel(m), m.Content)
					}
					text = out.String()
					if text == "" {
						text = "Nenhuma memoria encontrada para: " + query
					}
				}
			}
		}
	case "list_memories":
		var memories []Memory
		memories, err = store.List(ctx)
		if err == nil {
			if len(memories) == 0 {
				text = "Nenhuma memoria salva."
			} else {
				var out strings.Builder
				fmt.Fprintf(&out, "Total: %d memorias\n\n", len(memories))
				for _, m := range memories {
					fmt.Fprintf(&out, "- [%s] (%s): %s\n", displayMemoryTopic(m), memoryScopeLabel(m), m.Content)
				}
				text = out.String()
			}
		}
	case "delete_memory":
		var topic string
		topic, err = memorySkillString(args, "topic")
		if err == nil {
			if topic == "" {
				err = fmt.Errorf("topic e obrigatorio")
			} else {
				storageTopic, _ := scopedUserTopic(ctx, topic)
				err = store.Delete(ctx, storageTopic)
				text = fmt.Sprintf("Memoria deletada: [%s]", topic)
			}
		}
	default:
		err = fmt.Errorf("acao de memoria desconhecida: %s", action)
	}
	if err != nil {
		return types.ToolResult{IsError: true, Content: err.Error()}, nil
	}
	return types.ToolResult{Content: text}, nil
}
