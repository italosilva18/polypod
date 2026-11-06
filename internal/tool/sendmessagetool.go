package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/costa/polypod/internal/types"
)

// SendMessage represents a queued outbound message to a named channel.
type SendMessage struct {
	ID          string     `json:"id"`
	Channel     string     `json:"channel"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

// SendMessageStore holds pending messages with concurrent-safe access.
type SendMessageStore struct {
	mu       sync.Mutex
	messages []SendMessage
	counter  int
}

// NewSendMessageStore creates an empty SendMessageStore.
func NewSendMessageStore() *SendMessageStore {
	return &SendMessageStore{}
}

// globalSendMessageStore is the package-level singleton store.
var globalSendMessageStore = NewSendMessageStore()

// GetSendMessageStore returns the package-level singleton store.
func GetSendMessageStore() *SendMessageStore {
	return globalSendMessageStore
}

// Enqueue adds a new message to the store and returns it.
func (s *SendMessageStore) Enqueue(channel, body string) SendMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	msg := SendMessage{
		ID:        fmt.Sprintf("msg_%d", s.counter),
		Channel:   channel,
		Body:      body,
		CreatedAt: time.Now(),
	}
	s.messages = append(s.messages, msg)
	return msg
}

// Deliver attempts to deliver the message identified by id via HTTP POST to
// webhookURL (JSON payload). If webhookURL is empty the message is left
// pending. Returns nil on success.
func (s *SendMessageStore) Deliver(id string, webhookURL string) error {
	s.mu.Lock()
	idx := -1
	for i := range s.messages {
		if s.messages[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		s.mu.Unlock()
		return fmt.Errorf("mensagem %s nao encontrada", id)
	}
	msg := s.messages[idx]
	s.mu.Unlock()

	if webhookURL == "" {
		return nil
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("falha ao serializar mensagem: %w", err)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("falha ao enviar webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook retornou status %d", resp.StatusCode)
	}

	s.MarkDelivered(id)
	return nil
}

// ListPending returns messages that have not been delivered yet.
// If channel is non-empty only messages for that channel are returned.
func (s *SendMessageStore) ListPending(channel string) []SendMessage {
	s.mu.Lock()
	defer s.mu.Unlock()

	var result []SendMessage
	for _, m := range s.messages {
		if m.DeliveredAt != nil {
			continue
		}
		if channel != "" && m.Channel != channel {
			continue
		}
		result = append(result, m)
	}
	return result
}

// MarkDelivered records the delivery time for a message.
func (s *SendMessageStore) MarkDelivered(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for i := range s.messages {
		if s.messages[i].ID == id {
			s.messages[i].DeliveredAt = &now
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Tool constructors
// ---------------------------------------------------------------------------

// SendMessageEnqueueTool queues a message to the named channel and returns
// JSON of the newly created SendMessage.
var SendMessageEnqueueTool = types.BuildTool(types.ToolDef{
	NameFn: func() string { return "send_message_enqueue" },
	DescriptionFn: func() string {
		return "Enfileira uma mensagem para envio em um canal (ex: telegram, discord, slack, webhook)"
	},
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Nome do canal de destino (ex: telegram, discord, slack, webhook)",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Corpo da mensagem a ser enviada",
				},
			},
			"required": []string{"channel", "body"},
		}
	},
	IsDestructiveFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		channel, _ := args["channel"].(string)
		body, _ := args["body"].(string)
		if channel == "" {
			return types.ToolResult{Content: "channel e obrigatorio", IsError: true}, nil
		}
		if body == "" {
			return types.ToolResult{Content: "body e obrigatorio", IsError: true}, nil
		}

		msg := globalSendMessageStore.Enqueue(channel, body)

		data, err := json.Marshal(msg)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("erro ao serializar: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: string(data)}, nil
	},
})

// SendMessageListTool returns a JSON array of pending messages, optionally
// filtered by channel. An empty channel string means "all channels".
var SendMessageListTool = types.BuildTool(types.ToolDef{
	NameFn:        func() string { return "send_message_list" },
	DescriptionFn: func() string { return "Lista mensagens pendentes (nao entregues), opcionalmente filtradas por canal" },
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Filtrar por canal (deixe vazio para listar todos os canais)",
				},
			},
		}
	},
	IsReadOnlyFn:    func(map[string]any) bool { return true },
	IsConcurrencyFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		channel, _ := args["channel"].(string)
		pending := globalSendMessageStore.ListPending(channel)

		data, err := json.Marshal(pending)
		if err != nil {
			return types.ToolResult{Content: fmt.Sprintf("erro ao serializar: %v", err), IsError: true}, nil
		}
		return types.ToolResult{Content: string(data)}, nil
	},
})

// SendMessageDeliverTool attempts to deliver a queued message, optionally via
// an HTTP POST to webhook_url. Returns "entregue" when delivered or "na fila"
// when webhook_url is absent.
var SendMessageDeliverTool = types.BuildTool(types.ToolDef{
	NameFn: func() string { return "send_message_deliver" },
	DescriptionFn: func() string {
		return "Entrega uma mensagem enfileirada, enviando via HTTP POST se webhook_url for fornecida"
	},
	InputSchemaFn: func() map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "ID da mensagem a ser entregue",
				},
				"webhook_url": map[string]any{
					"type":        "string",
					"description": "URL de destino para entrega via HTTP POST (opcional)",
				},
			},
			"required": []string{"id"},
		}
	},
	IsDestructiveFn: func(map[string]any) bool { return true },
	ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
		id, _ := args["id"].(string)
		webhookURL, _ := args["webhook_url"].(string)
		if id == "" {
			return types.ToolResult{Content: "id e obrigatorio", IsError: true}, nil
		}

		if err := globalSendMessageStore.Deliver(id, webhookURL); err != nil {
			return types.ToolResult{Content: err.Error(), IsError: true}, nil
		}

		if webhookURL != "" {
			return types.ToolResult{Content: "entregue"}, nil
		}
		return types.ToolResult{Content: "na fila"}, nil
	},
})

// NewSendMessageTools returns all three send-message tool instances.
func NewSendMessageTools() []types.Tool {
	return []types.Tool{
		SendMessageEnqueueTool,
		SendMessageListTool,
		SendMessageDeliverTool,
	}
}
